//! HTTP situation tests for zord-router (no Postgres/Redis required).

use axum::body::Body;
use axum::http::{Request, StatusCode};
use http_body_util::BodyExt;
use jsonwebtoken::{Algorithm, EncodingKey, Header, encode};
use serde::Serialize;
use serde_json::{json, Value};
use std::time::{SystemTime, UNIX_EPOCH};
use tower::ServiceExt;
use zord_router::config::Config;
use zord_router::http::app;
use zord_router::service::AppState;

const TEST_ROUTER_TOKEN: &str = "test-router-token";

fn state() -> AppState {
    let mut config = Config::from_env();
    config.router_auth_token = Some(TEST_ROUTER_TOKEN.into());
    AppState::memory_only(config)
}

async fn json_request(
    router: axum::Router,
    method: &str,
    uri: &str,
    body: Option<Value>,
) -> (StatusCode, Value) {
    let builder = Request::builder()
        .method(method)
        .uri(uri)
        .header("content-type", "application/json")
        .header("x-router-token", TEST_ROUTER_TOKEN);
    let request = if let Some(body) = body {
        builder.body(Body::from(body.to_string())).unwrap()
    } else {
        builder.body(Body::empty()).unwrap()
    };
    let response = router.oneshot(request).await.unwrap();
    let status = response.status();
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    let value = if bytes.is_empty() {
        Value::Null
    } else {
        serde_json::from_slice(&bytes).unwrap_or_else(|_| json!({ "raw": String::from_utf8_lossy(&bytes) }))
    };
    (status, value)
}

#[tokio::test]
async fn health_and_processors() {
    let s = state();
    let (status, health) = json_request(app(s.clone()), "GET", "/v1/health", None).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(health["service"], "zord-router");
    assert_eq!(health["status"], "ok");
    assert_eq!(health["postgres"], "skipped");
    assert_eq!(health["redis"], "skipped");

    let (status, body) = json_request(app(s), "GET", "/v1/processors", None).await;
    assert_eq!(status, StatusCode::OK);
    let names: Vec<&str> = body["processors"]
        .as_array()
        .unwrap()
        .iter()
        .map(|p| p["psp"].as_str().unwrap())
        .collect();
    assert_eq!(names, vec!["razorpay", "cashfree", "payu", "stripe"]);
}

#[tokio::test]
async fn small_inr_payout_is_razorpay_imps() {
    let (status, body) = json_request(
        app(state()),
        "POST",
        "/v1/routing/route",
        Some(json!({
            "payment_id": "batch-001",
            "amount_minor": 560000,
            "currency": "INR",
            "payment_method": "IMPS",
            "direction": "OUTBOUND",
            "country": "IN"
        })),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["psp"], "razorpay");
    assert_eq!(body["rail"], "IMPS");
    assert_eq!(body["connector_id"], "razorpayx-v1");
    assert_eq!(body["routing_strategy"], "weighted");
    assert!(body["rules_applied"]
        .as_array()
        .unwrap()
        .iter()
        .any(|v| v == "rule_inr_prefer_razorpay"));
}

#[tokio::test]
async fn route_alias_and_bank_kind() {
    let (status, body) = json_request(
        app(state()),
        "POST",
        "/v1/route",
        Some(json!({
            "payment_id": "PAY-001",
            "amount_minor": 550000,
            "currency": "inr",
            "payment_method": "BANK",
            "direction": "OUTBOUND"
        })),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["rail"], "IMPS");
    assert_eq!(body["psp"], "razorpay");
}

#[tokio::test]
async fn two_lakh_imps_rewrites_to_neft_over_http() {
    let (status, body) = json_request(
        app(state()),
        "POST",
        "/v1/routing/route",
        Some(json!({
            "payment_id": "pout_2l",
            "amount_minor": 20_000_000,
            "currency": "INR",
            "payment_method": "IMPS",
            "direction": "OUTBOUND"
        })),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["rail"], "NEFT");
    assert_eq!(body["rail_rewritten_from"], "IMPS");
}

#[tokio::test]
async fn five_lakh_imps_rewrites_to_rtgs_over_http() {
    let (status, body) = json_request(
        app(state()),
        "POST",
        "/v1/routing/route",
        Some(json!({
            "payment_id": "pout_rtgs",
            "amount_minor": 50_000_000,
            "currency": "INR",
            "rail": "IMPS",
            "direction": "OUTBOUND"
        })),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["rail"], "RTGS");
}

