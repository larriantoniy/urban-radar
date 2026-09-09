ALTER TABLE publications
    ADD COLUMN reconciled_at TIMESTAMPTZ,
    ADD COLUMN reconciled_by TEXT;
