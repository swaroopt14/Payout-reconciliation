use std::sync::Arc;
use tokio::sync::RwLock;
use tokio::time::Instant;

use crate::catalog::processors;
use crate::circuit::{now_unix, CircuitPolicy};
use crate::config::Config;
use crate::live::LiveSnapshot;
use crate::routing::engine::{route_with, to_info, RouteInputs};
use crate::routing::rules::seed_rules;
use crate::store::postgres::Postgres;
use crate::store::redis::{apply_local_outcome, RedisLive};
use crate::telemetry::PromMetrics;
use crate::triage::triage;
use crate::types::{
    ConnectorInfo, OutcomeRequest, OutcomeResponse, Psp, RouteDecision, RouteRequest, RoutingRule,
};

#[derive(Clone)]
pub struct AppState {
    pub config: Config,
    pub postgres: Option<Postgres>,
    pub redis: Option<RedisLive>,
    pub cache: Arc<RwLock<LiveSnapshot>>,
    pub metrics: Arc<PromMetrics>,
    pub circuit_policy: CircuitPolicy,
}

impl AppState {
    pub fn memory_only(config: Config) -> Self {
        let cache = LiveSnapshot::static_seed(&processors());
        let circuit_policy = CircuitPolicy {
            failure_threshold: config.circuit_failure_threshold,
            error_rate: config.circuit_error_rate,
            reset: config.circuit_reset,
        };
        Self {
            config,
            postgres: None,
            redis: None,
            cache: Arc::new(RwLock::new(cache)),
            metrics: Arc::new(PromMetrics::default()),
            circuit_policy,
        }
    }

    pub async fn snapshot(&self) -> LiveSnapshot {
        let mut snap = self.cache.read().await.clone();
        if let Some(redis) = &self.redis {
            match redis.load(&mut snap).await {
                Ok(()) => {
                    let mut w = self.cache.write().await;
                    *w = snap.clone();
                }
                Err(err) => {
                    tracing::warn!(error = %err, "redis live metrics unavailable; using cache/static");
                    if snap.source == "static_config" {
                        snap.source = "cache".into();
                    }
                }
            }
        }
        snap
    }

    pub async fn rules(&self, merchant_id: Option<&str>) -> Vec<RoutingRule> {
        if let Some(pg) = &self.postgres {
            match pg.load_rules(merchant_id).await {
                Ok(rules) if !rules.is_empty() => return rules,
                Ok(_) => tracing::warn!("postgres returned no routing rules; using seed"),
                Err(err) => tracing::warn!(error = %err, "postgres rules unavailable; using seed"),
            }
        }
        seed_rules()
    }

    pub async fn decide(
        &self,
        req: RouteRequest,
        idempotency_key: Option<String>,
    ) -> Result<RouteDecision, String> {
        let started = Instant::now();
        let payment_id = first_id(&req, idempotency_key.as_deref());
        if let Some(id) = payment_id.as_deref() {
            if let Some(pg) = &self.postgres {
                if let Ok(Some(existing)) = pg.get_decision(id).await {
                    tracing::info!(payment_id = id, "idempotent routing hit");
                    return Ok(existing);
                }
            }
        }

        let snap = self.snapshot().await;
        let now = now_unix();
        let open = snap.open_circuits(now, &self.circuit_policy);
        let overlay = snap.overlay(processors());
        let rules = self.rules(req.merchant_id.as_deref()).await;
        let inputs = RouteInputs {
            processors: overlay,
            rules,
            open_circuits: open,
            metrics_source: snap.source.clone(),
        };
        let decision = route_with(req, inputs)?;
        self.metrics.observe_route(
            decision.psp.as_str(),
            started.elapsed().as_millis() as u64,
            true,
            decision.fallbacks.len(),
        );
        if let Some(pg) = &self.postgres {
            if let Err(err) = pg
                .insert_decision(&decision, decision_merchant(&decision))
                .await
            {
                tracing::warn!(error = %err, "failed to persist routing decision");
            }
        }
        tracing::info!(
            payment_id = %decision.payment_id,
            processor = decision.psp.as_str(),
            rail = decision.rail.as_str(),
            strategy = %decision.routing_strategy,
            metrics_source = %decision.metrics_source,
            "routing decision"
        );
        Ok(decision)
    }

