package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"urban-radar/newscheck"
	urruntime "urban-radar/runtime"
	"urban-radar/source"
)

// TestPostgresSelectiveMaterializationContract exercises the real store when
// an operator supplies an isolated local development database. It is skipped
// in ordinary unit-test runs and cleans up its one uniquely identified row.
func TestPostgresSelectiveMaterializationContract(t *testing.T) {
	url := os.Getenv("URBAN_RADAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URBAN_RADAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPostgresStore(db)
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	item := source.SourceItem{
		Source: "storage-test", SourceItemID: "selective-materialization-contract", URL: "https://example.test/item",
		Title: "compact me", Summary: "long summary", Text: "large source payload", PublishedAt: now, RetrievedAt: now,
		Metadata: map[string]any{"raw": "large metadata payload"},
	}
	defer db.ExecContext(ctx, `DELETE FROM source_items WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)

	record, inserted, err := store.UpsertSeen(ctx, item, now)
	if err != nil || !inserted {
		t.Fatalf("upsert inserted=%v err=%v", inserted, err)
	}
	record.State = urruntime.StateDiscoveryDropped
	record.Discovery = &urruntime.StageResult{PolicyID: "discovery", Output: []byte(`{"candidates":[]}`), At: now}
	record.UpdatedAt = now
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(ctx, item.Source, item.SourceItemID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != urruntime.StateDiscoveryDropped || stored.Discovery == nil || stored.Item.Summary != "" || stored.Item.Text != "" || len(stored.Item.Metadata) != 0 {
		t.Fatalf("drop was not compacted: %+v", stored)
	}
	if stored.Item.URL != item.URL || stored.Item.Title != item.Title || stored.SourceFingerprint == "" {
		t.Fatalf("drop lost compact audit/provenance: %+v", stored)
	}

	updated := item
	updated.Text = "source payload must not restore a terminal drop"
	updated.Metadata = map[string]any{"new": "payload"}
	_, inserted, err = store.UpsertSeen(ctx, updated, now.Add(time.Hour))
	if err != nil || inserted {
		t.Fatalf("overlap upsert inserted=%v err=%v", inserted, err)
	}
	stored, err = store.Get(ctx, item.Source, item.SourceItemID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Item.Summary != "" || stored.Item.Text != "" || len(stored.Item.Metadata) != 0 || !stored.LastSeenAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("overlap re-materialized terminal drop: %+v", stored)
	}
	if stored.SourceFingerprint != urruntime.SourceItemSemanticHash(updated) {
		t.Fatalf("overlap did not retain the latest source revision marker: %q", stored.SourceFingerprint)
	}
}

func TestPostgresStoreFinalizesNewsCheckLifecycle(t *testing.T) {
	url := os.Getenv("URBAN_RADAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URBAN_RADAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPostgresStore(db)
	started := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	runID := "storage-test-interrupted-lifecycle"
	defer db.ExecContext(ctx, `DELETE FROM news_check_runs WHERE run_id=$1`, runID)
	if err := store.StartNewsCheck(ctx, runID, started); err != nil {
		t.Fatal(err)
	}
	summary := newscheck.Summary{RunID: runID, Status: "PARTIAL", RunStatus: "INTERRUPTED", StartedAt: started, FinishedAt: started.Add(time.Second)}
	if err := store.FinishNewsCheck(ctx, summary); err != nil {
		t.Fatal(err)
	}
	var status string
	var finished time.Time
	if err := db.QueryRowContext(ctx, `SELECT status, finished_at FROM news_check_runs WHERE run_id=$1`, runID).Scan(&status, &finished); err != nil {
		t.Fatal(err)
	}
	if status != "INTERRUPTED" || !finished.Equal(summary.FinishedAt) {
		t.Fatalf("status=%q finished=%s", status, finished)
	}
}
