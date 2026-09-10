use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use tokio::sync::RwLock;
use zord_router::catalog::processors;
use zord_router::circuit::CircuitPolicy;
use zord_router::config::Config;
use zord_router::http::app;
use zord_router::live::LiveSnapshot;
use zord_router::service::AppState;
use zord_router::store::postgres::Postgres;
use zord_router::store::redis::RedisLive;
use zord_router::telemetry::{init_tracing, PromMetrics};

#[tokio::main]
async fn main() {
    let config = Config::from_env();
    init_tracing(config.otel_endpoint.as_deref());

    let circuit_policy = CircuitPolicy {
        failure_threshold: config.circuit_failure_threshold,
        error_rate: config.circuit_error_rate,
        reset: config.circuit_reset,
    };
    let cache = Arc::new(RwLock::new(LiveSnapshot::static_seed(&processors())));

    let postgres = match config.database_url.as_deref() {
        Some(url) => match Postgres::connect(url, config.pg_timeout).await {
            Ok(pg) => {
                tracing::info!("postgres connected");
                Some(pg)
            }
            Err(err) => {
                tracing::error!(error = %err, "postgres unavailable; rules stay in-process");
                None
            }
        },
        None => {
            tracing::warn!("DATABASE_URL unset; using seed rules");
            None
        }
    };

    let redis = match config.redis_url.as_deref() {
        Some(url) => match RedisLive::connect(url, config.redis_timeout.max(Duration::from_millis(400))).await {
            Ok(r) => {
                tracing::info!("redis connected");
                Some(r)
            }
            Err(err) => {
                tracing::error!(error = %err, "redis unavailable; live metrics stay static/cache");
                None
            }
        },
        None => {
            tracing::warn!("REDIS_URL unset; metrics_source=static_config");
            None
        }
    };

    let state = AppState {
        config: config.clone(),
        postgres,
        redis,
        cache,
        metrics: Arc::new(PromMetrics::default()),
        circuit_policy,
    };

    let port = config.port;
    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .expect("bind router");
    tracing::info!(%addr, "zord-router listening");
    axum::serve(listener, app(state))
        .with_graceful_shutdown(shutdown())
        .await
        .expect("serve");
}

async fn shutdown() {
    let _ = tokio::signal::ctrl_c().await;
    tracing::info!("zord-router shutting down");
}
