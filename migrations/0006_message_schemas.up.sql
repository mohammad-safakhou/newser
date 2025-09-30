CREATE TABLE IF NOT EXISTS message_schemas (
  event_type TEXT NOT NULL,
  version TEXT NOT NULL,
  schema JSONB NOT NULL,
  checksum TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (event_type, version)
);
