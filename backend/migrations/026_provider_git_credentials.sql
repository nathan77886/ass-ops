ALTER TABLE connection_credentials
    ALTER COLUMN project_id DROP NOT NULL;

ALTER TABLE connection_credentials
    DROP CONSTRAINT IF EXISTS connection_credentials_kind_check;

ALTER TABLE connection_credentials
    ADD CONSTRAINT connection_credentials_kind_check CHECK (kind IN ('ssh_key', 'ssh_password', 'argo_token', 'provider_token'));

ALTER TABLE provider_accounts
    ADD COLUMN IF NOT EXISTS credential_id UUID REFERENCES connection_credentials(id) ON DELETE SET NULL;

DO $$
DECLARE r record;
BEGIN
    FOR r IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'provider_accounts'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%token_env%'
          AND pg_get_constraintdef(oid) LIKE '%enabled%'
    LOOP
        EXECUTE format('ALTER TABLE provider_accounts DROP CONSTRAINT %I', r.conname);
    END LOOP;
END $$;

ALTER TABLE provider_accounts
    ADD CONSTRAINT provider_accounts_enabled_credential_check CHECK (NOT enabled OR token_env <> '' OR credential_id IS NOT NULL);

ALTER TABLE git_remotes
    ADD COLUMN IF NOT EXISTS credential_id UUID REFERENCES connection_credentials(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_connection_credentials_global_kind ON connection_credentials(kind, created_at) WHERE project_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_provider_accounts_credential ON provider_accounts(credential_id);
CREATE INDEX IF NOT EXISTS idx_git_remotes_credential ON git_remotes(credential_id);
