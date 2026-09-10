use serde::Serialize;

use crate::types::{Direction, Psp, Rail};

#[derive(Debug, Clone, Serialize)]
pub struct Processor {
    pub id: Psp,
    pub name: String,
    pub enabled: bool,
    pub supported_currencies: Vec<String>,
    pub collect_rails: Vec<Rail>,
    pub payout_rails: Vec<Rail>,
    pub priority: i32,
    /// Lower is cheaper. Used to derive cost_score = 1 - min(fee, 1).
    pub cost_percentage: f64,
    pub authorization_rate: f64,
    pub average_latency_ms: f64,
    pub health_score: f64,
    pub error_rate: f64,
    /// `static_config` until Redis/cache overlays live scores.
    pub metrics_source: String,
}

impl Processor {
    pub fn supports(&self, direction: Direction, rail: Rail, currency: &str) -> Result<(), &'static str> {
        if !self.enabled {
            return Err("processor_disabled");
        }
        let cur = currency.trim().to_uppercase();
        if !self
            .supported_currencies
            .iter()
            .any(|c| c.eq_ignore_ascii_case(&cur))
        {
            return Err("currency_not_supported");
        }
        let rails = match direction {
            Direction::Inbound => &self.collect_rails,
            Direction::Outbound => &self.payout_rails,
        };
        if rails.is_empty() {
            return Err("direction_not_supported");
        }
        if !rails.contains(&rail) {
            return Err("rail_not_supported");
        }
        Ok(())
    }

    pub fn cost_score(&self) -> f64 {
        (1.0 - self.cost_percentage).clamp(0.0, 1.0)
    }

    pub fn latency_score(&self) -> f64 {
        (1.0 - self.average_latency_ms / 1000.0).clamp(0.0, 1.0)
    }

    pub fn priority_score(&self) -> f64 {
        (f64::from(self.priority) / 100.0).clamp(0.0, 1.0)
    }
}

pub fn processors() -> Vec<Processor> {
    vec![
        Processor {
            id: Psp::Razorpay,
            name: "Razorpay".into(),
            enabled: true,
            supported_currencies: vec!["INR".into()],
            collect_rails: vec![
                Rail::Upi,
                Rail::Card,
                Rail::Netbanking,
                Rail::Wallet,
                Rail::BankTransfer,
                Rail::Emi,
                Rail::Paylater,
            ],
            payout_rails: vec![Rail::Upi, Rail::Imps, Rail::Neft, Rail::Rtgs],
            priority: 100,
            cost_percentage: 0.08,
            authorization_rate: 0.96,
            average_latency_ms: 180.0,
            health_score: 0.98,
            error_rate: 0.0,
            metrics_source: "static_config".into(),
        },
        Processor {
            id: Psp::Cashfree,
            name: "Cashfree".into(),
            enabled: true,
            supported_currencies: vec!["INR".into()],
            collect_rails: vec![Rail::Upi, Rail::Card, Rail::Netbanking, Rail::Wallet],
            payout_rails: vec![Rail::Upi, Rail::Imps, Rail::Neft, Rail::Rtgs],
            priority: 80,
            cost_percentage: 0.10,
            authorization_rate: 0.94,
            average_latency_ms: 210.0,
            health_score: 0.96,
            error_rate: 0.0,
            metrics_source: "static_config".into(),
        },
        Processor {
            id: Psp::Payu,
            name: "PayU".into(),
            enabled: true,
            supported_currencies: vec!["INR".into()],
            collect_rails: vec![Rail::Upi, Rail::Card, Rail::Netbanking, Rail::Wallet],
            payout_rails: vec![],
            priority: 60,
            cost_percentage: 0.12,
            authorization_rate: 0.91,
            average_latency_ms: 260.0,
            health_score: 0.93,
            error_rate: 0.0,
            metrics_source: "static_config".into(),
        },
        Processor {
            id: Psp::Stripe,
            name: "Stripe".into(),
            enabled: true,
            supported_currencies: vec!["USD".into(), "EUR".into(), "GBP".into()],
            collect_rails: vec![Rail::Card],
            payout_rails: vec![],
            priority: 90,
            cost_percentage: 0.15,
            authorization_rate: 0.97,
            average_latency_ms: 160.0,
            health_score: 0.99,
            error_rate: 0.0,
            metrics_source: "static_config".into(),
        },
    ]
}
