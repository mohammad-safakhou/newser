-- Topic Update Policies define temporal execution preferences per topic.
CREATE TABLE IF NOT EXISTS topic_update_policies (
    topic_id UUID PRIMARY KEY REFERENCES topics(id) ON DELETE CASCADE,
    refresh_interval_seconds BIGINT NOT NULL DEFAULT 0 CHECK (refresh_interval_seconds >= 0),
    dedup_window_seconds BIGINT NOT NULL DEFAULT 0 CHECK (dedup_window_seconds >= 0),
    repeat_mode TEXT NOT NULL,
    freshness_threshold_seconds BIGINT NOT NULL DEFAULT 0 CHECK (freshness_threshold_seconds >= 0),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Ensure updated_at captures modifications.
CREATE OR REPLACE FUNCTION touch_topic_update_policy_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS topic_update_policies_set_updated_at ON topic_update_policies;
CREATE TRIGGER topic_update_policies_set_updated_at
BEFORE UPDATE ON topic_update_policies
FOR EACH ROW EXECUTE FUNCTION touch_topic_update_policy_updated_at();
