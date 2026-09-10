use crate::catalog::{processors, Processor};
use crate::types::{
    Candidate, ConnectorInfo, NormalizedRequest, Psp, RouteDecision, RouteRequest, RoutingRule,
};

use super::eligibility;
use super::fallback;
use super::normalize::{normalize, rewrite_rail};
use super::rules::{matching_actions, seed_rules, RuleAction};
use super::scoring;

#[derive(Debug, Clone)]
pub struct RouteInputs {
    pub processors: Vec<Processor>,
    pub rules: Vec<RoutingRule>,
    pub open_circuits: Vec<Psp>,
    pub metrics_source: String,
}

impl Default for RouteInputs {
    fn default() -> Self {
        Self::static_seed()
    }
}

impl RouteInputs {
    pub fn static_seed() -> Self {
        Self {
            processors: processors(),
            rules: seed_rules(),
            open_circuits: Vec::new(),
            metrics_source: "static_config".into(),
        }
    }
}

pub fn route(req: RouteRequest) -> Result<RouteDecision, String> {
    route_with(req, RouteInputs::static_seed())
}

#[tracing::instrument(skip_all, fields(payment_id, metrics_source = %inputs.metrics_source))]
pub fn route_with(req: RouteRequest, inputs: RouteInputs) -> Result<RouteDecision, String> {
    let mut req = normalize(req)?;
    tracing::Span::current().record("payment_id", req.entity_id.as_str());
    let rewritten = rewrite_rail(&mut req);

    let pool = inputs.processors;
    let rules = inputs.rules;
    let (actions, rules_applied) = matching_actions(&req, &rules);

    let mut excluded: Vec<Psp> = inputs.open_circuits.clone();
    let mut prefer: Option<Psp> = None;
    let mut volume: Option<Vec<(Psp, u8)>> = None;
    for action in actions {
        match action {
            RuleAction::Exclude(p) => excluded.push(p),
            RuleAction::Prefer(p) => {
                if prefer.is_none() {
                    prefer = Some(p);
                }
            }
            RuleAction::VolumeSplit(splits) => {
                if volume.is_none() {
                    volume = Some(splits);
                }
            }
        }
    }

    let (eligible, mut eliminated) = eligibility::filter(&req, &pool, &excluded);
    for psp in &inputs.open_circuits {
        if !eliminated.iter().any(|e| e.psp == *psp) {
            eliminated.push(crate::types::Eliminated {
                psp: *psp,
                reason: "circuit_open".into(),
            });
        } else if let Some(row) = eliminated.iter_mut().find(|e| e.psp == *psp) {
            if row.reason == "rule_excluded" {
                row.reason = "circuit_open".into();
            }
        }
    }
    if eligible.is_empty() {
        return Err(format!(
            "no eligible PSP for {:?} {} {}",
            req.direction,
            req.rail.as_str(),
            req.currency
        ));
    }

    let eligible_ids: Vec<Psp> = eligible.iter().map(|p| p.id).collect();
    let mut ranked = scoring::rank(&eligible, prefer, req.direction, req.ml_score);
    let mut strategy = "weighted";

    if let Some(splits) = volume {
        if let Some(chosen) = scoring::pick_weighted(&splits, &eligible_ids, &req.entity_id) {
            strategy = "volume_split";
            if let Some(idx) = ranked.iter().position(|c| c.psp == chosen) {
                let winner = ranked.remove(idx);
                ranked.insert(0, winner);
            }
        }
    }

    let primary = ranked
        .first()
        .cloned()
        .ok_or_else(|| "no eligible PSP".to_string())?;
    fix_connector_ids(&mut ranked, &req);

    let fallbacks = fallback::chain(&ranked);
    let fallback_processors: Vec<Psp> = fallbacks.iter().map(|f| f.psp).collect();
    let routing_id = format!("rte_{}", req.entity_id);

    Ok(RouteDecision {
        routing_id,
        payment_id: req.entity_id.clone(),
        selected_processor: primary.psp,
        psp: primary.psp,
        connector_id: primary.psp.connector_id(req.direction).to_string(),
        rail: req.rail,
        direction: req.direction,
        routing_strategy: strategy.to_string(),
        algorithm: strategy.to_string(),
        score: primary.score,
        reason: primary.breakdown.clone(),
        candidates: ranked,
        fallbacks,
        fallback_processors,
        eliminated,
        rules_applied,
        metrics_source: inputs.metrics_source,
        open_circuits: inputs.open_circuits,
        rail_rewritten_from: rewritten,
    })
}

fn fix_connector_ids(ranked: &mut [Candidate], req: &NormalizedRequest) {
    for c in ranked.iter_mut() {
        c.connector_id = c.psp.connector_id(req.direction).to_string();
    }
}

pub fn connector_catalog() -> Vec<ConnectorInfo> {
    processors().into_iter().map(|p| to_info(&p, "closed")).collect()
}