    pub async fn catalog(&self) -> Vec<ConnectorInfo> {
        let snap = self.snapshot().await;
        snap.overlay(processors())
            .into_iter()
            .map(|p| {
                let label = snap.circuit_label(p.id);
                to_info(&p, &label)
            })
            .collect()
    }

    pub async fn record_outcome(&self, req: OutcomeRequest) -> Result<OutcomeResponse, String> {
        let psp = Psp::parse(&req.processor).ok_or_else(|| "unknown processor".to_string())?;
        let t = triage(req.success, req.failure_class.as_deref());
        let mut snap = self.cache.write().await;
        let circuit = apply_local_outcome(
            &mut snap,
            psp,
            req.success,
            t.counts_toward_circuit,
            req.latency_ms,
            &self.circuit_policy,
            t.merchant_action,
        );
        let metrics = snap.metrics.get(&psp).cloned();
        let source = snap.source.clone();
        drop(snap);

        if let Some(redis) = &self.redis {
            if let Some(m) = metrics.as_ref() {
                if let Err(err) = redis.write_processor(psp, m, &circuit).await {
                    tracing::warn!(error = %err, "redis outcome write failed");
                }
            }
        }
        if let Some(pg) = &self.postgres {
            let triage_json = serde_json::to_value(&t).unwrap_or(serde_json::json!({}));
            if let Err(err) = pg
                .insert_outcome(
                    req.routing_id.as_deref().unwrap_or(""),
                    &req.payment_id,
                    psp.as_str(),
                    req.success,
                    req.latency_ms,
                    req.failure_class.as_deref(),
                    t.counts_toward_circuit,
                    t.use_fallback,
                    &triage_json,
                )
                .await
            {
                tracing::warn!(error = %err, "postgres outcome write failed");
            }
        }
        let opened = circuit.state == crate::circuit::Circuit::Open;
        self.metrics.observe_outcome(opened);
        tracing::info!(
            payment_id = %req.payment_id,
            processor = psp.as_str(),
            success = req.success,
            failure_class = ?t.failure_class,
            circuit = ?circuit.state,
            "routing outcome triaged"
        );
        Ok(OutcomeResponse {
            payment_id: req.payment_id,
            processor: psp,
            triage: t,
            circuit_state: match circuit.state {
                crate::circuit::Circuit::Open => "open".into(),
                crate::circuit::Circuit::HalfOpen => "half_open".into(),
                crate::circuit::Circuit::Closed => "closed".into(),
            },
            metrics_source: source,
        })
    }

    pub async fn health_body(&self) -> serde_json::Value {
        let pg = match &self.postgres {
            Some(p) => {
                if p.ping().await {
                    "ok"
                } else {
                    "error"
                }
            }
            None => "skipped",
        };
        let redis = match &self.redis {
            Some(r) => {
                if r.ping().await {
                    "ok"
                } else {
                    "error"
                }
            }
            None => "skipped",
        };
        let source = self.cache.read().await.source.clone();
        serde_json::json!({
            "status": "ok",
            "service": "zord-router",
            "engine": "eligibility+rules+scoring+fallback+circuit",
            "metrics_source": source,
            "postgres": pg,
            "redis": redis,
            "otel": self.config.otel_endpoint.is_some(),
        })
    }
}

fn first_id(req: &RouteRequest, idempotency: Option<&str>) -> Option<String> {
    if let Some(k) = idempotency {
        let t = k.trim();
        if !t.is_empty() {
            return Some(t.to_string());
        }
    }
    req.payment_id
        .as_deref()
        .or(req.entity_id.as_deref())
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
}

