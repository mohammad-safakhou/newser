CREATE TABLE IF NOT EXISTS procedural_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic_id UUID REFERENCES topics(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    description TEXT,
    current_version INTEGER NOT NULL DEFAULT 1,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_procedural_templates_topic_name
    ON procedural_templates (topic_id, name);

CREATE TABLE IF NOT EXISTS procedural_template_versions (
    id BIGSERIAL PRIMARY KEY,
    template_id UUID NOT NULL REFERENCES procedural_templates(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'pending_approval', 'approved', 'deprecated')),
    graph JSONB NOT NULL,
    parameters JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    changelog TEXT,
    approved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_procedural_template_versions_unique
    ON procedural_template_versions (template_id, version);

CREATE INDEX IF NOT EXISTS idx_procedural_template_versions_status
    ON procedural_template_versions (status);
