CREATE TABLE IF NOT EXISTS plan_graphs (
  plan_id TEXT PRIMARY KEY,
  thought_id TEXT NOT NULL,
  version TEXT NOT NULL,
  confidence DOUBLE PRECISION,
  execution_order TEXT[] DEFAULT ARRAY[]::TEXT[],
  budget JSONB,
  estimates JSONB,
  plan_json JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_plan_graphs_thought_id ON plan_graphs (thought_id);
