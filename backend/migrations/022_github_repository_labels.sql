CREATE TABLE IF NOT EXISTS github_repository_labels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_run_id UUID REFERENCES operation_runs(id) ON DELETE SET NULL,
    git_remote_id UUID NOT NULL REFERENCES git_remotes(id) ON DELETE CASCADE,
    external_label_id TEXT NOT NULL DEFAULT '',
    node_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    color TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    is_default BOOLEAN NOT NULL DEFAULT false,
    synced_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_github_repository_labels_remote
    ON github_repository_labels(git_remote_id, synced_at);

CREATE UNIQUE INDEX IF NOT EXISTS idx_github_repository_labels_remote_name
    ON github_repository_labels(git_remote_id, lower(name));

CREATE UNIQUE INDEX IF NOT EXISTS idx_github_repository_labels_remote_external
    ON github_repository_labels(git_remote_id, external_label_id)
    WHERE external_label_id <> '';
