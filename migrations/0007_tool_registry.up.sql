CREATE TABLE IF NOT EXISTS tool_registry (
  name TEXT NOT NULL,
  version TEXT NOT NULL,
  description TEXT NOT NULL,
  agent_type TEXT NOT NULL,
  input_schema JSONB NOT NULL,
  output_schema JSONB NOT NULL,
  cost_estimate DOUBLE PRECISION DEFAULT 0,
  side_effects JSONB NOT NULL DEFAULT '[]'::jsonb,
  checksum TEXT NOT NULL,
  signature TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(name, version)
);
