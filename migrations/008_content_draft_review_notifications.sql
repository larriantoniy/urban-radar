-- Review transport delivery is distinct from the human review state. A draft
-- remains PENDING until an authenticated reviewer explicitly approves or
-- rejects it.
ALTER TABLE content_drafts
    ADD COLUMN review_notified_at TIMESTAMPTZ,
    ADD COLUMN review_notification_channel TEXT,
    ADD COLUMN review_notification_external_id TEXT,
    ADD CONSTRAINT content_drafts_review_notification_audit_check
    CHECK (
        (review_notified_at IS NULL
            AND review_notification_channel IS NULL
            AND review_notification_external_id IS NULL)
        OR
        (review_notified_at IS NOT NULL
            AND review_notification_channel IS NOT NULL
            AND review_notification_external_id IS NOT NULL)
    );
