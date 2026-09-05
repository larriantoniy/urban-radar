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
