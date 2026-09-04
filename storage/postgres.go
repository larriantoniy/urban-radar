package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresRepository struct{ db *sql.DB }

func NewPostgresRepository(db *sql.DB) *PostgresRepository { return &PostgresRepository{db: db} }

func (r *PostgresRepository) Close() error { return r.db.Close() }

func (r *PostgresRepository) Get(ctx context.Context, source, sourceItemID string) (ProcessedSourceItem, error) {
	var item ProcessedSourceItem
	var metadata []byte
	row := r.db.QueryRowContext(ctx, `SELECT source, source_item_id, source_url, title,
published_at_source, retrieved_at, discovery_result, editor_decision, importance,
processed_at, publication_status, published_at, external_post_id, metadata
FROM source_items WHERE source = $1 AND source_item_id = $2`, source, sourceItemID)
	if err := row.Scan(&item.Source, &item.SourceItemID, &item.SourceURL, &item.Title,
		&item.PublishedAtSource, &item.RetrievedAt, &item.DiscoveryResult, &item.EditorDecision,
		&item.Importance, &item.ProcessedAt, &item.PublicationStatus, &item.PublishedAt,
		&item.ExternalPostID, &metadata); err != nil {
		return item, err
	}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return item, fmt.Errorf("decode metadata: %w", err)
		}
	}
	return item, nil
}

func (r *PostgresRepository) Upsert(ctx context.Context, item ProcessedSourceItem) error {
	metadata, err := metadataJSON(item.Metadata)
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO source_items
(source, source_item_id, source_url, title, published_at_source, retrieved_at,
 discovery_result, editor_decision, importance, processed_at, publication_status,
 published_at, external_post_id, metadata)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (source, source_item_id) DO UPDATE SET
source_url=EXCLUDED.source_url, title=EXCLUDED.title,
published_at_source=EXCLUDED.published_at_source, retrieved_at=EXCLUDED.retrieved_at,
discovery_result=EXCLUDED.discovery_result, editor_decision=EXCLUDED.editor_decision,
importance=EXCLUDED.importance, processed_at=EXCLUDED.processed_at,
publication_status=EXCLUDED.publication_status, published_at=EXCLUDED.published_at,
external_post_id=EXCLUDED.external_post_id, metadata=EXCLUDED.metadata`,
		item.Source, item.SourceItemID, item.SourceURL, item.Title, item.PublishedAtSource,
		item.RetrievedAt, item.DiscoveryResult, item.EditorDecision, item.Importance,
		item.ProcessedAt, item.PublicationStatus, item.PublishedAt, item.ExternalPostID, metadata)
	return err
}

func NullTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