#[tokio::test]
async fn usd_card_collect_selects_stripe() {
    let (status, body) = json_request(
        app(state()),
        "POST",
        "/v1/routing/route",
        Some(json!({
            "payment_id": "pay_usd",
            "amount_minor": 1999,
            "currency": "USD",
            "payment_method": "card",
            "direction": "INBOUND"
        })),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["psp"], "stripe");
    assert_eq!(body["rail"], "CARD");
}

#[tokio::test]
async fn stripe_cannot_do_inr_payouts() {
    let (status, body) = json_request(
        app(state()),
        "POST",
        "/v1/routing/route",
        Some(json!({
            "payment_id": "pout_card",
            "amount_minor": 1000,
            "currency": "INR",
            "payment_method": "CARD",
            "direction": "OUTBOUND"
        })),
    )
    .await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
    assert!(body["error"].as_str().unwrap().contains("no eligible PSP"));
}

#[tokio::test]
async fn missing_payment_id_is_bad_request() {
    let (status, body) = json_request(
        app(state()),
        "POST",
        "/v1/routing/route",
        Some(json!({
            "amount_minor": 1000,
            "currency": "INR",
            "payment_method": "IMPS",
            "direction": "OUTBOUND"
        })),
    )
    .await;
    assert_eq!(status, StatusCode::BAD_REQUEST);
    assert!(body["error"].as_str().unwrap().contains("required"));
}

#[tokio::test]
async fn rule_upsert_without_postgres_is_unavailable() {
    let (status, body) = json_request(
        app(state()),
        "POST",
        "/v1/routing/rules",
        Some(json!({
            "id": "rule_tmp",
            "name": "tmp",
            "priority": 1,
            "enabled": true,
            "condition": { "field": "currency", "operator": "equals", "value": "INR" },
            "action": { "type": "prefer", "processor": "cashfree" }
        })),
    )
    .await;
    assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
    assert!(body["error"].as_str().unwrap().contains("postgres"));
}

#[tokio::test]
async fn timeouts_trip_circuit_then_next_route_fails_over() {
    let s = state();
    for i in 0..5 {
        let (status, body) = json_request(
            app(s.clone()),
            "POST",
            "/v1/routing/outcome",
            Some(json!({
                "routing_id": format!("rte_{i}"),
                "payment_id": format!("pout_{i}"),
                "processor": "razorpay",
                "success": false,
                "failure_class": "TIMEOUT",
                "latency_ms": 800
            })),
        )
        .await;
        assert_eq!(status, StatusCode::OK);
        if i < 4 {
            assert_eq!(body["circuit_state"], "closed");
        } else {
            assert_eq!(body["circuit_state"], "open");
        }
    }

    let (status, body) = json_request(
        app(s),
        "POST",
        "/v1/routing/route",
        Some(json!({
            "payment_id": "pout_after_cb",
            "amount_minor": 10_000,
            "currency": "INR",
            "payment_method": "IMPS",
            "direction": "OUTBOUND"
        })),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["psp"], "cashfree");
    assert!(body["open_circuits"]
        .as_array()
        .unwrap()
        .iter()
        .any(|v| v == "razorpay"));
}

#[tokio::test]
async fn metrics_exposes_prometheus_counters() {
    let s = state();
    let _ = json_request(
        app(s.clone()),
        "POST",
        "/v1/routing/route",
        Some(json!({
            "payment_id": "pout_metrics",
            "amount_minor": 1000,
            "currency": "INR",
            "payment_method": "IMPS",
            "direction": "OUTBOUND"
        })),
    )
    .await;
    let request = Request::builder()
        .method("GET")
        .uri("/metrics")
        .body(Body::empty())
        .unwrap();
    let response = app(s).oneshot(request).await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let text = String::from_utf8(response.into_body().collect().await.unwrap().to_bytes().to_vec()).unwrap();
    assert!(text.contains("routing_requests_total"));
    assert!(text.contains("processor_selection_total"));
}

#[tokio::test]
async fn routing_route_without_credentials_is_unauthorized() {
    let request = Request::builder()
        .method("POST")
        .uri("/v1/routing/route")
        .header("content-type", "application/json")
        .body(Body::from(
            json!({
                "payment_id": "pout_noauth",
                "amount_minor": 1000,
                "currency": "INR",
                "payment_method": "IMPS",
                "direction": "OUTBOUND"
            })
            .to_string(),
        ))
        .unwrap();
    let response = app(state()).oneshot(request).await.unwrap();
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
}

