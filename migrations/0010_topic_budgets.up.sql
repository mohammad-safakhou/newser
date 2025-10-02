-- Topic-level budget configurations capture spend/time/token guardrails.
CREATE TABLE IF NOT EXISTS topic_budget_configs (
    topic_id UUID PRIMARY KEY REFERENCES topics(id) ON DELETE CASCADE,
    max_cost NUMERIC(12,4),
    max_tokens BIGINT,
    max_time_seconds BIGINT,
    approval_threshold NUMERIC(12,4),
    require_approval BOOLEAN NOT NULL DEFAULT FALSE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION touch_topic_budget_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS topic_budget_configs_set_updated_at ON topic_budget_configs;
CREATE TRIGGER topic_budget_configs_set_updated_at
BEFORE UPDATE ON topic_budget_configs
FOR EACH ROW EXECUTE FUNCTION touch_topic_budget_updated_at();

ALTER TABLE runs
    ADD COLUMN IF NOT EXISTS budget_cost_limit NUMERIC(12,4),
    ADD COLUMN IF NOT EXISTS budget_token_limit BIGINT,
    ADD COLUMN IF NOT EXISTS budget_time_seconds BIGINT,
    ADD COLUMN IF NOT EXISTS budget_approval_threshold NUMERIC(12,4),
    ADD COLUMN IF NOT EXISTS budget_require_approval BOOLEAN DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS budget_overrun BOOLEAN DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS budget_overrun_reason TEXT;
