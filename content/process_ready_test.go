package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"urban-radar/runtime"
	"urban-radar/source"
)

type readyProcessStore struct {
	events []ReadyEvent
	drafts []Draft
	nextID int64
}

func (s *readyProcessStore) LoadReadyEvents(_ context.Context, refs []SourceRef) ([]ReadyEvent, error) {
	byRef := make(map[string]ReadyEvent, len(s.events))
	for _, event := range s.events {
		byRef[event.Item.Source+"/"+event.Item.SourceItemID] = event
	}
	out := make([]ReadyEvent, 0, len(refs))
	for _, ref := range refs {
		if event, ok := byRef[ref.Source+"/"+ref.SourceItemID]; ok {
			out = append(out, event)
		}
	}
	return out, nil
}
func (s *readyProcessStore) ListReadyEvents(context.Context) ([]ReadyEvent, error) {
	return append([]ReadyEvent(nil), s.events...), nil
}
func (s *readyProcessStore) FindContentDraft(_ context.Context, sourceName, sourceItemID, style, platform, input string) (Draft, bool, error) {
	for _, draft := range s.drafts {
		if draft.Source == sourceName && draft.SourceItemID == sourceItemID && draft.StyleVersion == style && draft.Platform == platform && draft.SourceInputHash == input {
			return draft, true, nil
		}
	}
	return Draft{}, false, nil
}
func (s *readyProcessStore) SaveContentDraft(_ context.Context, draft Draft) error {
	s.nextID++
	draft.ContentDraftID = s.nextID
	s.drafts = append(s.drafts, draft)
	return nil
}

type readyProcessAgent struct {
	calls []string
	fail  map[string]error
}

func (a *readyProcessAgent) GenerateContent(_ context.Context, request runtime.ContentRequest) (runtime.ContentOutcome, runtime.Usage, error) {
	key := request.Item.Source + "/" + request.Item.SourceItemID
	a.calls = append(a.calls, key)
	if err := a.fail[key]; err != nil {
		return runtime.ContentOutcome{}, runtime.Usage{}, err
	}
	return runtime.ContentOutcome{Output: readyDraftJSON(request.SourceLabel, request.Item.URL)}, runtime.Usage{APICalls: 1}, nil
}

type readyProcessNotifier struct {
	store *readyProcessStore
	calls []int64
	fail  map[int64]error
}

func (n *readyProcessNotifier) Notify(_ context.Context, id *int64) ([]ReviewNotificationResult, error) {
	for i := range n.store.drafts {
		draft := &n.store.drafts[i]
		if draft.ContentDraftID != *id || draft.HumanReviewStatus != ReviewStatusPending || draft.ReviewNotifiedAt != nil {
			continue
		}
		n.calls = append(n.calls, *id)
		if err := n.fail[*id]; err != nil {
			return []ReviewNotificationResult{{DraftID: *id, Status: "ERROR", Error: err.Error()}}, nil
		}
		now := time.Unix(1, 0)
		draft.ReviewNotifiedAt = &now
		return []ReviewNotificationResult{{DraftID: *id, Status: "SENT"}}, nil
	}
	return []ReviewNotificationResult{{DraftID: *id, Status: "SKIPPED"}}, nil
}

