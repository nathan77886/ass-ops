ALTER TABLE operation_approvals
    ADD COLUMN IF NOT EXISTS last_reminded_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS reminder_count INTEGER NOT NULL DEFAULT 0;

UPDATE operation_approvals
SET reminder_count=0
WHERE reminder_count < 0;

CREATE INDEX IF NOT EXISTS idx_operation_approvals_reminder_due
    ON operation_approvals(status, last_reminded_at, expires_at, created_at)
    WHERE status='pending';
