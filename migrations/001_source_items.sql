CREATE TABLE IF NOT EXISTS source_items (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    source_item_id TEXT NOT NULL,
    source_url TEXT NOT NULL,
    title TEXT NOT NULL,
    published_at_source TIMESTAMPTZ,
    retrieved_at TIMESTAMPTZ NOT NULL,
    discovery_result TEXT NOT NULL DEFAULT '',
    editor_decision TEXT NOT NULL DEFAULT '',
    importance DOUBLE PRECISION,
    processed_at TIMESTAMPTZ,
    publication_status TEXT NOT NULL DEFAULT 'unpublished',
    published_at TIMESTAMPTZ,
    external_post_id TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT source_items_source_item_key UNIQUE (source, source_item_id)
);

CREATE INDEX IF NOT EXISTS source_items_processed_idx
    ON source_items (processed_at);
