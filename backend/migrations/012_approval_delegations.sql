CREATE TABLE IF NOT EXISTS operation_approval_delegations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_approval_id UUID NOT NULL REFERENCES operation_approvals(id) ON DELETE CASCADE,
    from_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    to_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason TEXT NOT NULL DEFAULT '',
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(operation_approval_id, to_user_id)
);

CREATE INDEX IF NOT EXISTS idx_operation_approval_delegations_approval
    ON operation_approval_delegations(operation_approval_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_operation_approval_delegations_user
    ON operation_approval_delegations(to_user_id, created_at DESC)
    WHERE revoked_at IS NULL;
