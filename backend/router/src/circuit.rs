use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

use crate::types::Psp;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Circuit {
    Closed,
    Open,
    HalfOpen,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CircuitState {
    pub processor: Psp,
    pub state: Circuit,
    pub consecutive_infra_failures: u32,
    pub samples: u32,
    pub infra_failures: u32,
    pub opened_at_unix: Option<u64>,
}

impl CircuitState {
    pub fn closed(processor: Psp) -> Self {
        Self {
            processor,
            state: Circuit::Closed,
            consecutive_infra_failures: 0,
            samples: 0,
            infra_failures: 0,
            opened_at_unix: None,
        }
    }

    pub fn error_rate(&self) -> f64 {
        if self.samples == 0 {
            return 0.0;
        }
        f64::from(self.infra_failures) / f64::from(self.samples)
    }
}

#[derive(Debug, Clone)]
pub struct CircuitPolicy {
    pub failure_threshold: u32,
    pub error_rate: f64,
    pub reset: Duration,
}

impl Default for CircuitPolicy {
    fn default() -> Self {
        Self {
            failure_threshold: 5,
            error_rate: 0.10,
            reset: Duration::from_secs(30),
        }
    }
}

pub fn now_unix() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

pub fn is_open(state: &CircuitState, policy: &CircuitPolicy, now: u64) -> bool {
    match state.state {
        Circuit::Closed => false,
        Circuit::HalfOpen => false,
        Circuit::Open => {
            if let Some(opened) = state.opened_at_unix {
                if now.saturating_sub(opened) >= policy.reset.as_secs() {
                    return false; // probe window — treat as half-open at call site
                }
            }
            true
        }
    }
}

pub fn record(
    mut state: CircuitState,
    infra_failure: bool,
    success: bool,
    policy: &CircuitPolicy,
    now: u64,
) -> CircuitState {
    if state.state == Circuit::Open {
        if let Some(opened) = state.opened_at_unix {
            if now.saturating_sub(opened) >= policy.reset.as_secs() {
                state.state = Circuit::HalfOpen;
            }
        }
    }

    state.samples = state.samples.saturating_add(1);
    if infra_failure {
        state.infra_failures = state.infra_failures.saturating_add(1);
        state.consecutive_infra_failures = state.consecutive_infra_failures.saturating_add(1);
    } else if success {
        state.consecutive_infra_failures = 0;
    }

    let trip = infra_failure
        && (state.consecutive_infra_failures >= policy.failure_threshold
            || (state.samples >= policy.failure_threshold && state.error_rate() > policy.error_rate));

    match state.state {
        Circuit::HalfOpen => {
            if success {
                state.state = Circuit::Closed;
                state.opened_at_unix = None;
                state.consecutive_infra_failures = 0;
            } else if infra_failure {
                state.state = Circuit::Open;
                state.opened_at_unix = Some(now);
            }
        }
        Circuit::Closed if trip => {
            state.state = Circuit::Open;
            state.opened_at_unix = Some(now);
        }
        Circuit::Open if success => {
            // Direct success while theoretically open shouldn't happen; close.
            state.state = Circuit::Closed;
            state.opened_at_unix = None;
        }
        _ => {}
    }
    state
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn five_timeouts_open_circuit() {
        let policy = CircuitPolicy::default();
        let mut s = CircuitState::closed(Psp::Razorpay);
        for _ in 0..4 {
            s = record(s, true, false, &policy, 1);
            assert_eq!(s.state, Circuit::Closed);
        }
        s = record(s, true, false, &policy, 1);
        assert_eq!(s.state, Circuit::Open);
        assert!(is_open(&s, &policy, 1));
    }

    #[test]
    fn cooldown_allows_half_open_probe() {
        let policy = CircuitPolicy::default();
        let mut s = CircuitState::closed(Psp::Cashfree);
        for _ in 0..5 {
            s = record(s, true, false, &policy, 10);
        }
        assert_eq!(s.state, Circuit::Open);
        assert!(!is_open(&s, &policy, 10 + 30));
        s = record(s, false, true, &policy, 10 + 30);
        assert_eq!(s.state, Circuit::Closed);
    }

    #[test]
    fn hard_declines_do_not_open_circuit() {
        let policy = CircuitPolicy::default();
        let mut s = CircuitState::closed(Psp::Razorpay);
        for _ in 0..8 {
            s = record(s, false, false, &policy, 1);
        }
        assert_eq!(s.state, Circuit::Closed);
        assert!(!is_open(&s, &policy, 1));
    }

    #[test]
    fn half_open_infra_failure_reopens() {
        let policy = CircuitPolicy::default();
        let mut s = CircuitState::closed(Psp::Payu);
        for _ in 0..5 {
            s = record(s, true, false, &policy, 10);
        }
        s = record(s, true, false, &policy, 10 + 30);
        assert_eq!(s.state, Circuit::Open);
    }
}
