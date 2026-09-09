ALTER TABLE publications
    DROP CONSTRAINT publications_status_check;

ALTER TABLE publications
    ADD CONSTRAINT publications_status_check
    CHECK (status IN ('PENDING','PUBLISHING','PUBLISHED','FAILED','RECOVERY_REQUIRED'));
