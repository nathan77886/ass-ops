ALTER TABLE operation_approval_rules
    ADD COLUMN IF NOT EXISTS required_approval_count INTEGER NOT NULL DEFAULT 1;

ALTER TABLE operation_approvals
    ADD COLUMN IF NOT EXISTS required_approval_count INTEGER NOT NULL DEFAULT 1;

UPDATE operation_approval_rules
SET required_approval_count=1
WHERE required_approval_count < 1;

UPDATE operation_approvals
SET required_approval_count=1
WHERE required_approval_count < 1;

CREATE TABLE IF NOT EXISTS operation_approval_decisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_approval_id UUID NOT NULL REFERENCES operation_approvals(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    decision TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (decision IN ('approved', 'rejected')),
    UNIQUE (operation_approval_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_operation_approval_decisions_approval ON operation_approval_decisions(operation_approval_id, decided_at);
CREATE INDEX IF NOT EXISTS idx_operation_approval_decisions_user ON operation_approval_decisions(user_id, decided_at DESC);
