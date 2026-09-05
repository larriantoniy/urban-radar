package storage

import (
	"os"
	"strings"
	"testing"

	urruntime "urban-radar/runtime"
)

func TestRuntimeMigrationProtectsCurrentInvariants(t *testing.T) {
	var sql string
	for _, name := range []string{"../migrations/001_source_items.sql", "../migrations/002_incremental_news_runtime.sql", "../migrations/003_selective_source_materialization.sql", "../migrations/004_news_check_run_lifecycle.sql"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sql += string(data)
	}
	for _, required := range []string{"source_checkpoints", "news_check_runs", "first_seen_at", "last_seen_at", "processing_state", "READY_TO_PUBLISH", "retry_stage", "runtime_data", "research_rounds BETWEEN 0 AND 1", "UNIQUE (source, source_item_id)", "source_fingerprint", "INTERRUPTED", "COMPLETED"} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration does not contain %q", required)
		}
	}
}

func TestSelectiveMaterializationQueriesPreserveDropCompaction(t *testing.T) {
	data, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, required := range []string{
		"CASE WHEN source_items.processing_state='DISCOVERY_DROPPED' THEN source_items.summary ELSE EXCLUDED.summary END",
		"CASE WHEN source_items.processing_state='DISCOVERY_DROPPED' THEN source_items.body_text ELSE EXCLUDED.body_text END",
		"summary=CASE WHEN $3='DISCOVERY_DROPPED' THEN '' ELSE summary END",
		"body_text=CASE WHEN $3='DISCOVERY_DROPPED' THEN '' ELSE body_text END",
		"metadata=CASE WHEN $3='DISCOVERY_DROPPED' THEN '{}'::jsonb ELSE metadata END",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("storage query missing selective materialization contract: %q", required)
		}
	}
}

func TestRuntimePayloadRoundTrip(t *testing.T) {
	record := urruntime.ItemRecord{State: urruntime.StateEditorReevaluation, ResearchRounds: 1, Research: &urruntime.StageResult{PolicyID: "research-v0.2"}, Usage: urruntime.Usage{APICalls: 3}}
	payload := runtimePayload(record)
	var restored urruntime.ItemRecord
	applyPayload(&restored, payload)
	if restored.Research == nil || restored.Research.PolicyID != "research-v0.2" || restored.Usage.APICalls != 3 {
		t.Fatalf("restored=%+v", restored)
	}
}
