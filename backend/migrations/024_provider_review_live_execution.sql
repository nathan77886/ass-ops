SELECT pg_advisory_xact_lock(hashtext('assops:024_provider_review_live_execution'));

ALTER TABLE provider_review_attempts
    ADD COLUMN IF NOT EXISTS provider_status_class TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider_review_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS executed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS live_execution_phase TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS live_execution_retryable BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS live_execution_manual_cleanup_hint TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cleanup_attempted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS cleanup_succeeded BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS cleanup_required BOOLEAN NOT NULL DEFAULT false;

DO $$
DECLARE
    constraint_record RECORD;
BEGIN
    FOR constraint_record IN
        SELECT conname, pg_get_constraintdef(oid) AS definition
        FROM pg_constraint
        WHERE conrelid = 'provider_review_attempts'::regclass
            AND contype = 'c'
            AND (
                pg_get_constraintdef(oid) = 'CHECK ((provider_api_call_made = false))'
                OR pg_get_constraintdef(oid) = 'CHECK ((external_call_made = false))'
                OR pg_get_constraintdef(oid) = 'CHECK ((idempotency_key_hash = ''''::text))'
            )
    LOOP
        EXECUTE format('ALTER TABLE provider_review_attempts DROP CONSTRAINT %I', constraint_record.conname);
    END LOOP;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'provider_review_attempts_live_call_requires_enabled_mutation'
    ) THEN
        ALTER TABLE provider_review_attempts
            ADD CONSTRAINT provider_review_attempts_live_call_requires_enabled_mutation
            CHECK (provider_api_call_made = false OR provider_api_mutation = 'enabled');
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'provider_review_attempts_external_call_requires_enabled_mutation'
    ) THEN
        ALTER TABLE provider_review_attempts
            ADD CONSTRAINT provider_review_attempts_external_call_requires_enabled_mutation
            CHECK (external_call_made = false OR provider_api_mutation = 'enabled');
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'provider_review_attempts_idempotency_hash_requires_enabled_mutation'
    ) THEN
        ALTER TABLE provider_review_attempts
            ADD CONSTRAINT provider_review_attempts_idempotency_hash_requires_enabled_mutation
            CHECK (idempotency_key_hash = '' OR provider_api_mutation = 'enabled');
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'provider_review_attempts_provider_status_class_check'
    ) THEN
        ALTER TABLE provider_review_attempts
            ADD CONSTRAINT provider_review_attempts_provider_status_class_check
            CHECK (provider_status_class IN ('', '2xx', '4xx', '5xx', 'unknown'));
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'provider_review_attempts_live_execution_phase_check'
    ) THEN
        ALTER TABLE provider_review_attempts
            ADD CONSTRAINT provider_review_attempts_live_execution_phase_check
            CHECK (live_execution_phase IN ('', 'input_validation', 'read_base_ref', 'create_review_branch', 'commit_starter_files', 'open_review_request', 'cleanup_review_branch', 'completed'));
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'provider_review_attempts_live_execution_manual_cleanup_hint_check'
    ) THEN
        ALTER TABLE provider_review_attempts
            ADD CONSTRAINT provider_review_attempts_live_execution_manual_cleanup_hint_check
            CHECK (live_execution_manual_cleanup_hint IN ('', 'review_branch_delete_required'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_provider_review_attempts_live_execution
    ON provider_review_attempts(executed_at DESC)
    WHERE provider_api_call_made = true;

CREATE INDEX IF NOT EXISTS idx_provider_review_attempts_cleanup_required
    ON provider_review_attempts(updated_at DESC)
    WHERE cleanup_required = true;
