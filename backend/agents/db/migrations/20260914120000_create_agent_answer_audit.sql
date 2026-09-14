-- Agent finance-answer audit. Prompt, model, and evidence set so a past
-- answer can be reproduced without trusting the model memory.
CREATE TABLE IF NOT EXISTS agent_answer_audit (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    question TEXT NOT NULL,
    intent TEXT NOT NULL,
    model_version TEXT NOT NULL,
    evidence_ids TEXT[] NOT NULL DEFAULT '{}',
    answer TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS agent_answer_audit_tenant_idx
    ON agent_answer_audit (tenant_id, created_at DESC);
