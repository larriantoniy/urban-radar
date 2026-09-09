CREATE TABLE publications (
    publication_id BIGSERIAL PRIMARY KEY,
    content_draft_id BIGINT NOT NULL REFERENCES content_drafts(content_draft_id),
    platform TEXT NOT NULL CHECK (platform = 'VK'),
    status TEXT NOT NULL CHECK (status IN ('PENDING','PUBLISHING','PUBLISHED','FAILED')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error TEXT,
    external_post_id TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (content_draft_id, platform),
    UNIQUE (platform, external_post_id),
    CHECK ((status = 'PUBLISHED') = (external_post_id IS NOT NULL AND published_at IS NOT NULL))
);
