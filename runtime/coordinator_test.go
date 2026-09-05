package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"urban-radar/source"
)

type memoryStore struct{ record ItemRecord }

func (s *memoryStore) Get(context.Context, string, string) (ItemRecord, error) { return s.record, nil }
func (s *memoryStore) Save(_ context.Context, record ItemRecord) error {
	// Mirror the durable store contract: a successful terminal Discovery DROP
	// keeps identity and compact Discovery provenance, not the heavy payload.
	if record.State == StateDiscoveryDropped {
		record.Item.Summary = ""
		record.Item.Text = ""
		record.Item.Metadata = map[string]any{}
	}
	s.record = record
	return nil
}

type fakeAgents struct {
	discoveryCandidate bool
	discoveryErr       error
	editorDecisions    []string
	editorErr          error
	researchErr        error
	discoveryCalls     int
	editorCalls        int
	researchCalls      int
	editorItems        []source.SourceItem
}

func (f *fakeAgents) Discover(_ context.Context, item source.SourceItem) (DiscoveryOutcome, Usage, error) {
	f.discoveryCalls++
	if f.discoveryErr != nil {
		return DiscoveryOutcome{}, Usage{APICalls: 1}, f.discoveryErr
	}
	out := map[string]any{"candidates": []any{}}
	if f.discoveryCandidate {
		out["candidates"] = []any{map[string]any{"source_url": item.URL, "title": item.Title}}
	}
	b, _ := json.Marshal(out)
	return DiscoveryOutcome{Candidate: f.discoveryCandidate, Output: b}, Usage{APICalls: 1}, nil
}

func (f *fakeAgents) Edit(_ context.Context, item source.SourceItem, _ json.RawMessage, _ json.RawMessage) (EditorOutcome, Usage, error) {
	f.editorCalls++
	f.editorItems = append(f.editorItems, item)
	if f.editorErr != nil {
		return EditorOutcome{}, Usage{APICalls: 1}, f.editorErr
	}
	decision := f.editorDecisions[0]
	f.editorDecisions = f.editorDecisions[1:]
	missing := []string(nil)
	if decision == "RESEARCH" {
		missing = []string{"адрес"}
	}
	b, _ := json.Marshal(map[string]any{"decisions": []any{map[string]any{"source_url": item.URL, "decision": decision, "importance": 0.5, "reason": "test", "missing_information": missing}}})
	return EditorOutcome{Decision: decision, Importance: 0.5, Reason: "test", MissingInformation: missing, Output: b}, Usage{APICalls: 1}, nil
}

func (f *fakeAgents) Research(context.Context, ResearchRequest) (ResearchOutcome, Usage, error) {
	f.researchCalls++
	if f.researchErr != nil {
		return ResearchOutcome{}, Usage{APICalls: 1}, f.researchErr
	}
	return ResearchOutcome{Output: json.RawMessage(`{"source_item_id":"1","status":"COMPLETE","findings":[],"unresolved":[],"sources":[]}`)}, Usage{APICalls: 1}, nil
}

func testRecord(sourceName string) ItemRecord {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	return ItemRecord{Item: source.SourceItem{Source: sourceName, SourceItemID: "1", URL: "https://example.test/1", Title: "item", PublishedAt: now}, State: StateReceived, FirstSeenAt: now, LastSeenAt: now}
}

func coordinator(store *memoryStore, agents *fakeAgents) *Coordinator {
	return &Coordinator{Store: store, Agents: agents, Policies: Policies{Discovery: "discovery", Editor: "editor-v1", Research: "research-v0.2"}, ResearchSupportedSources: map[string]bool{"zakupki": true}, Now: func() time.Time { return time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC) }}
}

func TestCoordinatorTerminalTransitions(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		candidate  bool
		decisions  []string
		want       State
		wantEditor int
		wantSearch int
	}{
		{"discovery drop", "tgl", false, nil, StateDiscoveryDropped, 0, 0},
		{"editor ignore", "tgl", true, []string{"IGNORE"}, StateIgnored, 1, 0},
		{"editor publish", "tgl", true, []string{"PUBLISH"}, StateReadyToPublish, 1, 0},
		{"editor project", "tgl", true, []string{"UPDATE_PROJECT"}, StateProjectAction, 1, 0},
		{"unsupported TGL research", "tgl", true, []string{"RESEARCH"}, StateResearchUnsupported, 1, 0},
		{"research publish", "zakupki", true, []string{"RESEARCH", "PUBLISH"}, StateReadyToPublish, 2, 1},
		{"research ignore", "zakupki", true, []string{"RESEARCH", "IGNORE"}, StateIgnored, 2, 1},
		{"research exhausted", "zakupki", true, []string{"RESEARCH", "RESEARCH"}, StateResearchExhausted, 2, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryStore{record: testRecord(tt.source)}
			agents := &fakeAgents{discoveryCandidate: tt.candidate, editorDecisions: append([]string(nil), tt.decisions...)}
			got := coordinator(store, agents).Process(context.Background(), store.record.Item)
			if got.State != tt.want || agents.editorCalls != tt.wantEditor || agents.researchCalls != tt.wantSearch {
				t.Fatalf("result=%+v editor=%d research=%d", got, agents.editorCalls, agents.researchCalls)
			}
			if store.record.ResearchRounds > MaxResearchRounds {
				t.Fatalf("research rounds=%d", store.record.ResearchRounds)
			}
		})
	}
}

