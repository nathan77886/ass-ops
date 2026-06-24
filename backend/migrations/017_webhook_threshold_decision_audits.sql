CREATE TABLE IF NOT EXISTS webhook_threshold_decision_audits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    webhook_connection_id UUID NOT NULL REFERENCES webhook_connections(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL DEFAULT '',
    threshold_review_state TEXT NOT NULL DEFAULT '',
    decision_state TEXT NOT NULL DEFAULT '',
    operator_decision TEXT NOT NULL DEFAULT '',
    evidence_window TEXT NOT NULL DEFAULT '7d',
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_webhook_threshold_decision_audits_connection
    ON webhook_threshold_decision_audits(webhook_connection_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_webhook_threshold_decision_audits_project
    ON webhook_threshold_decision_audits(project_id, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_threshold_decision_audits_once
    ON webhook_threshold_decision_audits(webhook_connection_id, decision_state, evidence_window);
