use serde_json::Value;

use crate::types::{NormalizedRequest, Psp, RoutingRule};

pub fn seed_rules() -> Vec<RoutingRule> {
    vec![
        RoutingRule {
            id: "rule_usd_card_stripe".into(),
            merchant_id: None,
            name: "Non-INR card prefers Stripe".into(),
            priority: 10,
            enabled: true,
            condition: serde_json::json!({
                "all": [
                    { "field": "rail", "operator": "equals", "value": "CARD" },
                    { "field": "currency", "operator": "not_equals", "value": "INR" }
                ]
            }),
            action: serde_json::json!({ "type": "prefer", "processor": "stripe" }),
        },
        RoutingRule {
            id: "rule_inr_upi_volume".into(),
            merchant_id: None,
            name: "INR UPI collect volume split".into(),
            priority: 20,
            enabled: true,
            condition: serde_json::json!({
                "all": [
                    { "field": "direction", "operator": "equals", "value": "INBOUND" },
                    { "field": "rail", "operator": "equals", "value": "UPI" },
                    { "field": "currency", "operator": "equals", "value": "INR" }
                ]
            }),
            action: serde_json::json!({
                "type": "volume_split",
                "splits": [
                    { "processor": "razorpay", "split": 70 },
                    { "processor": "cashfree", "split": 30 }
                ]
            }),
        },
        RoutingRule {
            id: "rule_inr_prefer_razorpay".into(),
            merchant_id: None,
            name: "INR prefers Razorpay".into(),
            priority: 90,
            enabled: true,
            condition: serde_json::json!({
                "all": [{ "field": "currency", "operator": "equals", "value": "INR" }]
            }),
            action: serde_json::json!({ "type": "prefer", "processor": "razorpay" }),
        },
    ]
}

#[derive(Debug, Clone)]
pub enum RuleAction {
    Prefer(Psp),
    Exclude(Psp),
    VolumeSplit(Vec<(Psp, u8)>),
}

pub fn matching_actions(req: &NormalizedRequest, rules: &[RoutingRule]) -> (Vec<RuleAction>, Vec<String>) {
    let mut actions = Vec::new();
    let mut applied = Vec::new();
    let mut ordered = rules.to_vec();
    ordered.sort_by_key(|r| r.priority);
    for rule in ordered {
        if !rule.enabled {
            continue;
        }
        if !condition_matches(req, &rule.condition) {
            continue;
        }
        if let Some(action) = parse_action(&rule.action) {
            applied.push(rule.id.clone());
            actions.push(action);
        }
    }
    (actions, applied)
}

fn parse_action(action: &Value) -> Option<RuleAction> {
    let kind = action.get("type")?.as_str()?;
    match kind {
        "prefer" => Psp::parse(action.get("processor")?.as_str()?).map(RuleAction::Prefer),
        "exclude" => Psp::parse(action.get("processor")?.as_str()?).map(RuleAction::Exclude),
        "volume_split" => {
            let splits = action.get("splits")?.as_array()?;
            let mut out = Vec::new();
            for s in splits {
                let psp = Psp::parse(s.get("processor")?.as_str()?)?;
                let w = s.get("split")?.as_u64()? as u8;
                out.push((psp, w));
            }
            Some(RuleAction::VolumeSplit(out))
        }
        _ => None,
    }
}

fn condition_matches(req: &NormalizedRequest, condition: &Value) -> bool {
    if let Some(all) = condition.get("all").and_then(Value::as_array) {
        return all.iter().all(|c| clause_matches(req, c));
    }
    if let Some(any) = condition.get("any").and_then(Value::as_array) {
        return any.iter().any(|c| clause_matches(req, c));
    }
    clause_matches(req, condition)
}

fn clause_matches(req: &NormalizedRequest, clause: &Value) -> bool {
    let field = clause.get("field").and_then(Value::as_str).unwrap_or("");
    let op = clause.get("operator").and_then(Value::as_str).unwrap_or("equals");
    let expected = clause.get("value").cloned().unwrap_or(Value::Null);
    let actual = field_value(req, field);
    match op {
        "equals" => json_eq(&actual, &expected),
        "not_equals" => !json_eq(&actual, &expected),
        "gte" => num(&actual) >= num(&expected),
        "gt" => num(&actual) > num(&expected),
        "lte" => num(&actual) <= num(&expected),
        "lt" => num(&actual) < num(&expected),
        _ => false,
    }
}

fn field_value(req: &NormalizedRequest, field: &str) -> Value {
    match field {
        "currency" => Value::String(req.currency.clone()),
        "rail" | "payment_method" => Value::String(req.rail.as_str().to_string()),
        "direction" => Value::String(
            match req.direction {
                crate::types::Direction::Inbound => "INBOUND".into(),
                crate::types::Direction::Outbound => "OUTBOUND".into(),
            },
        ),
        "amount_minor" | "amount" => serde_json::json!(req.amount_minor),
        "country" => Value::String(req.country.clone().unwrap_or_default()),
        _ => Value::Null,
    }
}

fn json_eq(a: &Value, b: &Value) -> bool {
    match (a, b) {
        (Value::String(x), Value::String(y)) => x.eq_ignore_ascii_case(y),
        _ => a == b,
    }
}

fn num(v: &Value) -> f64 {
    v.as_f64()
        .or_else(|| v.as_i64().map(|n| n as f64))
        .unwrap_or(0.0)
}
