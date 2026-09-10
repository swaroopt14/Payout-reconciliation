use crate::catalog::Processor;
use crate::types::{Eliminated, NormalizedRequest, Psp};

pub fn filter(
    req: &NormalizedRequest,
    processors: &[Processor],
    excluded: &[Psp],
) -> (Vec<Processor>, Vec<Eliminated>) {
    let mut eligible = Vec::new();
    let mut eliminated = Vec::new();
    for p in processors {
        if excluded.contains(&p.id) {
            eliminated.push(Eliminated {
                psp: p.id,
                reason: "rule_excluded".into(),
            });
            continue;
        }
        match p.supports(req.direction, req.rail, &req.currency) {
            Ok(()) => eligible.push(p.clone()),
            Err(reason) => eliminated.push(Eliminated {
                psp: p.id,
                reason: reason.into(),
            }),
        }
    }
    (eligible, eliminated)
}
