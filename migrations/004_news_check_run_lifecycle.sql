-- Runtime V0 distinguishes a completed batch from a controlled interruption.
-- Existing RUNNING and FAILED rows remain valid historical records.
ALTER TABLE news_check_runs
    DROP CONSTRAINT IF EXISTS news_check_runs_status_check;

ALTER TABLE news_check_runs
    ADD CONSTRAINT news_check_runs_status_check
    CHECK (status IN ('RUNNING', 'COMPLETED', 'INTERRUPTED', 'FAILED'));
