//! HTTP situation tests for zord-router (no Postgres/Redis required).

use axum::body::Body;
use axum::http::{Request, StatusCode};
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use zord_router::config::Config;
use zord_router::http::app;
use zord_router::service::AppState;

fn state() -> AppState {
    AppState::memory_only(Config::from_env())
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
        .header("content-type", "application/json");
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
