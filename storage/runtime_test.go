package storage

import (
	"os"
	"strings"
	"testing"

	urruntime "urban-radar/runtime"
)

func TestRuntimeMigrationProtectsCurrentInvariants(t *testing.T) {
	var sql string
	for _, name := range []string{"../migrations/001_source_items.sql", "../migrations/002_incremental_news_runtime.sql"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sql += string(data)
	}
	for _, required := range []string{"source_checkpoints", "news_check_runs", "first_seen_at", "last_seen_at", "processing_state", "READY_TO_PUBLISH", "retry_stage", "runtime_data", "research_rounds BETWEEN 0 AND 1", "UNIQUE (source, source_item_id)"} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration does not contain %q", required)
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
