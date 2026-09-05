package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"urban-radar/source"
)

type Coordinator struct {
	Store                    Store
	Agents                   AgentExecutor
	Policies                 Policies
	ResearchSupportedSources map[string]bool
	Now                      func() time.Time
}

const researchGoal = "Уточнить факты, необходимые для оценки материала редактором."

func (c *Coordinator) Process(ctx context.Context, item source.SourceItem) ProcessResult {
	result := ProcessResult{Source: item.Source, SourceItemID: item.SourceItemID}
	var runUsage Usage
	record, err := c.Store.Get(ctx, item.Source, item.SourceItemID)
	if err != nil {
		result.State, result.Error = StateError, err.Error()
		return result
	}
	if record.State.Terminal() {
		result.State = record.State
		return result
	}
	if record.State == StateError {
		if record.RetryStage == "" {
			result.State, result.Error = StateError, "retryable item has no retry stage"
			return result
		}
		record.State, record.RetryStage, record.LastError = record.RetryStage, "", ""
	}
	c.reconcilePersistedStages(&record)
	for !record.State.Terminal() && record.State != StateError {
		stage := record.State
		if stage == StateReceived {
			stage = StateDiscovery
			record.State = stage
		}
		switch stage {
		case StateDiscovery:
			out, usage, callErr := c.Agents.Discover(ctx, record.Item)
			record.Usage.Add(usage)
			runUsage.Add(usage)
			if callErr != nil {
				c.fail(ctx, &record, stage, callErr)
				break
			}
			record.Discovery = stageResult(c.Policies.Discovery, semanticItem(record.Item), out.Output, usage, c.now())
			if out.Candidate {
				record.State = StateEditor
			} else {
				record.State = StateDiscoveryDropped
			}
			c.save(ctx, &record)
		case StateEditor:
			if record.Discovery == nil {
				c.fail(ctx, &record, stage, fmt.Errorf("missing persisted Discovery output"))
				break
			}
			out, usage, callErr := c.Agents.Edit(ctx, record.Item, record.Discovery.Output, nil)
			record.Usage.Add(usage)
			runUsage.Add(usage)
			if callErr != nil {
				c.fail(ctx, &record, stage, callErr)
				break
			}
			record.Editor = stageResult(c.Policies.Editor, record.Discovery.Output, out.Output, usage, c.now())
			record.State = initialEditorState(c.ResearchSupportedSources[record.Item.Source], out.Decision, record.ResearchRounds)
			if record.State == StateError {
				c.fail(ctx, &record, stage, fmt.Errorf("unsupported Editor decision %q", out.Decision))
				break
			}
			c.save(ctx, &record)
		case StateResearch:
			if record.ResearchRounds >= MaxResearchRounds {
				record.State = StateResearchExhausted
				c.save(ctx, &record)
				break
			}
			if record.Editor == nil {
				c.fail(ctx, &record, stage, fmt.Errorf("missing persisted Editor output"))
				break
			}
			var editor EditorOutcome
			if err := decodeEditorOutcome(record.Editor.Output, record.Item.URL, &editor); err != nil {
				c.fail(ctx, &record, stage, err)
				break
			}
			request := researchRequest(record.Item, editor.MissingInformation)
			out, usage, callErr := c.Agents.Research(ctx, request)
			record.Usage.Add(usage)
			runUsage.Add(usage)
			if callErr != nil {
				c.fail(ctx, &record, stage, callErr)
				break
			}
			record.Research = stageResult(c.Policies.Research, request, out.Output, usage, c.now())
			record.ResearchRounds++
			record.State = StateEditorReevaluation
			c.save(ctx, &record)
		case StateEditorReevaluation:
			if record.Discovery == nil || record.Research == nil || record.ResearchRounds != MaxResearchRounds {
				c.fail(ctx, &record, stage, fmt.Errorf("missing persisted Research context"))
				break
			}
			out, usage, callErr := c.Agents.Edit(ctx, record.Item, record.Discovery.Output, record.Research.Output)
			record.Usage.Add(usage)
			runUsage.Add(usage)
			if callErr != nil {
				c.fail(ctx, &record, stage, callErr)
				break
			}
			record.EditorReeval = stageResult(c.Policies.Editor, struct {
				Discovery json.RawMessage `json:"discovery"`
				Evidence  json.RawMessage `json:"evidence"`
			}{record.Discovery.Output, record.Research.Output}, out.Output, usage, c.now())
			if out.Decision == "RESEARCH" {
				record.State = StateResearchExhausted
			} else {
				record.State = decisionState(out.Decision)
				if record.State == StateError {
					c.fail(ctx, &record, stage, fmt.Errorf("unsupported Editor decision %q", out.Decision))
					break
				}
			}
			c.save(ctx, &record)
		default:
			c.fail(ctx, &record, stage, fmt.Errorf("unsupported runtime state %q", stage))
		}
	}
	result.State, result.Usage = record.State, runUsage
	if record.State == StateError {
		result.Error = record.LastError
	}
	return result
}

