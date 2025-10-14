CREATE TABLE IF NOT EXISTS attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    artifact_id TEXT NOT NULL,
    name TEXT,
    media_type TEXT,
    size_bytes BIGINT,
    checksum TEXT,
    storage_uri TEXT NOT NULL,
    retention_expires_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_attachments_run_task_artifact
    ON attachments (run_id, task_id, artifact_id);

CREATE INDEX IF NOT EXISTS idx_attachments_retention
    ON attachments (retention_expires_at)
    WHERE retention_expires_at IS NOT NULL;
