CREATE TABLE IF NOT EXISTS provider_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    provider_type TEXT NOT NULL,
    api_base_url TEXT NOT NULL DEFAULT '',
    web_base_url TEXT NOT NULL DEFAULT '',
    token_env TEXT NOT NULL DEFAULT '',
    default_owner TEXT NOT NULL DEFAULT '',
    visibility TEXT NOT NULL DEFAULT 'private',
    enabled BOOLEAN NOT NULL DEFAULT true,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (provider_type IN ('github', 'gitea')),
    CHECK (visibility IN ('public', 'private', 'internal')),
    CHECK (NOT enabled OR token_env <> '')
);

CREATE INDEX IF NOT EXISTS idx_provider_accounts_provider_enabled ON provider_accounts(provider_type, enabled, updated_at);

UPDATE git_remotes gr
SET source_account_id = NULL
WHERE gr.source_account_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM provider_accounts pa WHERE pa.id = gr.source_account_id
  );

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_git_remotes_source_account_provider_accounts'
    ) THEN
        ALTER TABLE git_remotes
            ADD CONSTRAINT fk_git_remotes_source_account_provider_accounts
            FOREIGN KEY (source_account_id) REFERENCES provider_accounts(id) ON DELETE SET NULL;
    END IF;
END $$;
