CREATE TABLE IF NOT EXISTS memory_jobs (
    id BIGSERIAL PRIMARY KEY,
    topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    job_type TEXT NOT NULL CHECK (job_type IN ('summarise', 'prune')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'success', 'failed')),
    scheduled_for TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    params JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_jobs_topic_status
    ON memory_jobs (topic_id, status);

CREATE INDEX IF NOT EXISTS idx_memory_jobs_scheduled
    ON memory_jobs (scheduled_for)
    WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS memory_job_results (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT NOT NULL REFERENCES memory_jobs(id) ON DELETE CASCADE,
    metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
    summary TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_job_results_job
    ON memory_job_results (job_id);
