CREATE TABLE IF NOT EXISTS kubernetes_environments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    cluster_name TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    kubeconfig_secret_ref TEXT NOT NULL DEFAULT '',
    service_account TEXT NOT NULL DEFAULT '',
    token_subject_review_status TEXT NOT NULL DEFAULT 'not_reviewed',
    rbac_read_logs_status TEXT NOT NULL DEFAULT 'not_reviewed',
    status TEXT NOT NULL DEFAULT 'metadata_only',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_id, environment, cluster_name, namespace)
);

CREATE INDEX IF NOT EXISTS idx_kubernetes_environments_project
    ON kubernetes_environments(project_id, environment, namespace);

CREATE INDEX IF NOT EXISTS idx_kubernetes_environments_scope
    ON kubernetes_environments(project_id, cluster_name, namespace);
