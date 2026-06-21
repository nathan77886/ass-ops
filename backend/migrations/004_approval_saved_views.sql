CREATE TABLE IF NOT EXISTS operation_approval_views (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    filters JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (name <> ''),
    UNIQUE (user_id, name)
);

CREATE INDEX IF NOT EXISTS idx_operation_approval_views_user_updated ON operation_approval_views(user_id, updated_at DESC);
