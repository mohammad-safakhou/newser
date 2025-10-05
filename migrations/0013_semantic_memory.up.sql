-- Enable pgvector extension for semantic search if not already available
CREATE EXTENSION IF NOT EXISTS vector;

-- Store semantic embeddings at the run/topic level
CREATE TABLE IF NOT EXISTS run_embeddings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
  kind TEXT NOT NULL DEFAULT 'run_summary',
  embedding vector(1536) NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (run_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_run_embeddings_topic_kind ON run_embeddings (topic_id, kind);
CREATE INDEX IF NOT EXISTS idx_run_embeddings_embedding ON run_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- Store semantic embeddings for plan/sub-plan nodes to enable fine-grained recall
CREATE TABLE IF NOT EXISTS plan_step_embeddings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
  task_id TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'plan_step',
  embedding vector(1536) NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (run_id, task_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_plan_step_embeddings_topic_task ON plan_step_embeddings (topic_id, task_id);
CREATE INDEX IF NOT EXISTS idx_plan_step_embeddings_embedding ON plan_step_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
