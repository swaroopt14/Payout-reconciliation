use crate::types::{Direction, NormalizedRequest, Rail, RouteRequest};

pub fn normalize(req: RouteRequest) -> Result<NormalizedRequest, String> {
    let entity_id = first_nonempty(&[
        req.entity_id.as_deref(),
        req.payment_id.as_deref(),
    ])
    .ok_or_else(|| "entity_id or payment_id is required".to_string())?;

    let rail = req
        .rail
        .or_else(|| parse_rail(req.payment_method.as_deref()))
        .ok_or_else(|| "rail or payment_method is required".to_string())?;

    let amount_minor = req.amount_minor.or(req.amount).unwrap_or(0);
    let currency = if req.currency.trim().is_empty() {
        "INR".to_string()
    } else {
        req.currency.trim().to_uppercase()
    };

    let direction = req.direction.unwrap_or_else(|| infer_direction(rail));

    Ok(NormalizedRequest {
        tenant_id: req.tenant_id,
        merchant_id: req.merchant_id,
        entity_id,
        direction,
        rail,
        amount_minor,
        currency,
        country: req.country,
        card_network: req.card_network,
        ml_score: req.ml_score,
    })
}

fn infer_direction(rail: Rail) -> Direction {
    match rail {
        Rail::Imps | Rail::Neft | Rail::Rtgs => Direction::Outbound,
        _ => Direction::Inbound,
    }
}

pub fn parse_rail(raw: Option<&str>) -> Option<Rail> {
    let s = raw?.trim().to_ascii_uppercase();
    match s.as_str() {
        "UPI" => Some(Rail::Upi),
        "CARD" | "VISA" | "MASTERCARD" | "RUPAY" | "AMEX" => Some(Rail::Card),
        "NETBANKING" | "NB" => Some(Rail::Netbanking),
        "WALLET" => Some(Rail::Wallet),
        "IMPS" => Some(Rail::Imps),
        "NEFT" => Some(Rail::Neft),
        "RTGS" => Some(Rail::Rtgs),
        // CSV `beneficiary.instrument.kind=BANK` is an India bank payout, not a collect rail.
        "BANK" | "BANK_ACCOUNT" | "BANKACCOUNT" => Some(Rail::Imps),
        "BANK_TRANSFER" | "BANKTRANSFER" => Some(Rail::BankTransfer),
        "EMI" | "CARDLESS_EMI" => Some(Rail::Emi),
        "PAYLATER" => Some(Rail::Paylater),
        _ => None,
    }
}

fn first_nonempty(values: &[Option<&str>]) -> Option<String> {
    for v in values {
        if let Some(s) = v {
            let t = s.trim();
            if !t.is_empty() {
                return Some(t.to_string());
            }
        }
    }
    None
}

/// India payout rail policy: IMPS is the small-value default.
/// ₹2L+ prefers NEFT. ₹5L+ leaves IMPS (NPCI cap).
pub fn rewrite_rail(req: &mut NormalizedRequest) -> Option<Rail> {
    if req.direction != Direction::Outbound || req.rail != Rail::Imps {
        return None;
    }
    let from = req.rail;
    if req.amount_minor >= 50_000_000 {
        req.rail = Rail::Rtgs;
        return Some(from);
    }
    if req.amount_minor >= 20_000_000 {
        req.rail = Rail::Neft;
        return Some(from);
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::Direction;

    fn base() -> RouteRequest {
        RouteRequest {
            tenant_id: None,
            merchant_id: None,
            entity_id: Some("pay_1".into()),
            payment_id: None,
            direction: Some(Direction::Outbound),
            rail: Some(Rail::Imps),
            payment_method: None,
            card_network: None,
            amount_minor: Some(10_000),
            amount: None,
            currency: "INR".into(),
            country: Some("IN".into()),
            ml_score: None,
        }
    }

    #[test]
    fn missing_id_is_rejected() {
        let mut req = base();
        req.entity_id = None;
        req.payment_id = None;
        assert!(normalize(req).unwrap_err().contains("required"));
    }

    #[test]
    fn bank_kind_aliases_to_imps() {
        assert_eq!(parse_rail(Some("BANK")), Some(Rail::Imps));
        assert_eq!(parse_rail(Some("bank_account")), Some(Rail::Imps));
        assert_eq!(parse_rail(Some("upi")), Some(Rail::Upi));
        assert_eq!(parse_rail(Some("nb")), Some(Rail::Netbanking));
        assert_eq!(parse_rail(Some("WALLET")), Some(Rail::Wallet));
        assert_eq!(parse_rail(Some("garbage")), None);
    }

    #[test]
    fn rewrite_thresholds() {
        let mut n = normalize(base()).unwrap();
        n.amount_minor = 19_999_999;
        assert_eq!(rewrite_rail(&mut n), None);
        assert_eq!(n.rail, Rail::Imps);

        n.amount_minor = 20_000_000;
        assert_eq!(rewrite_rail(&mut n), Some(Rail::Imps));
        assert_eq!(n.rail, Rail::Neft);

        let mut n = normalize(base()).unwrap();
        n.amount_minor = 50_000_000;
        assert_eq!(rewrite_rail(&mut n), Some(Rail::Imps));
        assert_eq!(n.rail, Rail::Rtgs);

        let mut n = normalize(base()).unwrap();
        n.rail = Rail::Neft;
        n.amount_minor = 80_000_000;
        assert_eq!(rewrite_rail(&mut n), None);
        assert_eq!(n.rail, Rail::Neft);
    }
}
