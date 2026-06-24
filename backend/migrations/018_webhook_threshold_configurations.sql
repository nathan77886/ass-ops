CREATE TABLE IF NOT EXISTS webhook_threshold_configurations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    webhook_connection_id UUID NOT NULL REFERENCES webhook_connections(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL DEFAULT '',
    threshold_key TEXT NOT NULL,
    warning_at INTEGER NOT NULL,
    danger_at INTEGER NOT NULL,
    unit TEXT NOT NULL DEFAULT '',
    evidence_window TEXT NOT NULL DEFAULT '7d',
    source_audit_id UUID REFERENCES webhook_threshold_decision_audits(id) ON DELETE SET NULL,
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    applied_by UUID REFERENCES users(id) ON DELETE SET NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT webhook_threshold_configurations_non_negative CHECK (warning_at >= 0 AND danger_at >= 0),
    CONSTRAINT webhook_threshold_configurations_order CHECK (danger_at >= warning_at)
);

CREATE INDEX IF NOT EXISTS idx_webhook_threshold_configurations_connection
    ON webhook_threshold_configurations(webhook_connection_id, applied_at DESC);

CREATE INDEX IF NOT EXISTS idx_webhook_threshold_configurations_project
    ON webhook_threshold_configurations(project_id, applied_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_threshold_configurations_once
    ON webhook_threshold_configurations(webhook_connection_id, threshold_key, evidence_window);