#[derive(Serialize)]
struct TestClaims {
    tenant_id: String,
    user_id: String,
    iss: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    aud: Option<String>,
    exp: usize,
}

fn mint_jwt(secret: &str, tenant: &str) -> String {
    mint_jwt_aud(secret, tenant, Some("zord-console"))
}

// Edge (backend/edge/services/jwt_service.go) mints aud = "zord-console".
fn mint_jwt_aud(secret: &str, tenant: &str, aud: Option<&str>) -> String {
    let exp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs() as usize
        + 3600;
    encode(
        &Header::new(Algorithm::HS256),
        &TestClaims {
            tenant_id: tenant.into(),
            user_id: "user-1".into(),
            iss: "zord-edge".into(),
            aud: aud.map(str::to_string),
            exp,
        },
        &EncodingKey::from_secret(secret.as_bytes()),
    )
    .unwrap()
}

fn jwt_state() -> AppState {
    let mut config = Config::from_env();
    config.router_auth_token = None;
    config.jwt_signing_secret = Some("jwt-secret-for-tests".into());
    config.jwt_issuer = "zord-edge".into();
    config.jwt_audience = "zord-console".into();
    AppState::memory_only(config)
}

#[tokio::test]
async fn routing_route_accepts_matching_jwt() {
    let token = mint_jwt("jwt-secret-for-tests", "tenant-a");
    let request = Request::builder()
        .method("POST")
        .uri("/v1/routing/route")
        .header("content-type", "application/json")
        .header("authorization", format!("Bearer {token}"))
        .header("x-tenant-id", "tenant-a")
        .body(Body::from(
            json!({
                "payment_id": "pout_jwt",
                "tenant_id": "tenant-a",
                "amount_minor": 1000,
                "currency": "INR",
                "payment_method": "IMPS",
                "direction": "OUTBOUND"
            })
            .to_string(),
        ))
        .unwrap();
    let response = app(jwt_state()).oneshot(request).await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
}

#[tokio::test]
async fn routing_route_rejects_jwt_tenant_mismatch() {
    let token = mint_jwt("jwt-secret-for-tests", "tenant-a");
    let request = Request::builder()
        .method("POST")
        .uri("/v1/routing/route")
        .header("content-type", "application/json")
        .header("authorization", format!("Bearer {token}"))
        .header("x-tenant-id", "tenant-b")
        .body(Body::from(
            json!({
                "payment_id": "pout_jwt_mismatch",
                "tenant_id": "tenant-b",
                "amount_minor": 1000,
                "currency": "INR",
                "payment_method": "IMPS",
                "direction": "OUTBOUND"
            })
            .to_string(),
        ))
        .unwrap();
    let response = app(jwt_state()).oneshot(request).await.unwrap();
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
}

fn jwt_route_request(token: &str, payment_id: &str) -> Request<Body> {
    Request::builder()
        .method("POST")
        .uri("/v1/routing/route")
        .header("content-type", "application/json")
        .header("authorization", format!("Bearer {token}"))
        .header("x-tenant-id", "tenant-a")
        .body(Body::from(
            json!({
                "payment_id": payment_id,
                "tenant_id": "tenant-a",
                "amount_minor": 1000,
                "currency": "INR",
                "payment_method": "IMPS",
                "direction": "OUTBOUND"
            })
            .to_string(),
        ))
        .unwrap()
}

#[tokio::test]
async fn routing_route_accepts_edge_audience_zord_console() {
    let token = mint_jwt_aud("jwt-secret-for-tests", "tenant-a", Some("zord-console"));
    let response = app(jwt_state()).oneshot(jwt_route_request(&token, "pout_aud_ok")).await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
}

#[tokio::test]
async fn routing_route_rejects_wrong_audience() {
    let token = mint_jwt_aud("jwt-secret-for-tests", "tenant-a", Some("some-other-app"));
    let response = app(jwt_state()).oneshot(jwt_route_request(&token, "pout_aud_bad")).await.unwrap();
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn routing_route_rejects_missing_audience() {
    let token = mint_jwt_aud("jwt-secret-for-tests", "tenant-a", None);
    let response = app(jwt_state()).oneshot(jwt_route_request(&token, "pout_aud_none")).await.unwrap();
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
}

#[test]
fn router_default_audience_matches_edge() {
    std::env::remove_var("JWT_AUDIENCE");
    assert_eq!(Config::from_env().jwt_audience, "zord-console");
}
