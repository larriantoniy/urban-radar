package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeDiscoveryContract(t *testing.T) {
	url := "https://example.test/item"
	candidate := `{"outcome":"CANDIDATE","candidate":{"title":"title","published_at":"2026-09-05","source_url":"` + url + `","event_type_guess":"repair","summary":"summary","why_potentially_relevant":"change","uncertainty":"LOW"}}`
	for _, tt := range []struct {
		name          string
		raw           string
		wantCandidate bool
		wantErr       string
	}{
		{"drop", `{"outcome":"DROP"}`, false, ""},
		{"candidate", candidate, true, ""},
		{"multiple candidates field rejected", `{"outcome":"DROP","candidates":[]}`, false, "unknown field \"candidates\""},
		{"candidate without object", `{"outcome":"CANDIDATE"}`, false, "CANDIDATE requires candidate"},
		{"drop with object", `{"outcome":"DROP","candidate":{}}`, false, "DROP must not contain candidate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeRuntimeDiscovery([]byte(tt.raw), url)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error=%v want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.wantCandidate {
				t.Fatalf("candidate=%v err=%v", got, err)
			}
		})
	}
}

func TestDiscoveryInvocationTimeoutIsDiagnosable(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "slow-hermes")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	executor := &HermesExecutor{Command: command, DiscoveryPrompt: []byte("test"), DiscoveryTimeout: 10 * time.Millisecond}
	_, _, err := executor.Discover(context.Background(), testRecord("tgl").Item)
	if err == nil || !strings.Contains(err.Error(), "Discovery invocation timeout") {
		t.Fatalf("error=%v", err)
	}
}

func TestContentOutputContractPreservesSourceAndHumanReview(t *testing.T) {
	url := "https://example.test/source"
	valid := []byte(`{"schema_version":"content-draft-v1","style_version":"urban-radar-editorial-style-v1","platform":"vk","event_type":"OTHER","hook":"Hook","body":"Body","closing":"","source_label":"Example","source_url":"https://example.test/source","post_text":"Hook\n\nИсточник: Example — https://example.test/source","fact_warnings":[],"human_review_required":true}`)
	if err := validateContentOutput(valid, "Example", url); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{
		[]byte(`{"schema_version":"content-draft-v1"}`),
		[]byte(`{"schema_version":"content-draft-v1","style_version":"urban-radar-editorial-style-v1","platform":"vk","event_type":"OTHER","hook":"Hook","body":"Body","closing":"","source_label":"Other","source_url":"https://bad.test","post_text":"text https://bad.test","fact_warnings":[],"human_review_required":true}`),
		[]byte(`{"schema_version":"content-draft-v1","style_version":"urban-radar-editorial-style-v1","platform":"vk","event_type":"OTHER","hook":"Hook","body":"Body","closing":"","source_label":"Example","source_url":"https://example.test/source","post_text":"text https://example.test/source","fact_warnings":[],"human_review_required":false}`),
	} {
		if err := validateContentOutput(raw, "Example", url); err == nil {
			t.Fatalf("expected invalid content output: %s", raw)
		}
	}
}