fn decision_merchant(_d: &RouteDecision) -> Option<&str> {
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::{Direction, Rail, RouteRequest};

    fn payout(id: &str) -> RouteRequest {
        RouteRequest {
            tenant_id: None,
            merchant_id: None,
            entity_id: Some(id.into()),
            payment_id: None,
            direction: Some(Direction::Outbound),
            rail: Some(Rail::Imps),
            payment_method: None,
            card_network: None,
            amount_minor: Some(10_000),
            amount: None,
            currency: "INR".into(),
            country: None,
            ml_score: None,
        }
    }

    #[tokio::test]
    async fn timeouts_trip_circuit_and_fail_over() {
        let state = AppState::memory_only(Config::from_env());
        for i in 0..5 {
            let res = state
                .record_outcome(OutcomeRequest {
                    routing_id: Some(format!("rte_{i}")),
                    payment_id: format!("pout_{i}"),
                    processor: "razorpay".into(),
                    success: false,
                    latency_ms: Some(800.0),
                    failure_class: Some("TIMEOUT".into()),
                    failure_detail: None,
                })
                .await
                .unwrap();
            if i < 4 {
                assert_eq!(res.circuit_state, "closed");
            } else {
                assert_eq!(res.circuit_state, "open");
            }
        }
        let d = state.decide(payout("pout_after_cb"), None).await.unwrap();
        assert_eq!(d.psp, Psp::Cashfree);
        assert!(d.open_circuits.contains(&Psp::Razorpay));
    }

    #[tokio::test]
    async fn hard_declines_keep_razorpay_in_pool() {
        let state = AppState::memory_only(Config::from_env());
        for i in 0..6 {
            let res = state
                .record_outcome(OutcomeRequest {
                    routing_id: Some(format!("rte_hd_{i}")),
                    payment_id: format!("pout_hd_{i}"),
                    processor: "razorpay".into(),
                    success: false,
                    latency_ms: Some(120.0),
                    failure_class: Some("HARD_DECLINE".into()),
                    failure_detail: None,
                })
                .await
                .unwrap();
            assert_eq!(res.circuit_state, "closed");
            assert!(!res.triage.counts_toward_circuit);
            assert!(res.triage.use_fallback);
        }
        let d = state.decide(payout("pout_after_hd"), None).await.unwrap();
        assert!(
            !d.open_circuits.contains(&Psp::Razorpay),
            "hard declines must not open the circuit: {:?}",
            d.open_circuits
        );
        assert_eq!(d.psp, Psp::Cashfree, "auth EWMA should fail over without a circuit trip");
    }

    #[tokio::test]
    async fn insufficient_funds_does_not_fail_over_the_processor() {
        let state = AppState::memory_only(Config::from_env());
        let res = state
            .record_outcome(OutcomeRequest {
                routing_id: Some("rte_funds".into()),
                payment_id: "pout_funds".into(),
                processor: "razorpay".into(),
                success: false,
                latency_ms: Some(90.0),
                failure_class: Some("INSUFFICIENT_FUNDS".into()),
                failure_detail: None,
            })
            .await
            .unwrap();
        assert!(res.triage.merchant_action);
        assert!(!res.triage.use_fallback);
        assert_eq!(res.circuit_state, "closed");
        let d = state.decide(payout("pout_after_funds"), None).await.unwrap();
        assert_eq!(d.psp, Psp::Razorpay);
    }

    #[tokio::test]
    async fn unknown_processor_outcome_is_rejected() {
        let state = AppState::memory_only(Config::from_env());
        let err = state
            .record_outcome(OutcomeRequest {
                routing_id: None,
                payment_id: "pout_x".into(),
                processor: "adyen".into(),
                success: false,
                latency_ms: None,
                failure_class: Some("TIMEOUT".into()),
                failure_detail: None,
            })
            .await
            .unwrap_err();
        assert!(err.contains("unknown processor"));
    }
}
