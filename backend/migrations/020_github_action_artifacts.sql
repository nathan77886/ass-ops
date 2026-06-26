CREATE TABLE IF NOT EXISTS github_action_artifacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    git_remote_id UUID NOT NULL REFERENCES git_remotes(id) ON DELETE CASCADE,
    github_action_run_id UUID NOT NULL REFERENCES github_action_runs(id) ON DELETE CASCADE,
    external_artifact_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    size_in_bytes BIGINT NOT NULL DEFAULT 0,
    expired BOOLEAN NOT NULL DEFAULT false,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    synced_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_github_action_artifacts_remote
    ON github_action_artifacts(git_remote_id, synced_at);

CREATE INDEX IF NOT EXISTS idx_github_action_artifacts_run
    ON github_action_artifacts(github_action_run_id, name);

CREATE UNIQUE INDEX IF NOT EXISTS idx_github_action_artifacts_run_external
    ON github_action_artifacts(github_action_run_id, external_artifact_id)
    WHERE external_artifact_id <> '';
