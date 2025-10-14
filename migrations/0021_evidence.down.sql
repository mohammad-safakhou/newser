DROP INDEX IF EXISTS idx_run_evidence_run;
DROP TABLE IF EXISTS run_evidence;
ALTER TABLE processing_results DROP COLUMN IF EXISTS evidence;
