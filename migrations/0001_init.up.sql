-- Enable pgcrypto for UUID generation
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Users table
CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Topics table
CREATE TABLE IF NOT EXISTS topics (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  preferences JSONB NOT NULL DEFAULT '{}'::jsonb,
  schedule_cron TEXT NOT NULL DEFAULT '@daily',
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Runs table
CREATE TABLE IF NOT EXISTS runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  topic_id UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
  status TEXT NOT NULL,
  started_at TIMESTAMPTZ DEFAULT NOW(),
  finished_at TIMESTAMPTZ,
  error TEXT
);

-- Processing results table (keyed by run_id or thought_id)
CREATE TABLE IF NOT EXISTS processing_results (
  id TEXT PRIMARY KEY,
  user_thought JSONB NOT NULL,
  summary TEXT,
  detailed_report TEXT,
  sources JSONB,
  highlights JSONB,
  conflicts JSONB,
  confidence DOUBLE PRECISION,
  processing_time BIGINT,
  cost_estimate DOUBLE PRECISION,
  tokens_used BIGINT,
  agents_used JSONB,
  llm_models_used JSONB,
  metadata JSONB,
  created_at TIMESTAMPTZ DEFAULT NOW()
);