func readyEventFor(sourceName, id string, published time.Time, revision string) ReadyEvent {
	url := "https://example.test/" + sourceName + "/" + id
	return ReadyEvent{Item: source.SourceItem{Source: sourceName, SourceItemID: id, URL: url, Title: revision, Text: "facts", PublishedAt: published}, Discovery: runtime.StageResult{Output: json.RawMessage(`{"outcome":"CANDIDATE"}`)}, Editor: runtime.StageResult{PolicyID: "editor", InputHash: revision, Output: json.RawMessage(`{"decisions":[{"decision":"PUBLISH"}]}`)}}
}
func readyDraftJSON(label, url string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"schema_version":"content-draft-v1","style_version":"urban-radar-editorial-style-v1","platform":"vk","event_type":"OTHER","hook":"Hook","body":"Body","closing":"","source_label":%q,"source_url":%q,"post_text":%q,"fact_warnings":[],"human_review_required":true}`, label, url, "Hook\n\nBody\n\nИсточник: "+label+" — "+url))
}
func readyProcessor(store *readyProcessStore, agent *readyProcessAgent, notifier *readyProcessNotifier) ReadyProcessor {
	return ReadyProcessor{Store: store, Agent: agent, Notifier: notifier}
}

func TestProcessReadyCreatesReusesAndNotifiesIdempotently(t *testing.T) {
	event := readyEventFor("tgl", "1", time.Unix(1, 0), "v1")
	store := &readyProcessStore{events: []ReadyEvent{event}}
	agent := &readyProcessAgent{}
	notifier := &readyProcessNotifier{store: store, fail: map[int64]error{}}
	summary, err := readyProcessor(store, agent, notifier).Process(context.Background())
	if err != nil || summary.DraftsCreated != 1 || summary.NotificationsSent != 1 || summary.Usage.APICalls != 1 || len(agent.calls) != 1 {
		t.Fatalf("summary=%+v calls=%v err=%v", summary, agent.calls, err)
	}
	summary, err = readyProcessor(store, agent, notifier).Process(context.Background())
	if err != nil || summary.DraftsReused != 1 || summary.NotificationsSkipped != 1 || summary.Usage.APICalls != 0 || len(agent.calls) != 1 {
		t.Fatalf("replay=%+v calls=%v err=%v", summary, agent.calls, err)
	}
}

func TestProcessReadyChangedInputCreatesNewDraft(t *testing.T) {
	old := readyEventFor("tgl", "1", time.Unix(1, 0), "v1")
	store := &readyProcessStore{events: []ReadyEvent{old}}
	agent := &readyProcessAgent{}
	notifier := &readyProcessNotifier{store: store, fail: map[int64]error{}}
	_, _ = readyProcessor(store, agent, notifier).Process(context.Background())
	store.events = []ReadyEvent{readyEventFor("tgl", "1", time.Unix(1, 0), "v2")}
	summary, err := readyProcessor(store, agent, notifier).Process(context.Background())
	if err != nil || summary.DraftsCreated != 1 || len(store.drafts) != 2 || len(agent.calls) != 2 {
		t.Fatalf("summary=%+v drafts=%d calls=%v err=%v", summary, len(store.drafts), agent.calls, err)
	}
}

func TestProcessReadySkipsFinalizedDrafts(t *testing.T) {
	events := []ReadyEvent{readyEventFor("tgl", "approved", time.Time{}, "a"), readyEventFor("tgl", "rejected", time.Time{}, "r")}
	store := &readyProcessStore{events: events, drafts: []Draft{
		{ContentDraftID: 1, Source: "tgl", SourceItemID: "approved", StyleVersion: StyleVersion, Platform: PlatformVK, SourceInputHash: inputHash(events[0]), HumanReviewStatus: ReviewStatusApproved},
		{ContentDraftID: 2, Source: "tgl", SourceItemID: "rejected", StyleVersion: StyleVersion, Platform: PlatformVK, SourceInputHash: inputHash(events[1]), HumanReviewStatus: ReviewStatusRejected},
	}}
	agent := &readyProcessAgent{}
	notifier := &readyProcessNotifier{store: store, fail: map[int64]error{}}
	summary, err := readyProcessor(store, agent, notifier).Process(context.Background())
	if err != nil || summary.DraftsReused != 2 || len(agent.calls) != 0 || len(notifier.calls) != 0 {
		t.Fatalf("summary=%+v agent=%v notify=%v err=%v", summary, agent.calls, notifier.calls, err)
	}
}

func TestProcessReadyStableOrderAndContinuesAfterFailures(t *testing.T) {
	events := []ReadyEvent{readyEventFor("zakupki", "2", time.Unix(2, 0), "b"), readyEventFor("tgl", "1", time.Unix(1, 0), "a"), readyEventFor("tgl", "3", time.Unix(3, 0), "c")}
	sort.Slice(events, func(i, j int) bool { return events[i].Item.PublishedAt.Before(events[j].Item.PublishedAt) })
	store := &readyProcessStore{events: events}
	agent := &readyProcessAgent{fail: map[string]error{"tgl/1": errors.New("bad content")}}
	notifier := &readyProcessNotifier{store: store, fail: map[int64]error{2: errors.New("telegram down")}}
	summary, err := readyProcessor(store, agent, notifier).Process(context.Background())
	if err != nil || summary.DraftsCreated != 2 || len(summary.Errors) != 2 || len(store.drafts) != 2 {
		t.Fatalf("summary=%+v drafts=%d err=%v", summary, len(store.drafts), err)
	}
	if got := strings.Join(agent.calls, ","); got != "tgl/1,zakupki/2,tgl/3" {
		t.Fatalf("order=%s", got)
	}
}
