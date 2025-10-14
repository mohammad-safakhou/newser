CREATE TABLE IF NOT EXISTS run_budget_events (
    id BIGSERIAL PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    topic_id UUID REFERENCES topics(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_run_budget_events_run_type ON run_budget_events (run_id, event_type);
CREATE INDEX IF NOT EXISTS idx_run_budget_events_created_at ON run_budget_events (created_at);
