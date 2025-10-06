CREATE TABLE run_manifests (
    run_id UUID PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    manifest JSONB NOT NULL,
    checksum TEXT NOT NULL,
    signature TEXT NOT NULL,
    algorithm TEXT NOT NULL DEFAULT 'hmac-sha256',
    signed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_run_manifests_topic ON run_manifests(topic_id);
