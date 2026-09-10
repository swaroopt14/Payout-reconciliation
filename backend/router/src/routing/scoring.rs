use std::collections::hash_map::DefaultHasher;
use std::hash::{Hash, Hasher};

use crate::catalog::Processor;
use crate::types::{Candidate, Direction, Psp, ScoreBreakdown};

pub const W_AUTH: f64 = 0.45;
pub const W_HEALTH: f64 = 0.25;
pub const W_COST: f64 = 0.15;
pub const W_LATENCY: f64 = 0.10;
pub const W_PRIORITY: f64 = 0.05;
pub const W_ML: f64 = 0.00; // reserved. ML must not invent rupees; it may later feed success probability.

pub fn score(processor: &Processor, prefer: Option<Psp>, ml_score: Option<f64>) -> ScoreBreakdown {
    let mut final_score = processor.authorization_rate * W_AUTH
        + processor.health_score * W_HEALTH
        + processor.cost_score() * W_COST
        + processor.latency_score() * W_LATENCY
        + processor.priority_score() * W_PRIORITY;
    if let Some(ml) = ml_score {
        final_score += ml.clamp(0.0, 1.0) * W_ML;
    }
    if prefer == Some(processor.id) {
        final_score += 0.04;
    }
    ScoreBreakdown {
        authorization_rate: processor.authorization_rate,
        health_score: processor.health_score,
        cost_score: processor.cost_score(),
        latency_score: processor.latency_score(),
        priority_score: processor.priority_score(),
        ml_score,
        final_score: final_score.clamp(0.0, 1.0),
    }
}

pub fn rank(
    processors: &[Processor],
    prefer: Option<Psp>,
    direction: Direction,
    ml_score: Option<f64>,
) -> Vec<Candidate> {
    let mut scored: Vec<(Processor, ScoreBreakdown)> = processors
        .iter()
        .cloned()
        .map(|p| {
            let b = score(&p, prefer, ml_score);
            (p, b)
        })
        .collect();
    scored.sort_by(|a, b| {
        b.1.final_score
            .partial_cmp(&a.1.final_score)
            .unwrap_or(std::cmp::Ordering::Equal)
            .then_with(|| a.0.id.as_str().cmp(b.0.id.as_str()))
    });
    scored
        .into_iter()
        .map(|(p, breakdown)| Candidate {
            connector_id: p.id.connector_id(direction).to_string(),
            psp: p.id,
            score: breakdown.final_score,
            breakdown,
        })
        .collect()
}

/// Deterministic weighted pick. Same payment always lands on the same PSP.
pub fn pick_weighted(splits: &[(Psp, u8)], eligible: &[Psp], seed: &str) -> Option<Psp> {
    let live: Vec<(Psp, u8)> = splits
        .iter()
        .copied()
        .filter(|(p, w)| *w > 0 && eligible.contains(p))
        .collect();
    if live.is_empty() {
        return None;
    }
    let total: u32 = live.iter().map(|(_, w)| u32::from(*w)).sum();
    let bucket = (stable_hash(seed) % u64::from(total.max(1))) as u32;
    let mut acc = 0u32;
    for (psp, w) in &live {
        acc += u32::from(*w);
        if bucket < acc {
            return Some(*psp);
        }
    }
    Some(live[0].0)
}

fn stable_hash(seed: &str) -> u64 {
    let mut h = DefaultHasher::new();
    seed.hash(&mut h);
    h.finish()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::processors;

    #[test]
    fn ml_weight_is_zero_so_score_does_not_move() {
        let rzp = processors().into_iter().find(|p| p.id == Psp::Razorpay).unwrap();
        let without = score(&rzp, None, None);
        let with = score(&rzp, None, Some(1.0));
        assert_eq!(W_ML, 0.0);
        assert_eq!(without.final_score, with.final_score);
        assert_eq!(with.ml_score, Some(1.0));
    }

    #[test]
    fn prefer_bump_is_small_and_deterministic() {
        let rzp = processors().into_iter().find(|p| p.id == Psp::Razorpay).unwrap();
        let plain = score(&rzp, None, None).final_score;
        let preferred = score(&rzp, Some(Psp::Razorpay), None).final_score;
        assert!((preferred - plain - 0.04).abs() < 1e-9);
    }
}
