use std::collections::HashMap;

use serde::{Deserialize, Serialize};

use crate::catalog::Processor;
use crate::circuit::{Circuit, CircuitState};
use crate::types::Psp;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LiveMetrics {
    pub authorization_rate: f64,
    pub health_score: f64,
    pub average_latency_ms: f64,
    pub error_rate: f64,
    pub samples: u64,
}

impl LiveMetrics {
    pub fn from_processor(p: &Processor) -> Self {
        Self {
            authorization_rate: p.authorization_rate,
            health_score: p.health_score,
            average_latency_ms: p.average_latency_ms,
            error_rate: 0.0,
            samples: 0,
        }
    }

    pub fn apply_outcome(&mut self, success: bool, infra_failure: bool, latency_ms: Option<f64>) {
        let alpha = 0.15;
        let auth = if success { 1.0 } else { 0.0 };
        let err = if infra_failure { 1.0 } else { 0.0 };
        self.authorization_rate = (1.0 - alpha) * self.authorization_rate + alpha * auth;
        self.error_rate = (1.0 - alpha) * self.error_rate + alpha * err;
        if let Some(lat) = latency_ms {
            self.average_latency_ms = (1.0 - alpha) * self.average_latency_ms + alpha * lat;
        }
        self.health_score = ((1.0 - self.error_rate) * 0.6 + self.authorization_rate * 0.4).clamp(0.0, 1.0);
        self.samples = self.samples.saturating_add(1);
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct LiveSnapshot {
    pub metrics: HashMap<Psp, LiveMetrics>,
    pub circuits: HashMap<Psp, CircuitState>,
    pub source: String,
}

impl LiveSnapshot {
    pub fn static_seed(processors: &[Processor]) -> Self {
        let mut metrics = HashMap::new();
        let mut circuits = HashMap::new();
        for p in processors {
            metrics.insert(p.id, LiveMetrics::from_processor(p));
            circuits.insert(p.id, CircuitState::closed(p.id));
        }
        Self {
            metrics,
            circuits,
            source: "static_config".into(),
        }
    }

    pub fn overlay(&self, mut processors: Vec<Processor>) -> Vec<Processor> {
        for p in processors.iter_mut() {
            if let Some(m) = self.metrics.get(&p.id) {
                p.authorization_rate = m.authorization_rate;
                p.health_score = m.health_score;
                p.average_latency_ms = m.average_latency_ms;
                p.error_rate = m.error_rate;
                p.metrics_source = self.source.clone();
            }
        }
        processors
    }

    pub fn open_circuits(&self, now: u64, policy: &crate::circuit::CircuitPolicy) -> Vec<Psp> {
        self.circuits
            .values()
            .filter(|c| crate::circuit::is_open(c, policy, now))
            .map(|c| c.processor)
            .collect()
    }

    pub fn circuit_label(&self, psp: Psp) -> String {
        self.circuits
            .get(&psp)
            .map(|c| match c.state {
                Circuit::Closed => "closed",
                Circuit::Open => "open",
                Circuit::HalfOpen => "half_open",
            })
            .unwrap_or("closed")
            .to_string()
    }
}
