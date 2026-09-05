-- Runtime V0 retains a lightweight revision marker after a terminal
-- Discovery DROP. The heavy source payload is compacted by storage.Save;
-- this marker records that a source record changed without reopening its
-- terminal editorial state automatically.
ALTER TABLE source_items
    ADD COLUMN IF NOT EXISTS source_fingerprint TEXT NOT NULL DEFAULT '';
