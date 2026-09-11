use std::time::Duration;

use axum::{
    extract::{Extension, State},
    http::{HeaderMap, StatusCode},
    middleware,
    routing::{get, post},
    Json, Router,
};
use tower_http::timeout::TimeoutLayer;
use tower_http::trace::TraceLayer;

use crate::auth::{self, Principal};
use crate::service::AppState;
use crate::types::{ErrorBody, OutcomeRequest, RouteDecision, RouteRequest, RoutingRule};

pub fn app(state: AppState) -> Router {
    let timeout = state.config.request_timeout;
    let protected = Router::new()
        .route("/v1/routing/rules", get(rules).post(upsert_rule))
        .route("/v1/routing/route", post(decide))
        .route("/v1/route", post(decide))
        .route("/v1/routing/outcome", post(outcome))
        .route_layer(middleware::from_fn_with_state(state.clone(), auth::require_auth));
    Router::new()
        .route("/health", get(health))
        .route("/ready", get(ready))
        .route("/metrics", get(metrics))
        .route("/v1/health", get(health))
        .route("/v1/processors", get(list_processors))
        .route("/v1/connectors", get(list_processors))
        .merge(protected)
        .with_state(state)
        .layer(TimeoutLayer::new(timeout.max(Duration::from_millis(50))))
        .layer(TraceLayer::new_for_http())
}

async fn health(State(state): State<AppState>) -> Json<serde_json::Value> {
    Json(state.health_body().await)
}

async fn ready(State(state): State<AppState>) -> Json<serde_json::Value> {
    let mut body = state.health_body().await;
    body["status"] = serde_json::json!("ready");
    Json(body)
}

async fn metrics(State(state): State<AppState>) -> String {
    state.metrics.render()
}

async fn list_processors(State(state): State<AppState>) -> Json<serde_json::Value> {
    let catalog = state.catalog().await;
    Json(serde_json::json!({
        "processors": catalog,
        "connectors": catalog
    }))
}

async fn rules(State(state): State<AppState>) -> Json<serde_json::Value> {
    Json(serde_json::json!({ "rules": state.rules(None).await }))
}

async fn upsert_rule(
    State(state): State<AppState>,
    principal: Option<Extension<Principal>>,
    Json(rule): Json<RoutingRule>,
) -> Result<Json<RoutingRule>, (StatusCode, Json<ErrorBody>)> {
    if auth::tenant_forbidden(principal.as_deref(), rule.merchant_id.as_deref()) {
        return Err((
            StatusCode::FORBIDDEN,
            Json(ErrorBody {
                error: "requested tenant is not authorised for this principal".into(),
            }),
        ));
    }
    let Some(pg) = &state.postgres else {
        return Err((
            StatusCode::SERVICE_UNAVAILABLE,
            Json(ErrorBody {
                error: "postgres not configured".into(),
            }),
        ));
    };
    pg.upsert_rule(&rule).await.map_err(|error| {
        (
            StatusCode::BAD_GATEWAY,
            Json(ErrorBody { error }),
        )
    })?;
    Ok(Json(rule))
}

async fn decide(
    State(state): State<AppState>,
    headers: HeaderMap,
    principal: Option<Extension<Principal>>,
    Json(req): Json<RouteRequest>,
) -> Result<Json<RouteDecision>, (StatusCode, Json<ErrorBody>)> {
    if auth::tenant_forbidden(principal.as_deref(), req.tenant_id.as_deref()) {
        return Err((
            StatusCode::FORBIDDEN,
            Json(ErrorBody {
                error: "requested tenant is not authorised for this principal".into(),
            }),
        ));
    }
    let idempotency = headers
        .get("Idempotency-Key")
        .and_then(|v| v.to_str().ok())
        .map(|s| s.to_string());
    match state.decide(req, idempotency).await {
        Ok(d) => Ok(Json(d)),
        Err(error) => {
            state.metrics.observe_route("none", 0, false, 0);
            let status = if error.contains("required") {
                StatusCode::BAD_REQUEST
            } else {
                StatusCode::UNPROCESSABLE_ENTITY
            };
            Err((status, Json(ErrorBody { error })))
        }
    }
}

async fn outcome(
    State(state): State<AppState>,
    Json(req): Json<OutcomeRequest>,
) -> Result<Json<serde_json::Value>, (StatusCode, Json<ErrorBody>)> {
    match state.record_outcome(req).await {
        Ok(res) => Ok(Json(serde_json::to_value(res).unwrap_or_else(|_| serde_json::json!({})))),
        Err(error) => Err((StatusCode::BAD_REQUEST, Json(ErrorBody { error }))),
    }
}
