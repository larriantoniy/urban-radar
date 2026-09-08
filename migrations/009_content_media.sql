CREATE TABLE content_media (
    id BIGSERIAL PRIMARY KEY,
    content_draft_id BIGINT NOT NULL REFERENCES content_drafts(content_draft_id),
    storage_path TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    uploaded_at TIMESTAMPTZ NOT NULL,
    uploaded_by TEXT NOT NULL,
    deleted_at TIMESTAMPTZ,
    UNIQUE (content_draft_id)
);
