ALTER TABLE agent_tool_calls
    ADD COLUMN IF NOT EXISTS operation_run_id UUID REFERENCES operation_runs(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS finished_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE agent_tool_calls atc
SET project_id=at.project_id,
    updated_at=now()
FROM agent_tasks at
WHERE at.id=atc.agent_task_id
    AND atc.project_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_tool_calls_task_created ON agent_tool_calls(agent_task_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_tool_calls_operation ON agent_tool_calls(operation_run_id, created_at) WHERE operation_run_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_tool_calls_project ON agent_tool_calls(project_id, created_at DESC) WHERE project_id IS NOT NULL;
