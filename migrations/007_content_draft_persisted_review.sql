-- A historical Content Experiment review is not a publication authorization:
-- it has no actor, timestamp, or immutable-content binding. Reset such rows
-- to PENDING before adding the V0 audited review invariant. Existing notes
-- remain available as experiment history.
UPDATE content_drafts
SET human_review_status = 'PENDING'
WHERE human_review_status <> 'PENDING';

ALTER TABLE content_drafts
    DROP CONSTRAINT content_drafts_human_review_status_check;

ALTER TABLE content_drafts
    ADD COLUMN approved_at TIMESTAMPTZ,
    ADD COLUMN approved_by TEXT,
    ADD COLUMN approved_content_hash TEXT,
    ADD COLUMN rejected_at TIMESTAMPTZ,
    ADD COLUMN rejected_by TEXT;

ALTER TABLE content_drafts
    ADD CONSTRAINT content_drafts_human_review_status_check
    CHECK (human_review_status IN ('PENDING', 'APPROVED', 'REJECTED')),
    ADD CONSTRAINT content_drafts_review_audit_check
    CHECK (
        (human_review_status = 'PENDING'
            AND approved_at IS NULL
            AND approved_by IS NULL
            AND approved_content_hash IS NULL
            AND rejected_at IS NULL
            AND rejected_by IS NULL)
        OR
        (human_review_status = 'APPROVED'
            AND approved_at IS NOT NULL
            AND approved_by IS NOT NULL
            AND approved_content_hash IS NOT NULL
            AND rejected_at IS NULL
            AND rejected_by IS NULL)
        OR
        (human_review_status = 'REJECTED'
            AND approved_at IS NULL
            AND approved_by IS NULL
            AND approved_content_hash IS NULL
            AND rejected_at IS NOT NULL
            AND rejected_by IS NOT NULL)
    );
