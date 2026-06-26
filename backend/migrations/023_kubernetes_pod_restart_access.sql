ALTER TABLE kubernetes_environments
    ADD COLUMN IF NOT EXISTS rbac_restart_pods_status TEXT NOT NULL DEFAULT 'not_reviewed';
