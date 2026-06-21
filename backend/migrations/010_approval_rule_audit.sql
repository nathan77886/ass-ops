CREATE TABLE IF NOT EXISTS operation_approval_rule_audits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_approval_rule_id UUID REFERENCES operation_approval_rules(id) ON DELETE SET NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    before_state JSONB NOT NULL DEFAULT '{}'::jsonb,
    after_state JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_operation_approval_rule_audits_rule
    ON operation_approval_rule_audits(operation_approval_rule_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_operation_approval_rule_audits_actor
    ON operation_approval_rule_audits(actor_user_id, created_at DESC);
