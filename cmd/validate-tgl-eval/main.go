// Command validate-tgl-eval validates the manually labelled tgl.ru dataset.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"
)

const inputPath = "data/evals/tgl-v0/items.json"

type item struct {
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	PublishedAt         string   `json:"published_at"`
	URL                 string   `json:"url"`
	Summary             string   `json:"summary"`
	HumanDiscovery      *bool    `json:"human_discovery"`
	HumanEditorDecision *string  `json:"human_editor_decision"`
	HumanImportance     *float64 `json:"human_importance"`
	HumanReason         *string  `json:"human_reason"`
}

func main() {
	file, err := os.Open(inputPath)
	if err != nil {
		fail(err)
	}
	defer file.Close()
	var items []item
	if err := json.NewDecoder(file).Decode(&items); err != nil {
		fail(err)
	}
	ids, urls := make(map[string]struct{}), make(map[string]struct{})
	// Human ground truth is binary at this evaluation boundary. Rich Editor
	// decisions are projected to PUBLISH or SKIP by the review dataset.
	decisions := map[string]bool{"PUBLISH": true, "SKIP": true}
	for index, entry := range items {
		prefix := fmt.Sprintf("item %d", index)
		if entry.ID == "" {
			fail(fmt.Errorf("%s: empty id", prefix))
		}
		if _, exists := ids[entry.ID]; exists {
			fail(fmt.Errorf("%s: duplicate id %q", prefix, entry.ID))
		}
		ids[entry.ID] = struct{}{}
		parsedURL, err := url.ParseRequestURI(entry.URL)
		if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
			fail(fmt.Errorf("%s: invalid url %q", prefix, entry.URL))
		}
		if _, exists := urls[entry.URL]; exists {
			fail(fmt.Errorf("%s: duplicate url %q", prefix, entry.URL))
		}
		urls[entry.URL] = struct{}{}
		if _, err := time.Parse("2006-01-02", entry.PublishedAt); err != nil {
			fail(fmt.Errorf("%s: invalid published_at %q", prefix, entry.PublishedAt))
		}
		if entry.HumanEditorDecision != nil && !decisions[*entry.HumanEditorDecision] {
			fail(fmt.Errorf("%s: invalid human_editor_decision %q", prefix, *entry.HumanEditorDecision))
		}
		if entry.HumanImportance != nil && (*entry.HumanImportance < 0 || *entry.HumanImportance > 1) {
			fail(fmt.Errorf("%s: human_importance must be within 0..1", prefix))
		}
	}
	fmt.Printf("valid: %d items\n", len(items))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "validate-tgl-eval:", err)
	os.Exit(1)
}
