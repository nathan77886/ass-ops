CREATE TABLE IF NOT EXISTS provider_review_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_approval_id UUID NOT NULL REFERENCES operation_approvals(id) ON DELETE CASCADE,
    project_template_run_id UUID REFERENCES project_template_runs(id) ON DELETE SET NULL,
    provider_type TEXT NOT NULL DEFAULT '',
    review_kind TEXT NOT NULL DEFAULT '',
    operation_name TEXT NOT NULL,
    endpoint_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'planned',
    replay_check TEXT NOT NULL DEFAULT '',
    conflict_policy TEXT NOT NULL DEFAULT '',
    retry_policy TEXT NOT NULL DEFAULT '',
    idempotency_key_kind TEXT NOT NULL DEFAULT 'operation_scope_hash',
    idempotency_key_hash TEXT NOT NULL DEFAULT '',
    idempotency_key_material JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    response_diagnostics JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_api_call_made BOOLEAN NOT NULL DEFAULT false,
    provider_api_mutation TEXT NOT NULL DEFAULT 'disabled',
    external_call_made BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('planned', 'running', 'completed', 'failed', 'blocked', 'skipped')),
    CHECK (provider_api_mutation IN ('disabled', 'enabled')),
    -- Defensive contract for the planning-only phase: future real provider calls must first add a migration that unlocks these fields.
    CHECK (provider_api_call_made = false),
    CHECK (external_call_made = false),
    CHECK (idempotency_key_hash = '')
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_review_attempts_approval_operation
    ON provider_review_attempts(operation_approval_id, operation_name);

CREATE INDEX IF NOT EXISTS idx_provider_review_attempts_template_run
    ON provider_review_attempts(project_template_run_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_provider_review_attempts_status
    ON provider_review_attempts(status, created_at DESC);
