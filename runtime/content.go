package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	ContentSchemaVersion = "content-draft-v1"
	ContentStyleVersion  = "urban-radar-editorial-style-v1"
	ContentPlatformVK    = "vk"
)

type contentDraftEnvelope struct {
	SchemaVersion       string   `json:"schema_version"`
	StyleVersion        string   `json:"style_version"`
	Platform            string   `json:"platform"`
	EventType           string   `json:"event_type"`
	Hook                string   `json:"hook"`
	Body                string   `json:"body"`
	Closing             string   `json:"closing"`
	CTA                 string   `json:"cta,omitempty"`
	SourceLabel         string   `json:"source_label"`
	SourceURL           string   `json:"source_url"`
	PostText            string   `json:"post_text"`
	FactsAsserted       []string `json:"facts_asserted,omitempty"`
	FactWarnings        []string `json:"fact_warnings"`
	HumanReviewRequired bool     `json:"human_review_required"`
}

func validateContentOutput(raw json.RawMessage, sourceLabel, sourceURL string) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var draft contentDraftEnvelope
	if err := decoder.Decode(&draft); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	if draft.SchemaVersion != ContentSchemaVersion || draft.StyleVersion != ContentStyleVersion || draft.Platform != ContentPlatformVK {
		return fmt.Errorf("unsupported content version or platform")
	}
	for name, value := range map[string]string{
		"event_type": draft.EventType, "hook": draft.Hook, "body": draft.Body,
		"source_label": draft.SourceLabel, "source_url": draft.SourceURL, "post_text": draft.PostText,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if draft.SourceLabel != sourceLabel || draft.SourceURL != sourceURL {
		return fmt.Errorf("source provenance does not match input")
	}
	if draft.FactWarnings == nil {
		return fmt.Errorf("fact_warnings is required")
	}
	if !draft.HumanReviewRequired {
		return fmt.Errorf("human_review_required must be true")
	}
	if !strings.Contains(draft.PostText, sourceURL) {
		return fmt.Errorf("post_text must retain source URL")
	}
	return nil
}
