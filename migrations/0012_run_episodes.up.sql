CREATE TABLE IF NOT EXISTS run_episodes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id UUID NOT NULL UNIQUE REFERENCES runs(id) ON DELETE CASCADE,
  topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  thought JSONB NOT NULL,
  plan_document JSONB,
  plan_raw JSONB,
  plan_prompt TEXT,
  result JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_run_episodes_topic_created ON run_episodes (topic_id, created_at DESC);

CREATE TABLE IF NOT EXISTS run_episode_steps (
  id BIGSERIAL PRIMARY KEY,
  episode_id UUID NOT NULL REFERENCES run_episodes(id) ON DELETE CASCADE,
  step_index INT NOT NULL,
  task JSONB NOT NULL,
  input_snapshot JSONB,
  prompt TEXT,
  result JSONB,
  artifacts JSONB,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_run_episode_steps_episode_idx ON run_episode_steps (episode_id, step_index);
