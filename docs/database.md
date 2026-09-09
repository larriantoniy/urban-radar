# PostgreSQL persistence

PostgreSQL is the required state store for the manual incremental news-check
runtime. It prevents repeated processing of the same source record across
checks. The shared `source_items` table is keyed by
`UNIQUE(source, source_item_id)`; source-specific values live in `metadata
JSONB` rather than separate tables.

Migration:

```text
migrations/001_source_items.sql
migrations/002_incremental_news_runtime.sql
migrations/003_selective_source_materialization.sql
migrations/004_news_check_run_lifecycle.sql
migrations/005_content_drafts.sql
migrations/006_content_draft_review_notes.sql
migrations/007_content_draft_persisted_review.sql
migrations/008_content_draft_review_notifications.sql
migrations/009_content_media.sql
migrations/010_content_draft_approved_payload_hash.sql
migrations/011_publications.sql
migrations/012_publications_recovery_required.sql
migrations/013_publications_reconciliation_audit.sql
migrations/014_publications_operator_recovery_audit.sql
```

The application-level persistence ports live beside `runtime/` and
`newscheck/`; `storage.PostgresStore` is their single pgx-backed
implementation. Configure a PostgreSQL instance with:

```sh
export DATABASE_URL='postgres://user:password@db-host:5432/urban_radar?sslmode=require'
```

Apply migrations in filename order with the normal PostgreSQL administration
tooling before the first run, for example:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/001_source_items.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/002_incremental_news_runtime.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/003_selective_source_materialization.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/004_news_check_run_lifecycle.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/005_content_drafts.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/006_content_draft_review_notes.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/007_content_draft_persisted_review.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/008_content_draft_review_notifications.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/009_content_media.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/010_content_draft_approved_payload_hash.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/011_publications.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/012_publications_recovery_required.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/013_publications_reconciliation_audit.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/014_publications_operator_recovery_audit.sql
```

`storage.OpenPostgres` owns connection setup (including a startup ping), and
the CLI closes the database during shutdown. There is deliberately no
in-memory fallback: starting `news check` without `DATABASE_URL`, a reachable
database, or the migrations fails clearly rather than silently losing dedup
state. Unit tests cover contracts and serialization without requiring an
external PostgreSQL instance.

Migration 002 activates three runtime concepts:

- `source_items`: first/last seen timestamps, current processing state,
  retry stage, at most one successful Research round, persisted stage outputs,
  policy hashes, usage and errors;
- `source_checkpoints`: the last successfully completed collection boundary
  independently for each source;
- `news_check_runs`: status and machine-readable aggregate summary.

Migration 001 contains historical prepared publication columns. Runtime V0
does not use them and never writes a fake publication record. Its terminal
editorial state is `READY_TO_PUBLISH`, which is distinct from a future
`PUBLISHED` state.

Migrations 005–007 add the separate `content_drafts` experiment table and its
minimal persisted review boundary. A draft starts as `PENDING` and may move
once to `APPROVED` or `REJECTED`. Approval records its actor, timestamp, and a
SHA-256 hash of the exact `post_text` bytes; future publication must verify the
current hash against that approved hash. Historical experiment verdicts without
this audit evidence are reset to fail-safe `PENDING` by migration 007 while
their optional notes remain available. This is not a publisher state and does
not call VK or mark a source item as published.

Migration 008 records only confirmed human-review notification delivery on the
same draft: timestamp, Telegram delivery channel, and external message ID.
`PENDING` remains a review state, not a delivery state. A later retry skips a
row with this evidence; a transport error leaves it eligible. The unavoidable
send-success/DB-write-failure window is at-least-once delivery, not an
exactly-once guarantee.