// reconcilePersistedStages reuses completed external work only while both its
// policy and semantic input still match. Terminal decisions remain immutable;
// deliberate terminal reprocessing is an explicit operator action, not an
// automatic consequence of a source field changing.
func (c *Coordinator) reconcilePersistedStages(record *ItemRecord) {
	if record.State.Terminal() {
		return
	}
	if record.Discovery != nil && !stageMatches(record.Discovery, c.Policies.Discovery, semanticItem(record.Item)) {
		record.Discovery, record.Editor, record.Research, record.EditorReeval = nil, nil, nil, nil
		record.ResearchRounds, record.State = 0, StateDiscovery
		return
	}
	if record.Editor != nil && (record.Discovery == nil || !stageMatches(record.Editor, c.Policies.Editor, record.Discovery.Output)) {
		record.Editor, record.Research, record.EditorReeval = nil, nil, nil
		record.ResearchRounds, record.State = 0, StateEditor
		return
	}
	if record.Research != nil {
		var editor EditorOutcome
		if record.Editor == nil || decodeEditorOutcome(record.Editor.Output, record.Item.URL, &editor) != nil ||
			!stageMatches(record.Research, c.Policies.Research, researchRequest(record.Item, editor.MissingInformation)) {
			record.Research, record.EditorReeval = nil, nil
			record.ResearchRounds, record.State = 0, StateResearch
		}
	}
}

func stageMatches(result *StageResult, policy string, input any) bool {
	return result != nil && result.PolicyID == policy && result.InputHash == hashJSON(input)
}

func researchRequest(item source.SourceItem, missing []string) ResearchRequest {
	return ResearchRequest{Source: item.Source, SourceItemID: item.SourceItemID, SourceURL: item.URL, ResearchGoal: researchGoal, MissingInformation: missing}
}

func initialEditorState(researchSupported bool, decision string, rounds int) State {
	if decision == "RESEARCH" {
		if !researchSupported {
			return StateResearchUnsupported
		}
		if rounds >= MaxResearchRounds {
			return StateResearchExhausted
		}
		return StateResearch
	}
	return decisionState(decision)
}

func decisionState(decision string) State {
	switch decision {
	case "PUBLISH":
		return StateReadyToPublish
	case "IGNORE":
		return StateIgnored
	case "UPDATE_PROJECT":
		return StateProjectAction
	default:
		return StateError
	}
}

func stageResult(policy string, input any, output json.RawMessage, usage Usage, at time.Time) *StageResult {
	return &StageResult{PolicyID: policy, InputHash: hashJSON(input), Output: append(json.RawMessage(nil), output...), Usage: usage, At: at}
}

func hashJSON(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func semanticItem(item source.SourceItem) any {
	return struct {
		Source       string         `json:"source"`
		SourceItemID string         `json:"source_item_id"`
		URL          string         `json:"url"`
		Title        string         `json:"title"`
		Summary      string         `json:"summary"`
		Text         string         `json:"text"`
		PublishedAt  time.Time      `json:"published_at"`
		Metadata     map[string]any `json:"metadata"`
	}{item.Source, item.SourceItemID, item.URL, item.Title, item.Summary, item.Text, item.PublishedAt, item.Metadata}
}

// SourceItemSemanticHash identifies the stable factual input supplied by a
// source adapter. RetrievedAt is deliberately excluded: seeing the same
// source record in an overlap window must not create a new semantic revision.
func SourceItemSemanticHash(item source.SourceItem) string {
	return hashJSON(semanticItem(item))
}

func (c *Coordinator) fail(ctx context.Context, record *ItemRecord, retry State, err error) {
	record.State, record.RetryStage, record.LastError = StateError, retry, err.Error()
	c.save(ctx, record)
}

func (c *Coordinator) save(ctx context.Context, record *ItemRecord) {
	record.UpdatedAt = c.now()
	if err := c.Store.Save(ctx, *record); err != nil {
		record.State, record.RetryStage, record.LastError = StateError, record.State, err.Error()
	}
}

func (c *Coordinator) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// decodeEditorOutcome recovers the information needed to construct a Research
// request from the exact persisted Editor JSON.
func decodeEditorOutcome(data json.RawMessage, sourceURL string, out *EditorOutcome) error {
	var envelope struct {
		Decisions []struct {
			SourceURL          string   `json:"source_url"`
			Decision           string   `json:"decision"`
			Importance         float64  `json:"importance"`
			Reason             string   `json:"reason"`
			MissingInformation []string `json:"missing_information"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode persisted Editor output: %w", err)
	}
	for _, decision := range envelope.Decisions {
		if decision.SourceURL == sourceURL {
			*out = EditorOutcome{Decision: decision.Decision, Importance: decision.Importance, Reason: decision.Reason, MissingInformation: decision.MissingInformation, Output: data}
			return nil
		}
	}
	return fmt.Errorf("persisted Editor output has no decision for source URL")
}
