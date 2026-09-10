use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Mutex;

use tracing_subscriber::EnvFilter;

#[derive(Default)]
pub struct PromMetrics {
    requests: AtomicU64,
    failures: AtomicU64,
    latency_ms_sum: AtomicU64,
    fallbacks: AtomicU64,
    outcomes: AtomicU64,
    circuit_opens: AtomicU64,
    selections: Mutex<HashMap<String, u64>>,
}

impl PromMetrics {
    pub fn observe_route(&self, processor: &str, latency_ms: u64, ok: bool, fallback_count: usize) {
        self.requests.fetch_add(1, Ordering::Relaxed);
        self.latency_ms_sum.fetch_add(latency_ms, Ordering::Relaxed);
        if !ok {
            self.failures.fetch_add(1, Ordering::Relaxed);
        }
        self.fallbacks
            .fetch_add(fallback_count as u64, Ordering::Relaxed);
        if let Ok(mut map) = self.selections.lock() {
            *map.entry(processor.to_string()).or_insert(0) += 1;
        }
    }

    pub fn observe_outcome(&self, circuit_opened: bool) {
        self.outcomes.fetch_add(1, Ordering::Relaxed);
        if circuit_opened {
            self.circuit_opens.fetch_add(1, Ordering::Relaxed);
        }
    }

    pub fn render(&self) -> String {
        let requests = self.requests.load(Ordering::Relaxed);
        let failures = self.failures.load(Ordering::Relaxed);
        let latency = self.latency_ms_sum.load(Ordering::Relaxed);
        let fallbacks = self.fallbacks.load(Ordering::Relaxed);
        let outcomes = self.outcomes.load(Ordering::Relaxed);
        let circuit_opens = self.circuit_opens.load(Ordering::Relaxed);
        let mut out = String::new();
        out.push_str("# HELP routing_requests_total Routing decisions attempted\n");
        out.push_str("# TYPE routing_requests_total counter\n");
        out.push_str(&format!("routing_requests_total {requests}\n"));
        out.push_str("# HELP routing_failures_total Routing decisions that returned an error\n");
        out.push_str("# TYPE routing_failures_total counter\n");
        out.push_str(&format!("routing_failures_total {failures}\n"));
        out.push_str("# HELP routing_latency_ms_sum Decision latency in milliseconds\n");
        out.push_str("# TYPE routing_latency_ms_sum counter\n");
        out.push_str(&format!("routing_latency_ms_sum {latency}\n"));
        out.push_str("# HELP fallback_usage_total Fallback hops attached to decisions\n");
        out.push_str("# TYPE fallback_usage_total counter\n");
        out.push_str(&format!("fallback_usage_total {fallbacks}\n"));
        out.push_str("# HELP routing_outcomes_total Execution outcomes ingested\n");
        out.push_str("# TYPE routing_outcomes_total counter\n");
        out.push_str(&format!("routing_outcomes_total {outcomes}\n"));
        out.push_str("# HELP processor_circuit_opens_total Circuit trips\n");
        out.push_str("# TYPE processor_circuit_opens_total counter\n");
        out.push_str(&format!("processor_circuit_opens_total {circuit_opens}\n"));
        out.push_str("# HELP processor_selection_total Selected processor counts\n");
        out.push_str("# TYPE processor_selection_total counter\n");
        if let Ok(map) = self.selections.lock() {
            for (psp, n) in map.iter() {
                out.push_str(&format!("processor_selection_total{{processor=\"{psp}\"}} {n}\n"));
            }
        }
        out
    }
}

pub fn init_tracing(otel_endpoint: Option<&str>) {
    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info"));
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .json()
        .with_current_span(true)
        .with_span_list(false)
        .init();
    if let Some(ep) = otel_endpoint {
        tracing::info!(endpoint = ep, "OTEL_EXPORTER_OTLP_ENDPOINT set; exporting via JSON traces until a collector sidecar is attached");
    }
}
