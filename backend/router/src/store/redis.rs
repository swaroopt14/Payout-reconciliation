use redis::AsyncCommands;
use redis::aio::ConnectionManager;
use tokio::time::{timeout, Duration};

use crate::circuit::{now_unix, record, CircuitPolicy, CircuitState};
use crate::live::{LiveMetrics, LiveSnapshot};
use crate::types::Psp;

#[derive(Clone)]
pub struct RedisLive {
    conn: ConnectionManager,
    timeout: Duration,
}

impl RedisLive {
    pub async fn connect(url: &str, wait: Duration) -> Result<Self, String> {
        let client = redis::Client::open(url).map_err(|e| e.to_string())?;
        let conn = timeout(wait.max(Duration::from_millis(400)), ConnectionManager::new(client))
            .await
            .map_err(|_| "redis connect timeout".to_string())?
            .map_err(|e| e.to_string())?;
        Ok(Self { conn, timeout: wait })
    }

    pub async fn load(&self, snapshot: &mut LiveSnapshot) -> Result<(), String> {
        let mut conn = self.conn.clone();
        for psp in [Psp::Razorpay, Psp::Cashfree, Psp::Payu, Psp::Stripe] {
            let key = format!("zord:processor:{}:metrics", psp.as_str());
            let ckey = format!("zord:processor:{}:circuit", psp.as_str());
            let metrics: Result<Vec<(String, String)>, _> = timeout(self.timeout, conn.hgetall(key)).await
                .map_err(|_| "redis timeout".to_string())?;
            if let Ok(pairs) = metrics {
                if !pairs.is_empty() {
                    let map: std::collections::HashMap<_, _> = pairs.into_iter().collect();
                    snapshot.metrics.insert(
                        psp,
                        LiveMetrics {
                            authorization_rate: parse_f(&map, "authorization_rate", 0.9),
                            health_score: parse_f(&map, "health_score", 0.9),
                            average_latency_ms: parse_f(&map, "latency_ms", 200.0),
                            error_rate: parse_f(&map, "error_rate", 0.0),
                            samples: parse_f(&map, "samples", 0.0) as u64,
                        },
                    );
                    snapshot.source = "redis".into();
                }
            }
            let circuit: Result<Vec<(String, String)>, _> =
                timeout(self.timeout, conn.hgetall(ckey)).await.map_err(|_| "redis timeout".to_string())?;
            if let Ok(pairs) = circuit {
                if !pairs.is_empty() {
                    let map: std::collections::HashMap<_, _> = pairs.into_iter().collect();
                    snapshot.circuits.insert(
                        psp,
                        CircuitState {
                            processor: psp,
                            state: match map.get("state").map(|s| s.as_str()).unwrap_or("closed") {
                                "open" => crate::circuit::Circuit::Open,
                                "half_open" => crate::circuit::Circuit::HalfOpen,
                                _ => crate::circuit::Circuit::Closed,
                            },
                            consecutive_infra_failures: parse_f(&map, "consecutive_infra_failures", 0.0) as u32,
                            samples: parse_f(&map, "samples", 0.0) as u32,
                            infra_failures: parse_f(&map, "infra_failures", 0.0) as u32,
                            opened_at_unix: {
                                let n = parse_f(&map, "opened_at_unix", 0.0) as u64;
                                if n == 0 { None } else { Some(n) }
                            },
                        },
                    );
                }
            }
        }
        Ok(())
    }

    pub async fn write_processor(
        &self,
        psp: Psp,
        metrics: &LiveMetrics,
        circuit: &CircuitState,
    ) -> Result<(), String> {
        let mut conn = self.conn.clone();
        let key = format!("zord:processor:{}:metrics", psp.as_str());
        let ckey = format!("zord:processor:{}:circuit", psp.as_str());
        let mfields = [
            ("authorization_rate", metrics.authorization_rate.to_string()),
            ("health_score", metrics.health_score.to_string()),
            ("latency_ms", metrics.average_latency_ms.to_string()),
            ("error_rate", metrics.error_rate.to_string()),
            ("samples", metrics.samples.to_string()),
        ];
        timeout(self.timeout, conn.hset_multiple::<_, _, _, ()>(key, &mfields))
            .await
            .map_err(|_| "redis timeout".to_string())?
            .map_err(|e| e.to_string())?;
        let state = match circuit.state {
            crate::circuit::Circuit::Open => "open",
            crate::circuit::Circuit::HalfOpen => "half_open",
            crate::circuit::Circuit::Closed => "closed",
        };
        let cfields = [
            ("state", state.to_string()),
            ("consecutive_infra_failures", circuit.consecutive_infra_failures.to_string()),
            ("samples", circuit.samples.to_string()),
            ("infra_failures", circuit.infra_failures.to_string()),
            ("opened_at_unix", circuit.opened_at_unix.unwrap_or(0).to_string()),
        ];
        timeout(self.timeout, conn.hset_multiple::<_, _, _, ()>(ckey, &cfields))
            .await
            .map_err(|_| "redis timeout".to_string())?
            .map_err(|e| e.to_string())?;
        Ok(())
    }

    pub async fn ping(&self) -> bool {
        let mut conn = self.conn.clone();
        timeout(self.timeout, redis::cmd("PING").query_async::<String>(&mut conn))
            .await
            .ok()
            .and_then(|r| r.ok())
            .is_some()
    }
}

pub fn apply_local_outcome(
    snapshot: &mut LiveSnapshot,
    psp: Psp,
    success: bool,
    infra_failure: bool,
    latency_ms: Option<f64>,
    policy: &CircuitPolicy,
) -> CircuitState {
    let metrics = snapshot
        .metrics
        .entry(psp)
        .or_insert_with(|| LiveMetrics {
            authorization_rate: 0.9,
            health_score: 0.9,
            average_latency_ms: 200.0,
            error_rate: 0.0,
            samples: 0,
        });
    metrics.apply_outcome(success, infra_failure, latency_ms);
    let current = snapshot
        .circuits
        .get(&psp)
        .cloned()
        .unwrap_or_else(|| CircuitState::closed(psp));
    let next = record(current, infra_failure, success, policy, now_unix());
    snapshot.circuits.insert(psp, next.clone());
    if snapshot.source == "static_config" {
        snapshot.source = "cache".into();
    }
    next
}

fn parse_f(map: &std::collections::HashMap<String, String>, key: &str, default: f64) -> f64 {
    map.get(key).and_then(|s| s.parse().ok()).unwrap_or(default)
}
