use axum::extract::Request;
use axum::http::{header, StatusCode};
use axum::middleware::Next;
use axum::response::Response;
use axum::{Json, extract::State};
use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode};
use serde::Deserialize;

use crate::service::AppState;
use crate::types::ErrorBody;

#[derive(Debug, Clone)]
pub struct Principal {
    pub tenant_id: String,
}

#[derive(Debug, Deserialize)]
struct AccessClaims {
    tenant_id: String,
    user_id: String,
    iss: Option<String>,
}

pub async fn require_auth(
    State(state): State<AppState>,
    mut req: Request,
    next: Next,
) -> Result<Response, (StatusCode, Json<ErrorBody>)> {
    let token_ok = header_token_ok(req.headers().get("x-router-token"), state.config.router_auth_token.as_deref());
    if token_ok {
        return Ok(next.run(req).await);
    }

    let Some(secret) = state.config.jwt_signing_secret.as_deref() else {
        return Err(deny(
            StatusCode::UNAUTHORIZED,
            "missing or invalid router credentials",
        ));
    };

    let bearer = req
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    let jwt = bearer
        .strip_prefix("Bearer ")
        .or_else(|| bearer.strip_prefix("bearer "))
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .ok_or_else(|| deny(StatusCode::UNAUTHORIZED, "missing or invalid router credentials"))?;

    let principal = verify_jwt(jwt, secret, &state.config.jwt_issuer)?;
    if let Some(requested) = requested_tenant(req.headers()) {
        if !requested.eq_ignore_ascii_case(&principal.tenant_id) {
            return Err(deny(
                StatusCode::FORBIDDEN,
                "requested tenant is not authorised for this principal",
            ));
        }
    }
    req.extensions_mut().insert(principal);
    Ok(next.run(req).await)
}

pub fn tenant_forbidden(principal: Option<&Principal>, body_tenant: Option<&str>) -> bool {
    let Some(p) = principal else {
        return false;
    };
    let Some(body) = body_tenant.map(str::trim).filter(|s| !s.is_empty()) else {
        return false;
    };
    !body.eq_ignore_ascii_case(&p.tenant_id)
}

fn requested_tenant(headers: &axum::http::HeaderMap) -> Option<String> {
    for name in ["x-tenant-id", "tenant-id", "tenant_id"] {
        if let Some(v) = headers.get(name).and_then(|v| v.to_str().ok()).map(str::trim) {
            if !v.is_empty() {
                return Some(v.to_string());
            }
        }
    }
    None
}

fn header_token_ok(header: Option<&axum::http::HeaderValue>, expected: Option<&str>) -> bool {
    let Some(expected) = expected.map(str::trim).filter(|s| !s.is_empty()) else {
        return false;
    };
    let Some(got) = header.and_then(|v| v.to_str().ok()).map(str::trim) else {
        return false;
    };
    constant_eq(got.as_bytes(), expected.as_bytes())
}

fn verify_jwt(token: &str, secret: &str, issuer: &str) -> Result<Principal, (StatusCode, Json<ErrorBody>)> {
    let mut validation = Validation::new(Algorithm::HS256);
    validation.set_issuer(&[issuer]);
    validation.set_required_spec_claims(&["exp", "iss"]);
    let data = decode::<AccessClaims>(token, &DecodingKey::from_secret(secret.as_bytes()), &validation)
        .map_err(|_| deny(StatusCode::UNAUTHORIZED, "missing or invalid router credentials"))?;
    if data.claims.tenant_id.trim().is_empty() || data.claims.user_id.trim().is_empty() {
        return Err(deny(StatusCode::UNAUTHORIZED, "token missing tenant_id or user_id"));
    }
    if let Some(iss) = data.claims.iss.as_deref() {
        if !iss.eq_ignore_ascii_case(issuer) {
            return Err(deny(StatusCode::UNAUTHORIZED, "missing or invalid router credentials"));
        }
    }
    Ok(Principal {
        tenant_id: data.claims.tenant_id.trim().to_string(),
    })
}

fn constant_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let mut acc = 0u8;
    for (x, y) in a.iter().zip(b.iter()) {
        acc |= x ^ y;
    }
    acc == 0
}

fn deny(status: StatusCode, error: &str) -> (StatusCode, Json<ErrorBody>) {
    (
        status,
        Json(ErrorBody {
            error: error.to_string(),
        }),
    )
}
