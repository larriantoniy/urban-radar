# PostgreSQL persistence

PostgreSQL will prevent repeated processing and duplicate publication across
sources (`tgl`, `zakupki`, and future adapters). The single shared
`source_items` table is keyed by `UNIQUE(source, source_item_id)`; procurement
specific values live in `metadata JSONB` rather than source-specific tables.

Migration:

```text
migrations/001_source_items.sql
```

The Go repository is in `storage/` and exposes a small `Repository` port plus
the pgx-backed implementation. Configure a VPS with:

```sh
export DATABASE_URL='postgres://user:password@db-host:5432/urban_radar?sslmode=require'
```

`storage.OpenPostgres` owns the connection lifecycle (ping on open and close on
failure); callers close the returned repository/database during shutdown. No
database is started or migrated locally by this milestone. Unit tests cover
configuration and serialization without requiring PostgreSQL.

The table stores source identity, source publication/retrieval timestamps,
Discovery and Editor decisions, importance, processing/publication state,
external post ID and optional metadata. Applying the migration on the VPS is a
deployment step for a future production run.
