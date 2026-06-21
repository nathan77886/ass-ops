ALTER TABLE operation_approval_rules
    ADD COLUMN IF NOT EXISTS escalation_after_minutes INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS escalation_channels TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];

ALTER TABLE operation_approvals
    ADD COLUMN IF NOT EXISTS escalation_after_minutes INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS escalation_channels TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    ADD COLUMN IF NOT EXISTS last_escalated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS escalation_count INTEGER NOT NULL DEFAULT 0;

UPDATE operation_approval_rules
SET escalation_after_minutes=0
WHERE escalation_after_minutes < 0;

UPDATE operation_approvals
SET escalation_after_minutes=0
WHERE escalation_after_minutes < 0;

UPDATE operation_approvals
SET escalation_count=0
WHERE escalation_count < 0;

CREATE INDEX IF NOT EXISTS idx_operation_approvals_escalation_due
    ON operation_approvals(status, last_escalated_at, created_at)
    WHERE status='pending' AND escalation_after_minutes > 0;