func TestCoordinatorDiscoveryDropCompactsPayloadAndPreservesOutcome(t *testing.T) {
	record := testRecord("tgl")
	record.Item.Summary = "large summary"
	record.Item.Text = string(make([]byte, 16*1024))
	record.Item.Metadata = map[string]any{"large": string(make([]byte, 8*1024))}
	store := &memoryStore{record: record}
	agents := &fakeAgents{discoveryCandidate: false}
	got := coordinator(store, agents).Process(context.Background(), record.Item)
	if got.State != StateDiscoveryDropped || store.record.Discovery == nil {
		t.Fatalf("result=%+v record=%+v", got, store.record)
	}
	if store.record.Item.Text != "" || store.record.Item.Summary != "" || len(store.record.Item.Metadata) != 0 {
		t.Fatalf("dropped record retained heavy payload: %+v", store.record.Item)
	}
	if store.record.Item.Source != "tgl" || store.record.Item.SourceItemID != "1" || store.record.Item.URL == "" || store.record.Item.Title == "" {
		t.Fatalf("drop lost durable identity/audit fields: %+v", store.record.Item)
	}
}

func TestCoordinatorCandidateKeepsFullPayloadForEditorAndResume(t *testing.T) {
	record := testRecord("tgl")
	record.Item.Summary = "summary"
	record.Item.Text = "full body required by Editor"
	record.Item.Metadata = map[string]any{"kind": "official"}
	store := &memoryStore{record: record}
	agents := &fakeAgents{discoveryCandidate: true, editorDecisions: []string{"PUBLISH"}}
	got := coordinator(store, agents).Process(context.Background(), record.Item)
	if got.State != StateReadyToPublish || len(agents.editorItems) != 1 {
		t.Fatalf("result=%+v editor calls=%d", got, len(agents.editorItems))
	}
	if agents.editorItems[0].Text != "full body required by Editor" || store.record.Item.Text != "full body required by Editor" || store.record.Item.Metadata["kind"] != "official" {
		t.Fatalf("candidate payload was not retained: editor=%+v stored=%+v", agents.editorItems[0], store.record.Item)
	}
}

func TestCoordinatorResumesEditorFromPersistedCandidateWithoutSourceRefetch(t *testing.T) {
	record := testRecord("tgl")
	record.Item.Text = "persisted candidate body"
	record.Item.Metadata = map[string]any{"source": "persisted"}
	record.State = StateEditor
	record.Discovery = stageResult("discovery", semanticItem(record.Item), json.RawMessage(`{"candidates":[{}]}`), Usage{}, record.UpdatedAt)
	store := &memoryStore{record: record}
	agents := &fakeAgents{editorDecisions: []string{"PUBLISH"}}
	got := coordinator(store, agents).Process(context.Background(), record.Item)
	if got.State != StateReadyToPublish || agents.discoveryCalls != 0 || len(agents.editorItems) != 1 {
		t.Fatalf("result=%+v discovery=%d editor=%d", got, agents.discoveryCalls, len(agents.editorItems))
	}
	if agents.editorItems[0].Text != "persisted candidate body" || agents.editorItems[0].Metadata["source"] != "persisted" {
		t.Fatalf("resumed Editor lost candidate payload: %+v", agents.editorItems[0])
	}
}

