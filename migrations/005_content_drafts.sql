-- Content Agent Experiment V1 stores human-reviewed drafts separately from
-- source_items and any future publisher state.
CREATE TABLE content_drafts (
    content_draft_id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    source_item_id TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    style_version TEXT NOT NULL,
    platform TEXT NOT NULL,
    event_type TEXT NOT NULL,
    hook TEXT NOT NULL,
    body TEXT NOT NULL,
    closing TEXT NOT NULL DEFAULT '',
    source_label TEXT NOT NULL,
    source_url TEXT NOT NULL,
    post_text TEXT NOT NULL,
    fact_warnings JSONB NOT NULL DEFAULT '[]'::jsonb,
    human_review_required BOOLEAN NOT NULL DEFAULT true,
    human_review_status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (human_review_status IN ('PENDING', 'APPROVED', 'REJECTED')),
    human_edited_text TEXT,
    source_input_hash TEXT NOT NULL,
    editor_policy_id TEXT NOT NULL,
    editor_input_hash TEXT NOT NULL,
    model TEXT NOT NULL,
    provider TEXT NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL,
    usage JSONB NOT NULL DEFAULT '{}'::jsonb,
    raw_output JSONB NOT NULL,
    FOREIGN KEY (source, source_item_id)
        REFERENCES source_items(source, source_item_id),
    UNIQUE (source, source_item_id, style_version, platform, source_input_hash)
);

CREATE INDEX content_drafts_review_idx
    ON content_drafts (human_review_status, generated_at DESC);
