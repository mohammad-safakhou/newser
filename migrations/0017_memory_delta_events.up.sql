CREATE TABLE IF NOT EXISTS memory_delta_events (
    id BIGSERIAL PRIMARY KEY,
    topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    total_items INTEGER NOT NULL,
    novel_items INTEGER NOT NULL,
    duplicate_items INTEGER NOT NULL,
    semantic_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_delta_events_topic_created_at ON memory_delta_events (topic_id, created_at DESC);
