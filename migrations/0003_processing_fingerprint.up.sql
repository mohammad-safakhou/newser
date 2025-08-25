ALTER TABLE processing_results
  ADD COLUMN IF NOT EXISTS fingerprint TEXT,
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;

-- backfill updated_at to created_at
UPDATE processing_results SET updated_at = COALESCE(updated_at, created_at);

