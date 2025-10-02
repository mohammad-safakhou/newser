DROP TRIGGER IF EXISTS topic_budget_configs_set_updated_at ON topic_budget_configs;
DROP FUNCTION IF EXISTS touch_topic_budget_updated_at();
DROP TABLE IF EXISTS topic_budget_configs;

ALTER TABLE runs
    DROP COLUMN IF EXISTS budget_cost_limit,
    DROP COLUMN IF EXISTS budget_token_limit,
    DROP COLUMN IF EXISTS budget_time_seconds,
    DROP COLUMN IF EXISTS budget_approval_threshold,
    DROP COLUMN IF EXISTS budget_require_approval,
    DROP COLUMN IF EXISTS budget_overrun,
    DROP COLUMN IF EXISTS budget_overrun_reason;
