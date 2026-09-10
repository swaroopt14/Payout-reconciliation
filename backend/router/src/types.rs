use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum Direction {
    Inbound,
    Outbound,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum Rail {
    Upi,
    Card,
    Netbanking,
    Wallet,
    Imps,
    Neft,
    Rtgs,
    BankTransfer,
    Emi,
    Paylater,
}

impl Rail {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Upi => "UPI",
            Self::Card => "CARD",
            Self::Netbanking => "NETBANKING",
            Self::Wallet => "WALLET",
            Self::Imps => "IMPS",
            Self::Neft => "NEFT",
            Self::Rtgs => "RTGS",
            Self::BankTransfer => "BANK_TRANSFER",
            Self::Emi => "EMI",
            Self::Paylater => "PAYLATER",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Psp {
    Razorpay,
    Cashfree,
    Payu,
    Stripe,
}

impl Psp {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Razorpay => "razorpay",
            Self::Cashfree => "cashfree",
            Self::Payu => "payu",
            Self::Stripe => "stripe",
        }
    }

    pub fn parse(raw: &str) -> Option<Self> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "razorpay" => Some(Self::Razorpay),
            "cashfree" => Some(Self::Cashfree),
            "payu" => Some(Self::Payu),
            "stripe" => Some(Self::Stripe),
            _ => None,
        }
    }

    pub fn connector_id(self, direction: Direction) -> &'static str {
        match (self, direction) {
            (Self::Razorpay, Direction::Outbound) => "razorpayx-v1",
            (Self::Razorpay, Direction::Inbound) => "razorpay-v1",
            (Self::Cashfree, Direction::Outbound) => "cashfree-payout-v1",
            (Self::Cashfree, Direction::Inbound) => "cashfree-v1",
            (Self::Payu, _) => "payu-v1",
            (Self::Stripe, _) => "stripe-v1",
        }
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct RouteRequest {
    pub tenant_id: Option<String>,
    pub merchant_id: Option<String>,
    pub entity_id: Option<String>,
    pub payment_id: Option<String>,
    pub direction: Option<Direction>,
    pub rail: Option<Rail>,
    pub payment_method: Option<String>,
    pub card_network: Option<String>,
    pub amount_minor: Option<i64>,
    pub amount: Option<i64>,
    #[serde(default = "default_inr")]
    pub currency: String,
    pub country: Option<String>,
    /// Optional success probability from ML. Weight is 0 until a routing model exists.
    pub ml_score: Option<f64>,
}

fn default_inr() -> String {
    "INR".to_string()
}

#[derive(Debug, Clone)]
pub struct NormalizedRequest {
    pub tenant_id: Option<String>,
    pub merchant_id: Option<String>,
    pub entity_id: String,
    pub direction: Direction,
    pub rail: Rail,
    pub amount_minor: i64,
    pub currency: String,
    pub country: Option<String>,
    pub card_network: Option<String>,
    pub ml_score: Option<f64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ScoreBreakdown {
    pub authorization_rate: f64,
    pub health_score: f64,
    pub cost_score: f64,
    pub latency_score: f64,
    pub priority_score: f64,
    pub ml_score: Option<f64>,
    pub final_score: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Candidate {
    pub psp: Psp,
    pub connector_id: String,
    pub score: f64,
    pub breakdown: ScoreBreakdown,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FallbackHop {
    pub psp: Psp,
    pub connector_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Eliminated {
    pub psp: Psp,
    pub reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RouteDecision {
    pub routing_id: String,
    pub payment_id: String,
    pub selected_processor: Psp,
    pub psp: Psp,
    pub connector_id: String,
    pub rail: Rail,
    pub direction: Direction,
    pub routing_strategy: String,
    pub algorithm: String,
    pub score: f64,
    pub reason: ScoreBreakdown,
    pub candidates: Vec<Candidate>,
    pub fallbacks: Vec<FallbackHop>,
    pub fallback_processors: Vec<Psp>,
    pub eliminated: Vec<Eliminated>,
    pub rules_applied: Vec<String>,
    pub metrics_source: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub open_circuits: Vec<Psp>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub rail_rewritten_from: Option<Rail>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConnectorInfo {
    pub psp: Psp,
    pub name: String,
    pub enabled: bool,
    pub collect_rails: Vec<Rail>,
    pub payout_rails: Vec<Rail>,
    pub currencies: Vec<String>,
    pub priority: i32,
    pub authorization_rate: f64,
    pub health_score: f64,
    pub error_rate: f64,
    pub circuit_state: String,
    pub metrics_source: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ErrorBody {
    pub error: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RoutingRule {
    pub id: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub merchant_id: Option<String>,
    pub name: String,
    pub priority: i32,
    pub enabled: bool,
    pub condition: serde_json::Value,
    pub action: serde_json::Value,
}

#[derive(Debug, Clone, Deserialize)]
pub struct OutcomeRequest {
    pub routing_id: Option<String>,
    pub payment_id: String,
    pub processor: String,
    pub success: bool,
    pub latency_ms: Option<f64>,
    pub failure_class: Option<String>,
    pub failure_detail: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct OutcomeResponse {
    pub payment_id: String,
    pub processor: Psp,
    pub triage: crate::triage::Triage,
    pub circuit_state: String,
    pub metrics_source: String,
}
