//! Rule-based PSP failure triage for routing.
//!
//! This is NOT recon RCA (`backend/ml` HDBSCAN) and must not invent rupees.
//! Relay reports an outcome; we decide whether the processor is sick (circuit),
//! whether the same PSP should be retried, and whether fallback is allowed.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum FailureClass {
    Success,
    Timeout,
    Network,
    RailDown,
    HardDecline,
    AuthFailed,
    InsufficientFunds,
    RateLimited,
    Unknown,
}

impl FailureClass {
    pub fn parse(raw: Option<&str>, success: bool) -> Self {
        if success {
            return Self::Success;
        }
        match raw.unwrap_or("").trim().to_ascii_uppercase().as_str() {
            "TIMEOUT" => Self::Timeout,
            "NETWORK" | "NETWORK_DROP" => Self::Network,
            "RAIL_DOWN" | "PROCESSOR_DOWN" => Self::RailDown,
            "HARD_DECLINE" | "DECLINED" => Self::HardDecline,
            "AUTH_FAILED" | "AUTHORIZATION_FAILED" => Self::AuthFailed,
            "INSUFFICIENT_FUNDS" => Self::InsufficientFunds,
            "RATE_LIMITED" | "429" => Self::RateLimited,
            _ => Self::Unknown,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Triage {
    pub failure_class: FailureClass,
    /// Processor infrastructure looks sick → count toward circuit breaker.
    pub counts_toward_circuit: bool,
    /// Safe to try the same PSP again immediately.
    pub retry_same_processor: bool,
    /// Routing should prefer the next fallback hop.
    pub use_fallback: bool,
    /// Merchant/beneficiary issue, not a processor outage.
    pub merchant_action: bool,
}

pub fn triage(success: bool, failure_class: Option<&str>) -> Triage {
    let class = FailureClass::parse(failure_class, success);
    match class {
        FailureClass::Success => Triage {
            failure_class: class,
            counts_toward_circuit: false,
            retry_same_processor: false,
            use_fallback: false,
            merchant_action: false,
        },
        FailureClass::Timeout | FailureClass::Network | FailureClass::RailDown => Triage {
            failure_class: class,
            counts_toward_circuit: true,
            retry_same_processor: false,
            use_fallback: true,
            merchant_action: false,
        },
        FailureClass::RateLimited => Triage {
            failure_class: class,
            counts_toward_circuit: true,
            retry_same_processor: false,
            use_fallback: true,
            merchant_action: false,
        },
        FailureClass::HardDecline | FailureClass::AuthFailed => Triage {
            failure_class: class,
            counts_toward_circuit: false,
            retry_same_processor: false,
            use_fallback: true,
            merchant_action: false,
        },
        FailureClass::InsufficientFunds => Triage {
            failure_class: class,
            counts_toward_circuit: false,
            retry_same_processor: false,
            use_fallback: false,
            merchant_action: true,
        },
        FailureClass::Unknown => Triage {
            failure_class: class,
            counts_toward_circuit: true,
            retry_same_processor: false,
            use_fallback: true,
            merchant_action: false,
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn timeout_opens_circuit_path_and_fallback() {
        let t = triage(false, Some("TIMEOUT"));
        assert!(t.counts_toward_circuit);
        assert!(t.use_fallback);
        assert!(!t.retry_same_processor);
    }

    #[test]
    fn insufficient_funds_is_merchant_not_processor() {
        let t = triage(false, Some("INSUFFICIENT_FUNDS"));
        assert!(!t.counts_toward_circuit);
        assert!(t.merchant_action);
        assert!(!t.use_fallback);
    }

    #[test]
    fn hard_decline_falls_back_without_tripping_circuit() {
        let t = triage(false, Some("HARD_DECLINE"));
        assert!(!t.counts_toward_circuit);
        assert!(t.use_fallback);
    }

    #[test]
    fn success_is_quiet() {
        let t = triage(true, Some("TIMEOUT"));
        assert_eq!(t.failure_class, FailureClass::Success);
        assert!(!t.counts_toward_circuit);
        assert!(!t.use_fallback);
    }

    #[test]
    fn network_and_rail_down_trip_circuit() {
        for class in ["NETWORK", "NETWORK_DROP", "RAIL_DOWN", "PROCESSOR_DOWN", "RATE_LIMITED", "429"] {
            let t = triage(false, Some(class));
            assert!(t.counts_toward_circuit, "{class}");
            assert!(t.use_fallback, "{class}");
        }
    }

    #[test]
    fn auth_failed_is_hard_decline_family() {
        let t = triage(false, Some("AUTHORIZATION_FAILED"));
        assert_eq!(t.failure_class, FailureClass::AuthFailed);
        assert!(!t.counts_toward_circuit);
        assert!(t.use_fallback);
        assert!(!t.merchant_action);
    }

    #[test]
    fn unknown_failure_is_treated_as_infra() {
        let t = triage(false, Some("wat"));
        assert_eq!(t.failure_class, FailureClass::Unknown);
        assert!(t.counts_toward_circuit);
        assert!(t.use_fallback);
    }
}
