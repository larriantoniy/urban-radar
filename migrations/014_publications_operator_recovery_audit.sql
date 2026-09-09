ALTER TABLE publications
    ADD COLUMN recovery_required_at TIMESTAMPTZ,
    ADD COLUMN recovery_required_by TEXT;
