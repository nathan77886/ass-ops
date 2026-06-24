ALTER TABLE provider_review_attempts
    ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS claimed_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_provider_review_attempts_claimed_at
    ON provider_review_attempts(claimed_at DESC)
    WHERE claimed_at IS NOT NULL;
