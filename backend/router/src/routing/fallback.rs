use crate::types::{Candidate, FallbackHop};

pub fn chain(ranked: &[Candidate]) -> Vec<FallbackHop> {
    ranked
        .iter()
        .skip(1)
        .map(|c| FallbackHop {
            psp: c.psp,
            connector_id: c.connector_id.clone(),
        })
        .collect()
}