func TestCoordinatorErrorsAreRetryableAndDoNotConsumeResearchRound(t *testing.T) {
	store := &memoryStore{record: testRecord("tgl")}
	agents := &fakeAgents{discoveryErr: errors.New("temporary")}
	got := coordinator(store, agents).Process(context.Background(), store.record.Item)
	if got.State != StateError || got.Usage.APICalls != 1 || store.record.Usage.APICalls != 1 || store.record.RetryStage != StateDiscovery || store.record.ResearchRounds != 0 {
		t.Fatalf("record=%+v", store.record)
	}
	if store.record.Item.SourceItemID != "1" || store.record.Item.URL == "" {
		t.Fatalf("discovery error lost retry payload: %+v", store.record.Item)
	}
	agents.discoveryErr = nil
	agents.discoveryCandidate = false
	got = coordinator(store, agents).Process(context.Background(), store.record.Item)
	if got.State != StateDiscoveryDropped || got.Usage.APICalls != 1 || store.record.Usage.APICalls != 2 || store.record.RetryStage != "" || agents.discoveryCalls != 2 {
		t.Fatalf("retry result=%+v calls=%d", got, agents.discoveryCalls)
	}

	store = &memoryStore{record: testRecord("zakupki")}
	agents = &fakeAgents{discoveryCandidate: true, editorDecisions: []string{"RESEARCH"}, researchErr: errors.New("tool unavailable")}
	got = coordinator(store, agents).Process(context.Background(), store.record.Item)
	if got.State != StateError || store.record.RetryStage != StateResearch || store.record.ResearchRounds != 0 {
		t.Fatalf("research failure=%+v", store.record)
	}
}

func TestCoordinatorDiscoveryTimeoutIsRetryableWithoutAutomaticRetry(t *testing.T) {
	store := &memoryStore{record: testRecord("tgl")}
	agents := &fakeAgents{discoveryErr: errors.New("Discovery invocation timeout after 90s")}
	got := coordinator(store, agents).Process(context.Background(), store.record.Item)
	if got.State != StateError || store.record.RetryStage != StateDiscovery || agents.discoveryCalls != 1 || store.record.ResearchRounds != 0 {
		t.Fatalf("result=%+v record=%+v calls=%d", got, store.record, agents.discoveryCalls)
	}
}

func TestCoordinatorResumesPersistedPendingDiscovery(t *testing.T) {
	record := testRecord("tgl")
	record.Item.Text = "persisted pending source payload"
	store := &memoryStore{record: record}
	agents := &fakeAgents{discoveryCandidate: false}
	got := coordinator(store, agents).Process(context.Background(), record.Item)
	if got.State != StateDiscoveryDropped || agents.discoveryCalls != 1 {
		t.Fatalf("result=%+v calls=%d", got, agents.discoveryCalls)
	}
}

func TestCoordinatorResumeReusesResearchAndTerminalWork(t *testing.T) {
	record := testRecord("zakupki")
	record.State = StateEditorReevaluation
	record.ResearchRounds = 1
	discovery := json.RawMessage(`{"candidates":[{"source_url":"https://example.test/1"}]}`)
	editor := json.RawMessage(`{"decisions":[{"source_url":"https://example.test/1","decision":"RESEARCH","importance":0.5,"reason":"test","missing_information":["адрес"]}]}`)
	record.Discovery = stageResult("discovery", semanticItem(record.Item), discovery, Usage{}, record.UpdatedAt)
	record.Editor = stageResult("editor-v1", discovery, editor, Usage{}, record.UpdatedAt)
	record.Research = stageResult("research-v0.2", researchRequest(record.Item, []string{"адрес"}), json.RawMessage(`{"status":"COMPLETE"}`), Usage{}, record.UpdatedAt)
	store := &memoryStore{record: record}
	agents := &fakeAgents{editorDecisions: []string{"PUBLISH"}}
	got := coordinator(store, agents).Process(context.Background(), record.Item)
	if got.State != StateReadyToPublish || agents.researchCalls != 0 || agents.editorCalls != 1 {
		t.Fatalf("resume=%+v calls=%+v", got, agents)
	}

	before := *agents
	got = coordinator(store, agents).Process(context.Background(), record.Item)
	if got.State != StateReadyToPublish || !reflect.DeepEqual(before, *agents) {
		t.Fatalf("terminal item invoked agents: before=%+v after=%+v", before, *agents)
	}
}

func TestCoordinatorDoesNotReuseMismatchedPersistedStage(t *testing.T) {
	record := testRecord("tgl")
	record.State = StateEditor
	record.Discovery = &StageResult{PolicyID: "old-policy", InputHash: "old-input", Output: json.RawMessage(`{"candidates":[]}`)}
	store := &memoryStore{record: record}
	agents := &fakeAgents{discoveryCandidate: false}
	got := coordinator(store, agents).Process(context.Background(), record.Item)
	if got.State != StateDiscoveryDropped || agents.discoveryCalls != 1 || agents.editorCalls != 0 {
		t.Fatalf("result=%+v calls=%+v", got, agents)
	}
}

func TestSemanticInputHashIgnoresRetrievedAtAndIsStable(t *testing.T) {
	a := testRecord("tgl").Item
	b := a
	b.RetrievedAt = time.Now()
	if SourceItemSemanticHash(a) != SourceItemSemanticHash(b) {
		t.Fatal("retrieved_at changed semantic input hash")
	}
}
