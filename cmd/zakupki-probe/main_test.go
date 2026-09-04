package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"urban-radar/zakupki"
)

func TestWriteExportAndCaptureMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.json")
	p := zakupki.Procurement{ID: "1", URL: "https://zakupki.gov.ru/1", Object: "Ремонт дороги", Price: 1200, Currency: "RUB", PublishedAt: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)}
	items := []probeItem{{Procurement: p, SourceItem: p.SourceItem(time.Now().UTC())}}
	if err := writeExport(path, items); err != nil {
		t.Fatal(err)
	}
	if err := writeCaptureMetadata(path, 20, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := writeExportREADME(path); err != nil {
		t.Fatal(err)
	}
	var exported []exportItem
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &exported); err != nil || len(exported) != 1 {
		t.Fatalf("export: %v", err)
	}
	if exported[0].HumanLabel != nil {
		t.Fatal("export must initialize human_label as null")
	}
	for _, name := range []string{"capture.json", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateLimit(t *testing.T) {
	for _, limit := range []int{0, -1, 101} {
		if validateLimit(limit) == nil {
			t.Fatalf("limit %d should fail", limit)
		}
	}
	for _, limit := range []int{1, 20, 100} {
		if err := validateLimit(limit); err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}