pub fn to_info(p: &Processor, circuit_state: &str) -> ConnectorInfo {
    ConnectorInfo {
        psp: p.id,
        name: p.name.clone(),
        enabled: p.enabled,
        collect_rails: p.collect_rails.clone(),
        payout_rails: p.payout_rails.clone(),
        currencies: p.supported_currencies.clone(),
        priority: p.priority,
        authorization_rate: p.authorization_rate,
        health_score: p.health_score,
        error_rate: p.error_rate,
        circuit_state: circuit_state.to_string(),
        metrics_source: p.metrics_source.clone(),
    }
}

pub fn list_rules() -> Vec<RoutingRule> {
    seed_rules()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::{Direction, Rail};

    fn req(direction: Direction, rail: Rail, amount: i64, currency: &str, id: &str) -> RouteRequest {
        RouteRequest {
            tenant_id: None,
            merchant_id: None,
            entity_id: Some(id.into()),
            payment_id: None,
            direction: Some(direction),
            rail: Some(rail),
            payment_method: None,
            card_network: None,
            amount_minor: Some(amount),
            amount: None,
            currency: currency.into(),
            country: None,
            ml_score: None,
        }
    }

    #[test]
    fn inr_payout_imps_picks_razorpay_first() {
        let d = route(req(Direction::Outbound, Rail::Imps, 10_000, "INR", "pout_1")).unwrap();
        assert_eq!(d.psp, Psp::Razorpay);
        assert_eq!(d.connector_id, "razorpayx-v1");
        assert_eq!(d.rail, Rail::Imps);
        assert!(d.fallbacks.iter().any(|f| f.psp == Psp::Cashfree));
        assert!(!d.fallbacks.iter().any(|f| f.psp == Psp::Payu));
        assert!(d.score > 0.8);
        assert!(d.reason.authorization_rate > 0.9);
        assert_eq!(d.metrics_source, "static_config");
    }

    #[test]
    fn large_imps_rewrites_to_neft() {
        let d = route(req(Direction::Outbound, Rail::Imps, 25_000_000, "INR", "pout_big")).unwrap();
        assert_eq!(d.rail, Rail::Neft);
        assert_eq!(d.rail_rewritten_from, Some(Rail::Imps));
        assert_eq!(d.psp, Psp::Razorpay);
    }

    #[test]
    fn five_lakh_imps_rewrites_to_rtgs() {
        let d = route(req(Direction::Outbound, Rail::Imps, 60_000_000, "INR", "pout_rtgs")).unwrap();
        assert_eq!(d.rail, Rail::Rtgs);
    }

    #[test]
    fn usd_card_goes_to_stripe() {
        let d = route(req(Direction::Inbound, Rail::Card, 1000, "USD", "pay_usd")).unwrap();
        assert_eq!(d.psp, Psp::Stripe);
        assert_eq!(d.connector_id, "stripe-v1");
        assert!(d.eliminated.iter().any(|e| e.psp == Psp::Razorpay));
        assert!(d.rules_applied.iter().any(|id| id.contains("stripe")));
    }

    #[test]
    fn inr_upi_collect_is_volume_split_and_stable() {
        let a = route(req(Direction::Inbound, Rail::Upi, 5000, "INR", "pay_stable")).unwrap();
        let b = route(req(Direction::Inbound, Rail::Upi, 5000, "INR", "pay_stable")).unwrap();
        assert_eq!(a.routing_strategy, "volume_split");
        assert_eq!(a.psp, b.psp);
        assert!(a.psp == Psp::Razorpay || a.psp == Psp::Cashfree);
    }

    #[test]
    fn stripe_cannot_payout_inr() {
        let err = route(req(Direction::Outbound, Rail::Card, 1000, "INR", "x")).unwrap_err();
        assert!(err.contains("no eligible PSP"), "{err}");
    }

    #[test]
    fn payment_method_alias_works() {
        let d = route(RouteRequest {
            tenant_id: None,
            merchant_id: Some("merchant_001".into()),
            entity_id: None,
            payment_id: Some("pay_123".into()),
            direction: None,
            rail: None,
            payment_method: Some("card".into()),
            card_network: Some("VISA".into()),
            amount_minor: None,
            amount: Some(5000),
            currency: "INR".into(),
            country: Some("IN".into()),
            ml_score: None,
        })
        .unwrap();
        assert_eq!(d.payment_id, "pay_123");
        assert_eq!(d.rail, Rail::Card);
        assert_eq!(d.psp, Psp::Razorpay);
    }

    #[test]
    fn open_circuit_skips_razorpay() {
        let mut inputs = RouteInputs::static_seed();
        inputs.open_circuits = vec![Psp::Razorpay];
        inputs.metrics_source = "redis".into();
        let d = route_with(req(Direction::Outbound, Rail::Imps, 10_000, "INR", "pout_cb"), inputs).unwrap();
        assert_eq!(d.psp, Psp::Cashfree);
        assert!(d.eliminated.iter().any(|e| e.psp == Psp::Razorpay && e.reason == "circuit_open"));
        assert_eq!(d.metrics_source, "redis");
    }
}
