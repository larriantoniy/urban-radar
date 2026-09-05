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
