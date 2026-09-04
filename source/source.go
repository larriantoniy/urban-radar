// Package source contains the source-level contract shared by all adapters.
package source

import "time"

// SourceItem is the normalized item passed from a source adapter to Discovery.
// SourceItemID is the stable identifier supplied by the source and is the
// deduplication key together with Source.
type SourceItem struct {
	Source       string         `json:"source"`
	SourceItemID string         `json:"source_item_id"`
	URL          string         `json:"url"`
	Title        string         `json:"title"`
	Summary      string         `json:"summary,omitempty"`
	Text         string         `json:"text,omitempty"`
	PublishedAt  time.Time      `json:"published_at,omitempty"`
	RetrievedAt  time.Time      `json:"retrieved_at"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}
