ALTER TABLE processing_results
  DROP COLUMN IF EXISTS fingerprint,
  DROP COLUMN IF EXISTS updated_at;

