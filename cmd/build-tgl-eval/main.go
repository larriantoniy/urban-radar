// Command build-tgl-eval creates the initial manually labelled tgl.ru dataset.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"urban-radar/tgl"
)

const outputPath = "data/evals/tgl-v0/items.json"

var sourceID = regexp.MustCompile(`/news/item/([0-9]+)-`)

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
	client := tgl.NewClient(15 * time.Second)
	news, err := client.ListNewsWithOptions(context.Background(), tgl.ListOptions{Limit: 100})
	if err != nil {
		fail(err)
	}
	items := make([]item, 0, len(news))
	for _, entry := range news {
		matches := sourceID.FindStringSubmatch(entry.URL)
		if len(matches) != 2 {
			fail(fmt.Errorf("cannot derive source item ID from %q", entry.URL))
		}
		items = append(items, item{
			ID: "tgl-" + matches[1], Title: entry.Title, PublishedAt: entry.PublishedAt,
			URL: entry.URL, Summary: entry.Summary,
		})
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fail(err)
	}
	file, err := os.Create(outputPath)
	if err != nil {
		fail(err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(items); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %d items to %s\n", len(items), outputPath)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "build-tgl-eval:", err)
	os.Exit(1)
}
