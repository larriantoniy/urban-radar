ALTER TABLE source_items
    ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS body_text TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS processing_state TEXT NOT NULL DEFAULT 'RECEIVED',
    ADD COLUMN IF NOT EXISTS retry_stage TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS research_rounds INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS runtime_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE source_items
SET first_seen_at = COALESCE(first_seen_at, retrieved_at),
    last_seen_at = COALESCE(last_seen_at, retrieved_at)
WHERE first_seen_at IS NULL OR last_seen_at IS NULL;

ALTER TABLE source_items
    ALTER COLUMN first_seen_at SET NOT NULL,
    ALTER COLUMN first_seen_at SET DEFAULT now(),
    ALTER COLUMN last_seen_at SET NOT NULL,
    ALTER COLUMN last_seen_at SET DEFAULT now();

ALTER TABLE source_items
    ADD CONSTRAINT source_items_research_rounds_v0
    CHECK (research_rounds BETWEEN 0 AND 1),
    ADD CONSTRAINT source_items_processing_state_v0
    CHECK (processing_state IN (
        'RECEIVED', 'DISCOVERY', 'EDITOR', 'RESEARCH', 'EDITOR_REEVALUATION',
        'DISCOVERY_DROPPED', 'IGNORED', 'READY_TO_PUBLISH', 'PROJECT_ACTION',
        'RESEARCH_EXHAUSTED', 'RESEARCH_UNSUPPORTED', 'ERROR'
    ));

CREATE INDEX IF NOT EXISTS source_items_runtime_processable_idx
    ON source_items (processing_state, published_at_source, source, source_item_id)
    WHERE processing_state IN ('RECEIVED', 'DISCOVERY', 'EDITOR', 'RESEARCH',
        'EDITOR_REEVALUATION', 'ERROR');

CREATE TABLE source_checkpoints (
    source TEXT PRIMARY KEY,
    last_successful_collection_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE news_check_runs (
    run_id TEXT PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'RUNNING'
        CHECK (status IN ('RUNNING', 'SUCCESS', 'PARTIAL', 'FAILED')),
    summary JSONB NOT NULL DEFAULT '{}'::jsonb
);
