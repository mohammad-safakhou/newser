DROP INDEX IF EXISTS idx_plan_step_embeddings_embedding;
DROP INDEX IF EXISTS idx_plan_step_embeddings_topic_task;
DROP TABLE IF EXISTS plan_step_embeddings;

DROP INDEX IF EXISTS idx_run_embeddings_embedding;
DROP INDEX IF EXISTS idx_run_embeddings_topic_kind;
DROP TABLE IF EXISTS run_embeddings;

-- Extension intentionally left installed if already present.
