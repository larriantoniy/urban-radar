package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urban-radar/runtime"
)

type Experiment struct {
	Store    Store
	Agent    Agent
	Model    string
	Provider string
	Now      func() time.Time
}

func (e Experiment) RunV1(ctx context.Context) (Summary, error) {
	if e.Store == nil || e.Agent == nil {
		return Summary{}, fmt.Errorf("content experiment requires store and agent")
	}
	events, err := e.Store.LoadReadyEvents(ctx, ExperimentV1Refs)
	if err != nil {
		return Summary{}, err
	}
	if len(events) != len(ExperimentV1Refs) {
		return Summary{}, fmt.Errorf("content experiment requires %d READY_TO_PUBLISH events, got %d", len(ExperimentV1Refs), len(events))
	}
	byRef := make(map[string]ReadyEvent, len(events))
	for _, event := range events {
		byRef[event.Item.Source+"/"+event.Item.SourceItemID] = event
	}
	summary := Summary{SchemaVersion: SchemaVersion, StyleVersion: StyleVersion, Platform: PlatformVK}
	for _, ref := range ExperimentV1Refs {
		event, ok := byRef[ref.Source+"/"+ref.SourceItemID]
		if !ok {
			return Summary{}, fmt.Errorf("missing READY_TO_PUBLISH event %s/%s", ref.Source, ref.SourceItemID)
		}
		input := inputHash(event)
		if draft, found, err := e.Store.FindContentDraft(ctx, event.Item.Source, event.Item.SourceItemID, StyleVersion, PlatformVK, input); err != nil {
			return summary, err
		} else if found {
			summary.Usage.Add(draft.Usage)
			summary.Results = append(summary.Results, Result{Draft: draft})
			continue
		}
		result := e.generate(ctx, event, input)
		summary.Usage.Add(result.Draft.Usage)
		summary.Results = append(summary.Results, result)
		if result.Error != "" {
			return summary, fmt.Errorf("content draft %s/%s: %s", ref.Source, ref.SourceItemID, result.Error)
		}
	}
	return summary, nil
}

func (e Experiment) generate(ctx context.Context, event ReadyEvent, sourceInputHash string) Result {
	label := sourceLabel(event.Item.Source)
	request := runtime.ContentRequest{Item: event.Item, DiscoveryOutput: event.Discovery.Output, EditorOutput: event.Editor.Output, SourceLabel: label}
	outcome, usage, err := e.Agent.GenerateContent(ctx, request)
	if err != nil {
		return Result{Draft: Draft{Source: event.Item.Source, SourceItemID: event.Item.SourceItemID, Usage: usage}, Error: err.Error()}
	}
	draft, err := decodeDraft(outcome.Output)
	if err != nil {
		return Result{Draft: Draft{Source: event.Item.Source, SourceItemID: event.Item.SourceItemID, Usage: usage}, Error: err.Error()}
	}
	if draft.SourceURL != event.Item.URL || draft.SourceLabel != label || !draft.HumanReviewRequired {
		return Result{Draft: Draft{Source: event.Item.Source, SourceItemID: event.Item.SourceItemID, Usage: usage}, Error: "content output violates source or review contract"}
	}
	now := time.Now().UTC()
	if e.Now != nil {
		now = e.Now().UTC()
	}
	draft.Source, draft.SourceItemID = event.Item.Source, event.Item.SourceItemID
	draft.SourceInputHash = sourceInputHash
	draft.EditorPolicyID, draft.EditorInputHash = event.Editor.PolicyID, event.Editor.InputHash
	draft.Model, draft.Provider, draft.GeneratedAt, draft.Usage, draft.RawOutput = e.Model, e.Provider, now, usage, outcome.Output
	draft.HumanReviewStatus = "PENDING"
	if err := e.Store.SaveContentDraft(ctx, draft); err != nil {
		return Result{Draft: draft, Error: err.Error()}
	}
	return Result{Draft: draft}
}

func sourceLabel(sourceName string) string {
	switch sourceName {
	case "tgl":
		return "tgl.ru"
	case "zakupki":
		return "ЕИС Закупки"
	default:
		return sourceName
	}
}

func decodeDraft(raw json.RawMessage) (Draft, error) {
	var value Draft
	if err := json.Unmarshal(raw, &value); err != nil {
		return Draft{}, fmt.Errorf("Content schema: %w", err)
	}
	if value.SchemaVersion != SchemaVersion || value.StyleVersion != StyleVersion || value.Platform != PlatformVK || value.FactWarnings == nil || !value.HumanReviewRequired {
		return Draft{}, fmt.Errorf("Content schema: invalid draft")
	}
	for _, field := range []string{value.EventType, value.Hook, value.Body, value.SourceLabel, value.SourceURL, value.PostText} {
		if strings.TrimSpace(field) == "" {
			return Draft{}, fmt.Errorf("Content schema: missing required field")
		}
	}
	return value, nil
}

func inputHash(event ReadyEvent) string {
	payload, _ := json.Marshal(struct {
		Item      any             `json:"item"`
		Discovery json.RawMessage `json:"discovery"`
		Editor    json.RawMessage `json:"editor"`
	}{event.Item, event.Discovery.Output, event.Editor.Output})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
