use std::time::Duration;

#[derive(Debug, Clone)]
pub struct Config {
    pub port: u16,
    pub database_url: Option<String>,
    pub redis_url: Option<String>,
    pub pg_timeout: Duration,
    pub redis_timeout: Duration,
    pub request_timeout: Duration,
    pub circuit_failure_threshold: u32,
    pub circuit_error_rate: f64,
    pub circuit_reset: Duration,
    pub otel_endpoint: Option<String>,
}

impl Config {
    pub fn from_env() -> Self {
        Self {
            port: parse_u16("PORT", 8091),
            database_url: nonempty("DATABASE_URL"),
            redis_url: nonempty("REDIS_URL"),
            pg_timeout: Duration::from_millis(parse_u64("ROUTER_PG_TIMEOUT_MS", 200)),
            redis_timeout: Duration::from_millis(parse_u64("ROUTER_REDIS_TIMEOUT_MS", 50)),
            request_timeout: Duration::from_millis(parse_u64("ROUTER_REQUEST_TIMEOUT_MS", 800)),
            circuit_failure_threshold: parse_u64("CIRCUIT_FAILURE_THRESHOLD", 5) as u32,
            circuit_error_rate: parse_f64("CIRCUIT_ERROR_RATE", 0.10),
            circuit_reset: Duration::from_secs(parse_u64("CIRCUIT_RESET_SECS", 30)),
            otel_endpoint: nonempty("OTEL_EXPORTER_OTLP_ENDPOINT"),
        }
    }
}

fn nonempty(key: &str) -> Option<String> {
    std::env::var(key)
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
}

fn parse_u16(key: &str, default: u16) -> u16 {
    std::env::var(key)
        .ok()
        .and_then(|s| s.parse().ok())
        .unwrap_or(default)
}

fn parse_u64(key: &str, default: u64) -> u64 {
    std::env::var(key)
        .ok()
        .and_then(|s| s.parse().ok())
        .unwrap_or(default)
}

fn parse_f64(key: &str, default: f64) -> f64 {
    std::env::var(key)
        .ok()
        .and_then(|s| s.parse().ok())
        .unwrap_or(default)
}
