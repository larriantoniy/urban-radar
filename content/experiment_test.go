package content

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"urban-radar/runtime"
	"urban-radar/source"
)

type fakeStore struct {
	events []ReadyEvent
	drafts []Draft
}

func (s *fakeStore) LoadReadyEvents(_ context.Context, _ []SourceRef) ([]ReadyEvent, error) {
	return s.events, nil
}
func (s *fakeStore) SaveContentDraft(_ context.Context, draft Draft) error {
	s.drafts = append(s.drafts, draft)
	return nil
}
func (s *fakeStore) FindContentDraft(_ context.Context, sourceName, sourceItemID, style, platform, input string) (Draft, bool, error) {
	for _, draft := range s.drafts {
		if draft.Source == sourceName && draft.SourceItemID == sourceItemID && draft.StyleVersion == style && draft.Platform == platform && draft.SourceInputHash == input {
			return draft, true, nil
		}
	}
	return Draft{}, false, nil
}

type fakeAgent struct {
	output json.RawMessage
	calls  int
}

func TestExperimentReusesExistingPendingDraft(t *testing.T) {
	ref := ExperimentV1Refs[0]
	event := readyEvent(ref)
	store := &fakeStore{events: []ReadyEvent{event}}
	// The experiment requires all four fixtures; seed three existing drafts and
	// verify no agent call is spent for any already persisted draft.
	for _, item := range ExperimentV1Refs {
		ready := readyEvent(item)
		store.drafts = append(store.drafts, Draft{Source: item.Source, SourceItemID: item.SourceItemID, StyleVersion: StyleVersion, Platform: PlatformVK, SourceInputHash: inputHash(ready), HumanReviewRequired: true, HumanReviewStatus: "PENDING"})
	}
	store.events = make([]ReadyEvent, 0, len(ExperimentV1Refs))
	for _, item := range ExperimentV1Refs {
		store.events = append(store.events, readyEvent(item))
	}
	agent := &urlAgent{}
	summary, err := (Experiment{Store: store, Agent: agent}).RunV1(context.Background())
	if err != nil || agent.calls != 0 || len(summary.Results) != 4 {
		t.Fatalf("summary=%+v calls=%d err=%v", summary, agent.calls, err)
	}
}

func (a *fakeAgent) GenerateContent(_ context.Context, request runtime.ContentRequest) (runtime.ContentOutcome, runtime.Usage, error) {
	a.calls++
	if len(a.output) == 0 {
		return runtime.ContentOutcome{}, runtime.Usage{}, fmt.Errorf("malformed agent JSON")
	}
	return runtime.ContentOutcome{Output: a.output}, runtime.Usage{APICalls: 1}, nil
}

func TestExperimentPersistsHumanReviewedDraftWithoutMutatingEvent(t *testing.T) {
	events := make([]ReadyEvent, 0, len(ExperimentV1Refs))
	for _, ref := range ExperimentV1Refs {
		events = append(events, readyEvent(ref))
	}
	store := &fakeStore{events: events}
	agentWithURLs := &urlAgent{}
	summary, err := (Experiment{Store: store, Agent: agentWithURLs, Model: "model", Provider: "provider", Now: func() time.Time { return time.Unix(0, 0) }}).RunV1(context.Background())
	if err != nil || len(store.drafts) != 4 || agentWithURLs.calls != 4 || summary.Usage.APICalls != 4 {
		t.Fatalf("summary=%+v drafts=%d calls=%d err=%v", summary, len(store.drafts), agentWithURLs.calls, err)
	}
	for i, draft := range store.drafts {
		if !draft.HumanReviewRequired || draft.HumanReviewStatus != "PENDING" || draft.SourceURL != events[i].Item.URL || draft.RawOutput == nil {
			t.Fatalf("draft=%+v", draft)
		}
	}
}

func TestExperimentRunProcessesExplicitReadyItem(t *testing.T) {
	ref := SourceRef{Source: "tgl", SourceItemID: "batch-2"}
	event := readyEvent(ref)
	store := &fakeStore{events: []ReadyEvent{event}}
	agent := &urlAgent{}
	summary, err := (Experiment{Store: store, Agent: agent}).Run(context.Background(), []SourceRef{ref})
	if err != nil || agent.calls != 1 || len(summary.Results) != 1 || len(store.drafts) != 1 {
		t.Fatalf("summary=%+v calls=%d drafts=%d err=%v", summary, agent.calls, len(store.drafts), err)
	}
	if store.drafts[0].SourceItemID != "batch-2" || store.drafts[0].HumanReviewStatus != "PENDING" {
		t.Fatalf("draft=%+v", store.drafts[0])
	}
}

func TestExperimentRejectsMalformedOutputAndDoesNotPersist(t *testing.T) {
	events := make([]ReadyEvent, 0, len(ExperimentV1Refs))
	for _, ref := range ExperimentV1Refs {
		events = append(events, readyEvent(ref))
	}
	store := &fakeStore{events: events}
	summary, err := (Experiment{Store: store, Agent: &fakeAgent{output: json.RawMessage(`{"schema_version":"content-draft-v1"}`)}}).RunV1(context.Background())
	if err == nil || len(store.drafts) != 0 || len(summary.Results) != 1 {
		t.Fatalf("summary=%+v drafts=%d err=%v", summary, len(store.drafts), err)
	}
}

func readyEvent(ref SourceRef) ReadyEvent {
	url := "https://example.test/" + ref.Source + "/" + ref.SourceItemID
	return ReadyEvent{Item: source.SourceItem{Source: ref.Source, SourceItemID: ref.SourceItemID, URL: url, Title: "title", Text: "facts"}, Discovery: runtime.StageResult{Output: json.RawMessage(`{"outcome":"CANDIDATE"}`)}, Editor: runtime.StageResult{PolicyID: "editor-v1:test", InputHash: "input", Output: json.RawMessage(`{"decisions":[{"decision":"PUBLISH"}]}`)}}
}

type urlAgent struct{ calls int }

func (a *urlAgent) GenerateContent(_ context.Context, request runtime.ContentRequest) (runtime.ContentOutcome, runtime.Usage, error) {
	a.calls++
	return runtime.ContentOutcome{Output: draftJSON(request.SourceLabel, request.Item.URL)}, runtime.Usage{APICalls: 1}, nil
}

func draftJSON(label, url string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"schema_version":"content-draft-v1","style_version":"urban-radar-editorial-style-v1","platform":"vk","event_type":"OTHER","hook":"Hook","body":"Body","closing":"","source_label":%q,"source_url":%q,"post_text":%q,"fact_warnings":[],"human_review_required":true}`, label, url, "Hook\n\nBody\n\nИсточник: "+label+" — "+url))
}
