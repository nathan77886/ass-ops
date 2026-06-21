ALTER TABLE project_git_repositories
    ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS repo_role TEXT NOT NULL DEFAULT 'code',
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';

ALTER TABLE git_remotes
    ADD COLUMN IF NOT EXISTS remote_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_provider_id UUID,
    ADD COLUMN IF NOT EXISTS source_account_id UUID,
    ADD COLUMN IF NOT EXISTS remote_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS web_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS remote_role TEXT NOT NULL DEFAULT 'mirror',
    ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS sync_enabled BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS protected BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS latest_sha TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_sync_status TEXT NOT NULL DEFAULT 'never';

UPDATE git_remotes
SET remote_key = name
WHERE remote_key = '';

UPDATE git_remotes
SET provider_type = kind
WHERE provider_type = '';

UPDATE git_remotes
SET remote_url = COALESCE(urls->>0, '')
WHERE remote_url = '';

ALTER TABLE repo_sync_runs
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS project_git_repository_id UUID REFERENCES project_git_repositories(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS repo_sync_asset_id UUID,
    ADD COLUMN IF NOT EXISTS source_remote_id UUID REFERENCES git_remotes(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS target_remote_id UUID REFERENCES git_remotes(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS ref TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS before_sha TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS after_sha TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS stdout TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS stderr TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS finished_at TIMESTAMPTZ;

ALTER TABLE repo_tag_runs
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS project_git_repository_id UUID REFERENCES project_git_repositories(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS target_remote_id UUID REFERENCES git_remotes(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS target_sha TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tag_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS stdout TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS stderr TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS finished_at TIMESTAMPTZ;

ALTER TABLE github_action_runs
    ADD COLUMN IF NOT EXISTS workflow_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS run_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS branch TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commit_sha TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS conclusion TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS html_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_repo_sync_runs_repo ON repo_sync_runs(project_git_repository_id, created_at);
CREATE INDEX IF NOT EXISTS idx_repo_sync_runs_asset ON repo_sync_runs(repo_sync_asset_id, created_at);
CREATE INDEX IF NOT EXISTS idx_repo_sync_runs_source ON repo_sync_runs(source_remote_id, created_at);
CREATE INDEX IF NOT EXISTS idx_repo_sync_runs_target ON repo_sync_runs(target_remote_id, created_at);
CREATE INDEX IF NOT EXISTS idx_repo_sync_runs_remote ON repo_sync_runs(git_remote_id, created_at);
CREATE INDEX IF NOT EXISTS idx_repo_tag_runs_repo ON repo_tag_runs(project_git_repository_id, created_at);
CREATE INDEX IF NOT EXISTS idx_github_action_runs_remote ON github_action_runs(git_remote_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_github_action_runs_remote_external
    ON github_action_runs(git_remote_id, external_run_id)
    WHERE external_run_id <> '';

ALTER TABLE ssh_command_runs
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_ssh_command_runs_project ON ssh_command_runs(project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_ssh_command_runs_machine ON ssh_command_runs(ssh_machine_id, created_at);

ALTER TABLE argo_connections
    ADD COLUMN IF NOT EXISTS last_sync_status TEXT NOT NULL DEFAULT 'never',
    ADD COLUMN IF NOT EXISTS last_sync_error TEXT NOT NULL DEFAULT '';

ALTER TABLE argo_apps
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS deployment_targets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT 'default',
    cluster_name TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'argocd',
    argo_connection_id UUID REFERENCES argo_connections(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'unknown',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_id, environment, cluster_name, namespace)
);

ALTER TABLE argo_apps
    ADD COLUMN IF NOT EXISTS deployment_target_id UUID REFERENCES deployment_targets(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_argo_apps_connection ON argo_apps(argo_connection_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_argo_apps_conn_name ON argo_apps(argo_connection_id, name);
CREATE INDEX IF NOT EXISTS idx_argo_apps_deployment_target ON argo_apps(deployment_target_id, created_at);
CREATE INDEX IF NOT EXISTS idx_deployment_targets_project ON deployment_targets(project_id, environment, namespace);

CREATE TABLE IF NOT EXISTS deployment_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    deployment_target_id UUID REFERENCES deployment_targets(id) ON DELETE SET NULL,
    argo_connection_id UUID REFERENCES argo_connections(id) ON DELETE SET NULL,
    argo_app_id UUID REFERENCES argo_apps(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    cluster_name TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'argocd',
    status TEXT NOT NULL DEFAULT 'unknown',
    revision TEXT NOT NULL DEFAULT '',
    image_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_id, source, name, environment, namespace, cluster_name)
);

CREATE TABLE IF NOT EXISTS rollback_points (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    deployment_record_id UUID REFERENCES deployment_records(id) ON DELETE SET NULL,
    deployment_target_id UUID REFERENCES deployment_targets(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    revision TEXT NOT NULL DEFAULT '',
    image_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    source TEXT NOT NULL DEFAULT 'argocd',
    status TEXT NOT NULL DEFAULT 'available',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_id, source, name, environment, revision)
);

CREATE INDEX IF NOT EXISTS idx_deployment_records_project ON deployment_records(project_id, environment, observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_deployment_records_target ON deployment_records(deployment_target_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_rollback_points_project ON rollback_points(project_id, environment, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_rollback_points_target ON rollback_points(deployment_target_id, captured_at DESC);

CREATE TABLE IF NOT EXISTS assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    asset_type TEXT NOT NULL,
    source_table TEXT NOT NULL DEFAULT '',
    source_id UUID,
    name TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'local',
    external_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'unknown',
    risk_level TEXT NOT NULL DEFAULT 'normal',
    owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(asset_type, source_table, source_id)
);

CREATE TABLE IF NOT EXISTS asset_relations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    from_asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    to_asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS asset_status_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    health TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    raw JSONB NOT NULL DEFAULT '{}'::jsonb,
    collected_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_assets_project_type ON assets(project_id, asset_type, updated_at);
CREATE INDEX IF NOT EXISTS idx_assets_source ON assets(source_table, source_id);
CREATE INDEX IF NOT EXISTS idx_asset_relations_project ON asset_relations(project_id, relation_type, created_at);
CREATE INDEX IF NOT EXISTS idx_asset_relations_from ON asset_relations(from_asset_id, relation_type);
CREATE INDEX IF NOT EXISTS idx_asset_relations_to ON asset_relations(to_asset_id, relation_type);
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_relations_unique_relation
    ON asset_relations(from_asset_id, to_asset_id, relation_type);

CREATE TABLE IF NOT EXISTS project_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    version TEXT NOT NULL DEFAULT 'v0.1',
    status TEXT NOT NULL DEFAULT 'active',
    defaults JSONB NOT NULL DEFAULT '{}'::jsonb,
    steps JSONB NOT NULL DEFAULT '[]'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_project_templates_status ON project_templates(status, updated_at);

INSERT INTO project_templates(slug, name, description, version, defaults, steps, metadata)
VALUES (
    'go-service-basic',
    'Go Service Basic',
    'Baseline backend service template with repository, CI, sync, deployment target, and AI context placeholders.',
    'v0.1',
    '{
        "repo_role":"service",
        "default_branch":"main",
        "environments":["dev","prod"],
        "repository":{"name_suffix":"service","repo_key_suffix":"service","display_name_suffix":"Service"},
        "remotes":[
            {"name":"Gitea origin","remote_key":"gitea","provider_type":"gitea","remote_role":"source","sync_enabled":true,"default_branch":"main"},
            {"name":"GitHub mirror","remote_key":"github","provider_type":"github","remote_role":"mirror","sync_enabled":true,"default_branch":"main"}
        ],
        "repo_sync":{"name":"default mirror","source_remote_key":"gitea","target_remote_key":"github","trigger_mode":"manual","sync_mode":"selected_refs","transport":"ssh","driver":"projectops_worker_git_ssh","enabled":false},
        "files":[
            {"path":"README.md","kind":"markdown","content":"# {{project_name}}\n\nGenerated from ASSOPS template `{{template_slug}}`.\n\nRepository: `{{repository_key}}`\n"},
            {"path":"ASSOPS_CONTEXT.md","kind":"markdown","content":"# ASSOPS Context\n\nProject: {{project_name}}\nSlug: {{project_slug}}\nRepository: {{repository_key}}\n"},
            {"path":"tool-manifest.json","kind":"json","content":"{\"project\":\"{{project_slug}}\",\"repository\":\"{{repository_key}}\",\"tools\":[\"repo.sync\",\"github.actions.sync\"]}\n"}
        ]
    }'::jsonb,
    '[
        {"key":"project","title":"Create project asset","status":"planned"},
        {"key":"repository","title":"Create service repository metadata","status":"planned"},
        {"key":"remotes","title":"Bind Gitea and GitHub remotes","status":"planned"},
        {"key":"repo_sync","title":"Configure Gitea to GitHub RepoSyncAsset","status":"planned"},
        {"key":"files","title":"Plan starter repository files","status":"planned"},
        {"key":"deployment_target","title":"Prepare deployment target read model","status":"planned"},
        {"key":"context","title":"Generate ASSOPS context files","status":"planned"}
    ]'::jsonb,
    '{"source":"assops_builtin"}'::jsonb
)
ON CONFLICT(slug) DO UPDATE SET
    name=EXCLUDED.name,
    description=EXCLUDED.description,
    version=EXCLUDED.version,
    defaults=EXCLUDED.defaults,
    steps=EXCLUDED.steps,
    metadata=EXCLUDED.metadata,
    updated_at=now();

CREATE TABLE IF NOT EXISTS project_template_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_run_id UUID REFERENCES operation_runs(id) ON DELETE SET NULL,
    project_template_id UUID REFERENCES project_templates(id) ON DELETE SET NULL,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    requested_by UUID REFERENCES users(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    project_name TEXT NOT NULL,
    project_slug TEXT NOT NULL,
    input JSONB NOT NULL DEFAULT '{}'::jsonb,
    steps JSONB NOT NULL DEFAULT '[]'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_project_template_runs_template ON project_template_runs(project_template_id, created_at);
CREATE INDEX IF NOT EXISTS idx_project_template_runs_project ON project_template_runs(project_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_project_template_runs_operation ON project_template_runs(operation_run_id) WHERE operation_run_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_project_template_runs_requested_by ON project_template_runs(requested_by, created_at DESC);

CREATE TABLE IF NOT EXISTS project_template_files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_template_run_id UUID REFERENCES project_template_runs(id) ON DELETE CASCADE,
    project_template_id UUID REFERENCES project_templates(id) ON DELETE SET NULL,
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    project_git_repository_id UUID REFERENCES project_git_repositories(id) ON DELETE SET NULL,
    path TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'text',
    content TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'planned',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_template_run_id, path)
);

CREATE INDEX IF NOT EXISTS idx_project_template_files_run ON project_template_files(project_template_run_id, created_at);
CREATE INDEX IF NOT EXISTS idx_project_template_files_project ON project_template_files(project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_project_template_files_repo ON project_template_files(project_git_repository_id, created_at);

CREATE TABLE IF NOT EXISTS repo_sync_assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    project_git_repository_id UUID NOT NULL REFERENCES project_git_repositories(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    source_remote_id UUID NOT NULL REFERENCES git_remotes(id) ON DELETE CASCADE,
    target_remote_id UUID NOT NULL REFERENCES git_remotes(id) ON DELETE CASCADE,
    trigger_mode TEXT NOT NULL DEFAULT 'manual',
    sync_mode TEXT NOT NULL DEFAULT 'selected_refs',
    transport TEXT NOT NULL DEFAULT 'ssh',
    driver TEXT NOT NULL DEFAULT 'projectops_worker_git_ssh',
    refs JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_sync_status TEXT NOT NULL DEFAULT 'never',
    last_sync_run_id UUID,
    last_synced_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_git_repository_id, name),
    CHECK (source_remote_id <> target_remote_id)
);

ALTER TABLE repo_sync_assets
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'repo_sync_runs_repo_sync_asset_fk'
    ) THEN
        ALTER TABLE repo_sync_runs
            ADD CONSTRAINT repo_sync_runs_repo_sync_asset_fk
            FOREIGN KEY (repo_sync_asset_id) REFERENCES repo_sync_assets(id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_repo_sync_assets_project ON repo_sync_assets(project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_repo_sync_assets_repo ON repo_sync_assets(project_git_repository_id, created_at);
CREATE INDEX IF NOT EXISTS idx_repo_sync_assets_source_target ON repo_sync_assets(source_remote_id, target_remote_id);
CREATE INDEX IF NOT EXISTS idx_repo_sync_assets_active ON repo_sync_assets(project_git_repository_id, enabled, created_at) WHERE archived_at IS NULL;

CREATE TABLE IF NOT EXISTS webhook_connections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provider TEXT NOT NULL DEFAULT 'gitea',
    name TEXT NOT NULL,
    source_remote_id UUID REFERENCES git_remotes(id) ON DELETE SET NULL,
    secret_token TEXT NOT NULL,
    secret_ciphertext TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    event_types JSONB NOT NULL DEFAULT '["push"]'::jsonb,
    last_delivery_status TEXT NOT NULL DEFAULT 'never',
    last_delivery_error TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE webhook_connections
    ADD COLUMN IF NOT EXISTS secret_ciphertext TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_connection_id UUID REFERENCES webhook_connections(id) ON DELETE SET NULL,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    provider TEXT NOT NULL DEFAULT 'gitea',
    event_type TEXT NOT NULL DEFAULT '',
    delivery_id TEXT NOT NULL DEFAULT '',
    signature_valid BOOLEAN NOT NULL DEFAULT false,
    matched_repo_sync_asset_id UUID REFERENCES repo_sync_assets(id) ON DELETE SET NULL,
    operation_run_id UUID REFERENCES operation_runs(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'received',
    error_message TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_webhook_connections_project ON webhook_connections(project_id, provider, created_at);
CREATE INDEX IF NOT EXISTS idx_webhook_connections_source ON webhook_connections(source_remote_id, created_at);
CREATE INDEX IF NOT EXISTS idx_webhook_events_connection ON webhook_events(webhook_connection_id, received_at);
CREATE INDEX IF NOT EXISTS idx_webhook_events_project ON webhook_events(project_id, received_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_events_delivery_once
    ON webhook_events(webhook_connection_id, delivery_id)
    WHERE delivery_id <> '' AND signature_valid;

CREATE TABLE IF NOT EXISTS operation_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    operation_run_id UUID REFERENCES operation_runs(id) ON DELETE SET NULL,
    approval_rule_id UUID,
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    title TEXT NOT NULL,
    request_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending',
    required_approver_roles TEXT[] NOT NULL DEFAULT ARRAY['admin','owner']::TEXT[],
    notification_channels TEXT[] NOT NULL DEFAULT ARRAY['ui']::TEXT[],
    notification_status TEXT NOT NULL DEFAULT 'pending',
    notification_last_error TEXT NOT NULL DEFAULT '',
    requested_by UUID REFERENCES users(id) ON DELETE SET NULL,
    decided_by UUID REFERENCES users(id) ON DELETE SET NULL,
    decision_reason TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE operation_approvals
    -- Keep these ALTERs for databases where operation_approvals already existed before this migration slice.
    ADD COLUMN IF NOT EXISTS approval_rule_id UUID,
    ADD COLUMN IF NOT EXISTS required_approver_roles TEXT[] NOT NULL DEFAULT ARRAY['admin','owner']::TEXT[],
    ADD COLUMN IF NOT EXISTS notification_channels TEXT[] NOT NULL DEFAULT ARRAY['ui']::TEXT[],
    ADD COLUMN IF NOT EXISTS notification_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS notification_last_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS expired_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS operation_approval_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    required_approver_roles TEXT[] NOT NULL DEFAULT ARRAY['admin','owner']::TEXT[],
    expires_after_minutes INTEGER NOT NULL DEFAULT 1440,
    notification_channels TEXT[] NOT NULL DEFAULT ARRAY['ui']::TEXT[],
    priority INTEGER NOT NULL DEFAULT 100,
    enabled BOOLEAN NOT NULL DEFAULT true,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(resource_type, action)
);

ALTER TABLE operation_approval_rules
    -- Keep this ALTER for databases where operation_approval_rules was created before priority existed.
    ADD COLUMN IF NOT EXISTS priority INTEGER NOT NULL DEFAULT 100;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_operation_approvals_rule'
    ) THEN
        ALTER TABLE operation_approvals
            ADD CONSTRAINT fk_operation_approvals_rule
            FOREIGN KEY (approval_rule_id) REFERENCES operation_approval_rules(id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_operation_approvals_project ON operation_approvals(project_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_operation_approvals_status ON operation_approvals(status, created_at);
CREATE INDEX IF NOT EXISTS idx_operation_approvals_created ON operation_approvals(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_operation_approvals_expires ON operation_approvals(status, expires_at) WHERE status='pending';
CREATE INDEX IF NOT EXISTS idx_operation_approvals_run ON operation_approvals(operation_run_id) WHERE operation_run_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_operation_approvals_pending_once
    ON operation_approvals(resource_type, resource_id, action)
    WHERE status='pending';
CREATE INDEX IF NOT EXISTS idx_operation_approval_rules_action ON operation_approval_rules(priority, resource_type, action) WHERE enabled;

INSERT INTO operation_approval_rules(resource_type, action, required_approver_roles, expires_after_minutes, notification_channels, priority, metadata)
VALUES
    ('git_remote', 'repo.tag', ARRAY['admin','owner']::TEXT[], 1440, ARRAY['ui']::TEXT[], 10, '{"risk":"release_tag"}'::jsonb),
    ('ssh_machine', 'ssh.exec', ARRAY['admin','owner']::TEXT[], 240, ARRAY['ui']::TEXT[], 10, '{"risk":"remote_command"}'::jsonb),
    ('operation', 'operation.cancel', ARRAY['admin','owner']::TEXT[], 240, ARRAY['ui']::TEXT[], 10, '{"risk":"operation_control"}'::jsonb),
    ('agent_task', 'agent.execute', ARRAY['admin','owner']::TEXT[], 720, ARRAY['ui']::TEXT[], 10, '{"risk":"agent_mutation"}'::jsonb)
ON CONFLICT (resource_type, action) DO UPDATE
SET required_approver_roles=EXCLUDED.required_approver_roles,
    expires_after_minutes=EXCLUDED.expires_after_minutes,
    notification_channels=EXCLUDED.notification_channels,
    priority=EXCLUDED.priority,
    metadata=operation_approval_rules.metadata || EXCLUDED.metadata,
    enabled=true,
    updated_at=now();
