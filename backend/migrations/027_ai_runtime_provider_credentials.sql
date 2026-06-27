ALTER TABLE connection_credentials
    DROP CONSTRAINT IF EXISTS connection_credentials_kind_check;

ALTER TABLE connection_credentials
    ADD CONSTRAINT connection_credentials_kind_check CHECK (kind IN ('ssh_key', 'ssh_password', 'argo_token', 'provider_token', 'ai_provider_api_key'));

ALTER TABLE ai_runtimes
    ADD COLUMN IF NOT EXISTS provider_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS api_base_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS credential_id UUID REFERENCES connection_credentials(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_ai_runtimes_credential ON ai_runtimes(credential_id);
CREATE INDEX IF NOT EXISTS idx_connection_credentials_ai_provider_key ON connection_credentials(kind, created_at) WHERE kind='ai_provider_api_key';
