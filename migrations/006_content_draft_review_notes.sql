-- Content Experiment V1 needs an explicit human-reviewed-with-notes outcome
-- without conflating review notes with a future human-edited publication text.
ALTER TABLE content_drafts
    ADD COLUMN human_review_notes TEXT;

ALTER TABLE content_drafts
    DROP CONSTRAINT content_drafts_human_review_status_check;

ALTER TABLE content_drafts
    ADD CONSTRAINT content_drafts_human_review_status_check
    CHECK (human_review_status IN ('PENDING', 'APPROVED', 'APPROVED_WITH_NOTES', 'REJECTED'));
