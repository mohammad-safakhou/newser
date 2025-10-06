CREATE TABLE builder_schemas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    version INTEGER NOT NULL,
    content JSONB NOT NULL,
    author_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (topic_id, kind, version)
);

CREATE INDEX idx_builder_schemas_topic_kind ON builder_schemas(topic_id, kind);
