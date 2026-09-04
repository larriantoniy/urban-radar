package main

import (
	"encoding/json"
	"testing"
)

func TestSyncItemsUpdatesOnlyLabelsAndPreservesOrder(t *testing.T) {
	data := []byte(`[{"id":"a","object":"one","candidate":true,"human_label":null},{"id":"b","object":"two","candidate":false,"human_label":null}]`)
	review := []byte("## 01 — a\n\nhuman_label: `CANDIDATE`\n\n## 02 — b\n\nhuman_label: SKIP\n")
	out, stats, err := syncItems(data, review)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Candidate != 1 || stats.Skip != 1 || stats.Unlabeled != 0 {
		t.Fatalf("stats=%+v", stats)
	}
	var got []map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got[0]["id"] != "a" || got[0]["human_label"] != "CANDIDATE" || got[1]["human_label"] != "SKIP" {
		t.Fatalf("output=%s", out)
	}
}

func TestSyncItemsRejectsUnknownAndDuplicateReviewIDs(t *testing.T) {
	data := []byte(`[{"id":"a","human_label":null}]`)
	if _, _, err := syncItems(data, []byte("## 01 — x\nhuman_label: SKIP\n")); err == nil {
		t.Fatal("expected unknown ID error")
	}
	if _, _, err := syncItems(data, []byte("## 01 — a\nhuman_label: SKIP\n## 02 — a\nhuman_label: CANDIDATE\n")); err == nil {
		t.Fatal("expected duplicate ID error")
	}
}

func TestSyncItemsInvalidLabelRemainsUnlabeled(t *testing.T) {
	_, stats, err := syncItems([]byte(`[{"id":"a","human_label":null}]`), []byte("## 01 — a\nhuman_label: MAYBE\n"))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Found != 0 || stats.Unlabeled != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}
