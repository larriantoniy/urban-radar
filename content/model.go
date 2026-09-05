// Package content contains the human-reviewed Content Agent experiment layer.
// It is intentionally separate from runtime publication state.
package content

import (
	"context"
	"encoding/json"
	"time"

	"urban-radar/runtime"
	"urban-radar/source"
)

const (
	SchemaVersion = runtime.ContentSchemaVersion
	StyleVersion  = runtime.ContentStyleVersion
	PlatformVK    = runtime.ContentPlatformVK
)

type SourceRef struct {
	Source       string `json:"source"`
	SourceItemID string `json:"source_item_id"`
}

var ExperimentV1Refs = []SourceRef{
	{Source: "tgl", SourceItemID: "25864"},
	{Source: "tgl", SourceItemID: "25865"},
	{Source: "tgl", SourceItemID: "25868"},
	{Source: "zakupki", SourceItemID: "0142200001326017126"},
}

type ReadyEvent struct {
	Item      source.SourceItem
	Discovery runtime.StageResult
	Editor    runtime.StageResult
}

type Draft struct {
	Source              string          `json:"source"`
	SourceItemID        string          `json:"source_item_id"`
	SchemaVersion       string          `json:"schema_version"`
	StyleVersion        string          `json:"style_version"`
	Platform            string          `json:"platform"`
	EventType           string          `json:"event_type"`
	Hook                string          `json:"hook"`
	Body                string          `json:"body"`
	Closing             string          `json:"closing"`
	CTA                 string          `json:"cta,omitempty"`
	SourceLabel         string          `json:"source_label"`
	SourceURL           string          `json:"source_url"`
	PostText            string          `json:"post_text"`
	FactsAsserted       []string        `json:"facts_asserted,omitempty"`
	FactWarnings        []string        `json:"fact_warnings"`
	HumanReviewRequired bool            `json:"human_review_required"`
	HumanReviewStatus   string          `json:"human_review_status"`
	SourceInputHash     string          `json:"source_input_hash"`
	EditorPolicyID      string          `json:"editor_policy_id"`
	EditorInputHash     string          `json:"editor_input_hash"`
	Model               string          `json:"model"`
	Provider            string          `json:"provider"`
	GeneratedAt         time.Time       `json:"generated_at"`
	Usage               runtime.Usage   `json:"usage"`
	RawOutput           json.RawMessage `json:"raw_output"`
}

type Agent interface {
	GenerateContent(context.Context, runtime.ContentRequest) (runtime.ContentOutcome, runtime.Usage, error)
}

type Store interface {
	LoadReadyEvents(context.Context, []SourceRef) ([]ReadyEvent, error)
	FindContentDraft(context.Context, string, string, string, string, string) (Draft, bool, error)
	SaveContentDraft(context.Context, Draft) error
}

type Result struct {
	Draft Draft  `json:"draft"`
	Error string `json:"error,omitempty"`
}

type Summary struct {
	SchemaVersion string        `json:"schema_version"`
	StyleVersion  string        `json:"style_version"`
	Platform      string        `json:"platform"`
	Results       []Result      `json:"results"`
	Usage         runtime.Usage `json:"usage"`
}
