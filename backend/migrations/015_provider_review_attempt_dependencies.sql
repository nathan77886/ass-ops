ALTER TABLE provider_review_attempts
    ADD COLUMN IF NOT EXISTS operation_order INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS depends_on_operation TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS dependency_status TEXT NOT NULL DEFAULT 'independent';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'provider_review_attempts_dependency_status_check'
    ) THEN
        ALTER TABLE provider_review_attempts
            ADD CONSTRAINT provider_review_attempts_dependency_status_check
            CHECK (dependency_status IN ('independent', 'waiting_for_dependency', 'dependency_satisfied', 'dependency_failed'));
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'provider_review_attempts_depends_on_operation_check'
    ) THEN
        ALTER TABLE provider_review_attempts
            ADD CONSTRAINT provider_review_attempts_depends_on_operation_check
            CHECK (depends_on_operation IN ('', 'create_branch_ref', 'commit_starter_files'));
    END IF;
END $$;

UPDATE provider_review_attempts
SET operation_order = CASE operation_name
        WHEN 'create_branch_ref' THEN 10
        WHEN 'commit_starter_files' THEN 20
        WHEN 'open_review_request' THEN 30
        ELSE operation_order
    END,
    depends_on_operation = CASE operation_name
        WHEN 'commit_starter_files' THEN 'create_branch_ref'
        WHEN 'open_review_request' THEN 'commit_starter_files'
        ELSE depends_on_operation
    END,
    dependency_status = CASE operation_name
        WHEN 'commit_starter_files' THEN 'waiting_for_dependency'
        WHEN 'open_review_request' THEN 'waiting_for_dependency'
        ELSE 'independent'
    END
WHERE operation_order = 0
    AND depends_on_operation = ''
    AND dependency_status = 'independent';

CREATE INDEX IF NOT EXISTS idx_provider_review_attempts_approval_order
    ON provider_review_attempts(operation_approval_id, operation_order, operation_name);
