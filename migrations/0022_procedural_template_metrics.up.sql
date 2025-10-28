CREATE TABLE IF NOT EXISTS procedural_template_fingerprints (
    topic_id UUID NOT NULL,
    fingerprint TEXT NOT NULL,
    occurrences INTEGER NOT NULL DEFAULT 1,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    template_id UUID REFERENCES procedural_templates(id) ON DELETE SET NULL,
    sample_graph JSONB NOT NULL,
    sample_parameters JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (topic_id, fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_proc_template_fingerprints_template
    ON procedural_template_fingerprints (template_id);

CREATE TABLE IF NOT EXISTS procedural_template_usage (
    id BIGSERIAL PRIMARY KEY,
    run_id UUID,
    topic_id UUID NOT NULL,
    template_id UUID REFERENCES procedural_templates(id) ON DELETE SET NULL,
    template_version INTEGER,
    fingerprint TEXT,
    stage TEXT,
    success BOOLEAN NOT NULL DEFAULT TRUE,
    latency_ms DOUBLE PRECISION,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_proc_template_usage_template
    ON procedural_template_usage (template_id);

CREATE INDEX IF NOT EXISTS idx_proc_template_usage_topic_created
    ON procedural_template_usage (topic_id, created_at DESC);

CREATE TABLE IF NOT EXISTS procedural_template_metrics (
    template_id UUID PRIMARY KEY REFERENCES procedural_templates(id) ON DELETE CASCADE,
    usage_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    total_latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
