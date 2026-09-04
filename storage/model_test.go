package storage

import (
	"context"
	"os"
	"testing"
)

func TestDatabaseURLFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/urban_radar")
	got, err := DatabaseURLFromEnv()
	if err != nil || got == "" {
		t.Fatalf("got %q, %v", got, err)
	}
	t.Setenv("DATABASE_URL", "")
	if _, err := DatabaseURLFromEnv(); err == nil {
		t.Fatal("expected missing DATABASE_URL error")
	}
}

func TestMetadataJSON(t *testing.T) {
	data, err := metadataJSON(map[string]any{"source": "zakupki"})
	if err != nil || string(data) != `{"source":"zakupki"}` {
		t.Fatalf("metadata = %s, err=%v", data, err)
	}
}

func TestOpenPostgresDoesNotUseEmptyURL(t *testing.T) {
	if _, err := OpenPostgres(context.Background(), ""); err == nil {
		t.Fatal("expected empty URL error")
	}
	_ = os.Getenv("DATABASE_URL")
}
