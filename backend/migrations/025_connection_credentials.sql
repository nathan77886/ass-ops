CREATE TABLE IF NOT EXISTS connection_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    secret_ciphertext TEXT NOT NULL DEFAULT '',
    public_value TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT connection_credentials_kind_check CHECK (kind IN ('ssh_key', 'ssh_password', 'argo_token'))
);

ALTER TABLE connection_credentials
    ADD COLUMN IF NOT EXISTS public_value TEXT NOT NULL DEFAULT '';

ALTER TABLE argo_connections
    ADD COLUMN IF NOT EXISTS credential_id UUID REFERENCES connection_credentials(id) ON DELETE SET NULL;

ALTER TABLE ssh_machines
    ADD COLUMN IF NOT EXISTS credential_id UUID REFERENCES connection_credentials(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_connection_credentials_project_kind ON connection_credentials(project_id, kind, created_at);
CREATE INDEX IF NOT EXISTS idx_argo_connections_credential ON argo_connections(credential_id);
CREATE INDEX IF NOT EXISTS idx_ssh_machines_credential ON ssh_machines(credential_id);
