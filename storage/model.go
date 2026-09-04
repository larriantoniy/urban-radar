// Package storage contains persistence ports and the PostgreSQL implementation
// used by the future production pipeline.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type ProcessedSourceItem struct {
	Source            string
	SourceItemID      string
	SourceURL         string
	Title             string
	PublishedAtSource *time.Time
	RetrievedAt       time.Time
	DiscoveryResult   string
	EditorDecision    string
	Importance        *float64
	ProcessedAt       *time.Time
	PublicationStatus string
	PublishedAt       *time.Time
	ExternalPostID    string
	Metadata          map[string]any
}

type Repository interface {
	Get(context.Context, string, string) (ProcessedSourceItem, error)
	Upsert(context.Context, ProcessedSourceItem) error
	Close() error
}

func DatabaseURLFromEnv() (string, error) {
	value := os.Getenv("DATABASE_URL")
	if value == "" {
		return "", fmt.Errorf("DATABASE_URL is not set")
	}
	return value, nil
}

func OpenPostgres(ctx context.Context, databaseURL string) (*sql.DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is empty")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return db, nil
}

func metadataJSON(metadata map[string]any) ([]byte, error) {
	if metadata == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(metadata)
}
