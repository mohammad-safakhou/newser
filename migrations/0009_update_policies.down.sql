DROP TRIGGER IF EXISTS topic_update_policies_set_updated_at ON topic_update_policies;
DROP FUNCTION IF EXISTS touch_topic_update_policy_updated_at();
DROP TABLE IF EXISTS topic_update_policies;
