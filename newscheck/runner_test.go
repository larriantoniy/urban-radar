package newscheck

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	urruntime "urban-radar/runtime"
	"urban-radar/source"
)

type fakeStore struct {
	items       map[string]urruntime.ItemRecord
	checkpoints map[string]time.Time
	advanced    map[string]int
	locked      bool
	summaries   []Summary
	upserts     int
	starts      int
	finishes    int
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: map[string]urruntime.ItemRecord{}, checkpoints: map[string]time.Time{}, advanced: map[string]int{}}
}
func key(item source.SourceItem) string { return item.Source + "/" + item.SourceItemID }
func (s *fakeStore) AcquireNewsCheck(context.Context) (func(context.Context) error, error) {
	if s.locked {
		return nil, ErrAlreadyRunning
	}
	s.locked = true
	return func(context.Context) error { s.locked = false; return nil }, nil
}
func (s *fakeStore) StartNewsCheck(context.Context, string, time.Time) error { s.starts++; return nil }
func (s *fakeStore) FinishNewsCheck(_ context.Context, summary Summary) error {
	s.finishes++
	s.summaries = append(s.summaries, summary)
	return nil
}
func (s *fakeStore) GetCheckpoint(_ context.Context, name string) (time.Time, bool, error) {
	v, ok := s.checkpoints[name]
	return v, ok, nil
}
func (s *fakeStore) AdvanceCheckpoint(_ context.Context, name string, value time.Time) error {
	s.checkpoints[name], s.advanced[name] = value, s.advanced[name]+1
	return nil
}
func (s *fakeStore) Get(_ context.Context, sourceName, sourceItemID string) (urruntime.ItemRecord, error) {
	record, ok := s.items[sourceName+"/"+sourceItemID]
	if !ok {
		return urruntime.ItemRecord{}, urruntime.ErrNotFound
	}
	return record, nil
}
func (s *fakeStore) UpsertSeen(_ context.Context, item source.SourceItem, now time.Time) (urruntime.ItemRecord, bool, error) {
	s.upserts++
	k := key(item)
	record, exists := s.items[k]
	if !exists {
		record = urruntime.ItemRecord{Item: item, State: urruntime.StateReceived, FirstSeenAt: now}
	}
	if record.State == urruntime.StateDiscoveryDropped {
		// Match PostgresStore: overlap observations refresh cheap audit fields
		// but never re-materialize a terminal dropped payload.
		record.Item.URL, record.Item.Title, record.Item.PublishedAt, record.Item.RetrievedAt = item.URL, item.Title, item.PublishedAt, item.RetrievedAt
	} else {
		record.Item = item
	}
	record.LastSeenAt = now
	s.items[k] = record
	return record, !exists, nil
}

func TestKnownDiscoveryDropOverlapDoesNotRematerializeOrInvokeProcessor(t *testing.T) {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := newFakeStore()
	a := item("tgl", "dropped", now)
	store.items[key(a)] = urruntime.ItemRecord{
		Item:  source.SourceItem{Source: a.Source, SourceItemID: a.SourceItemID, URL: a.URL, Title: a.Title, PublishedAt: a.PublishedAt},
		State: urruntime.StateDiscoveryDropped, FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Hour),
	}
	seenAgain := a
	seenAgain.Summary, seenAgain.Text = "large summary", "large payload"
	seenAgain.Metadata = map[string]any{"large": "payload"}
	collector := &fakeCollector{name: "tgl", collection: Collection{Items: []source.SourceItem{seenAgain}, Complete: true}}
	processor := &fakeProcessor{}
	runner := Runner{Store: store, Processor: processor, Collectors: []Collector{collector}, Now: func() time.Time { return now }}
	if _, err := runner.CheckNews(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := store.items[key(a)]
	if got.Item.Text != "" || got.Item.Summary != "" || len(got.Item.Metadata) != 0 {
		t.Fatalf("known DROP was re-materialized: %+v", got.Item)
	}
	if !got.LastSeenAt.Equal(now) || len(processor.calls) != 0 {
		t.Fatalf("last_seen=%s processor calls=%v", got.LastSeenAt, processor.calls)
	}
}

func TestDeferredReceivedItemKeepsPayloadWithoutSourceRefetch(t *testing.T) {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := newFakeStore()
	deferred := item("tgl", "deferred", now.Add(-time.Hour))
	deferred.Text = "full payload retained while discovery is pending"
	store.items[key(deferred)] = urruntime.ItemRecord{Item: deferred, State: urruntime.StateReceived, FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Hour)}
	collector := &fakeCollector{name: "tgl", collection: Collection{Complete: true}}
	processor := &fakeProcessor{}
	runner := Runner{Store: store, Processor: processor, Collectors: []Collector{collector}, Now: func() time.Time { return now }}
	if _, err := runner.CheckNews(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(processor.calls, []string{key(deferred)}) || store.items[key(deferred)].Item.Text != deferred.Text {
		t.Fatalf("deferred payload/calls=%+v %q", processor.calls, store.items[key(deferred)].Item.Text)
	}
}
func (s *fakeStore) ListProcessable(context.Context) ([]urruntime.ItemRecord, error) {
	var out []urruntime.ItemRecord
	for _, item := range s.items {
		if !item.State.Terminal() {
			out = append(out, item)
		}
	}
	return out, nil
}

type fakeCollector struct {
	name       string
	collection Collection
	err        error
	windows    []Window
}

func (f *fakeCollector) Name() string { return f.name }
func (f *fakeCollector) Collect(_ context.Context, window Window) (Collection, error) {
	f.windows = append(f.windows, window)
	return f.collection, f.err
}

type fakeProcessor struct {
	calls   []string
	states  map[string]urruntime.State
	results map[string]urruntime.ProcessResult
}

func (p *fakeProcessor) Process(_ context.Context, item source.SourceItem) urruntime.ProcessResult {
	p.calls = append(p.calls, key(item))
	if result, ok := p.results[key(item)]; ok {
		return result
	}
	state := urruntime.StateReadyToPublish
	if configured, ok := p.states[key(item)]; ok {
		state = configured
	}
	return urruntime.ProcessResult{Source: item.Source, SourceItemID: item.SourceItemID, State: state, Usage: urruntime.Usage{APICalls: 1}}
}

func item(name, id string, published time.Time) source.SourceItem {
	return source.SourceItem{Source: name, SourceItemID: id, URL: "https://example.test/" + id, Title: id, PublishedAt: published, RetrievedAt: published}
}

func TestRunnerWindowsOverlapAndCheckpointIsolation(t *testing.T) {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := newFakeStore()
	tgl := &fakeCollector{name: "tgl", collection: Collection{Complete: true}}
	zk := &fakeCollector{name: "zakupki", collection: Collection{Complete: true}}
	runner := Runner{Store: store, Processor: &fakeProcessor{}, Collectors: []Collector{tgl, zk}, Now: func() time.Time { return now }}
	if _, err := runner.CheckNews(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := tgl.windows[0].From; !got.Equal(now.Add(-DefaultBootstrapLookback)) {
		t.Fatalf("bootstrap from=%s", got)
	}
	checkpoint := now.Add(24 * time.Hour)
	runner.Now = func() time.Time { return checkpoint }
	zk.err = errors.New("upstream")
	if summary, err := runner.CheckNews(context.Background()); err != nil || summary.Status != "PARTIAL" {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if got := tgl.windows[1].From; !got.Equal(now.Add(-DefaultOverlap)) {
		t.Fatalf("overlap from=%s", got)
	}
	if !store.checkpoints["tgl"].Equal(checkpoint) || !store.checkpoints["zakupki"].Equal(now) {
		t.Fatalf("checkpoints=%v", store.checkpoints)
	}
}

func TestKnownTerminalItemUsesZeroAgentCallsOnSecondRun(t *testing.T) {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := newFakeStore()
	a := item("tgl", "A", now)
	collector := &fakeCollector{name: "tgl", collection: Collection{Items: []source.SourceItem{a}, Complete: true}}
	processor := &fakeProcessor{}
	runner := Runner{Store: store, Processor: processor, Collectors: []Collector{collector}, Now: func() time.Time { return now }}
	if _, err := runner.CheckNews(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := store.items[key(a)]
	record.State = urruntime.StateReadyToPublish
	store.items[key(a)] = record
	processor.calls = nil
	if summary, err := runner.CheckNews(context.Background()); err != nil || summary.Pipeline.Processable != 0 || len(processor.calls) != 0 {
		t.Fatalf("summary=%+v calls=%v err=%v", summary, processor.calls, err)
	}
}

func TestRetryableAndPendingItemsSurviveCheckpointAdvance(t *testing.T) {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := newFakeStore()
	retry := item("tgl", "retry", now.Add(-72*time.Hour))
	store.items[key(retry)] = urruntime.ItemRecord{Item: retry, State: urruntime.StateError, RetryStage: urruntime.StateDiscovery}
	collector := &fakeCollector{name: "tgl", collection: Collection{Complete: true}}
	processor := &fakeProcessor{states: map[string]urruntime.State{key(retry): urruntime.StateIgnored}}
	runner := Runner{Store: store, Processor: processor, Collectors: []Collector{collector}, Now: func() time.Time { return now }}
	summary, err := runner.CheckNews(context.Background())
	if err != nil || summary.Sources["tgl"].Retryable != 1 || summary.Pipeline.Ignored != 1 || !summary.Sources["tgl"].CheckpointAdvanced {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestIncompleteCollectionDoesNotAdvanceAndHasNoItemLimit(t *testing.T) {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := newFakeStore()
	items := make([]source.SourceItem, 0, 125)
	for i := 0; i < 125; i++ {
		items = append(items, item("zakupki", fmt.Sprint(i), now))
	}
	collector := &fakeCollector{name: "zakupki", collection: Collection{Items: items, PagesRead: 2, Complete: false}}
	processor := &fakeProcessor{}
	runner := Runner{Store: store, Processor: processor, Collectors: []Collector{collector}, Now: func() time.Time { return now }}
	summary, err := runner.CheckNews(context.Background())
	if err != nil || summary.Status != "FAILED" || summary.Sources["zakupki"].CheckpointAdvanced || len(store.items) != 125 || len(processor.calls) != 125 {
		t.Fatalf("summary=%+v stored=%d calls=%d err=%v", summary, len(store.items), len(processor.calls), err)
	}
}

func TestProcessingOrderAndFailureIsolation(t *testing.T) {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := newFakeStore()
	items := []source.SourceItem{item("tgl", "B", now), item("zakupki", "C", now.Add(-time.Hour)), item("tgl", "A", now)}
	collector := &fakeCollector{name: "tgl", collection: Collection{Items: items, Complete: true}}
	processor := &fakeProcessor{results: map[string]urruntime.ProcessResult{key(items[0]): {State: urruntime.StateError, Error: "schema"}}}
	runner := Runner{Store: store, Processor: processor, Collectors: []Collector{collector}, Now: func() time.Time { return now }}
	summary, err := runner.CheckNews(context.Background())
	want := []string{"zakupki/C", "tgl/A", "tgl/B"}
	if err != nil || summary.Status != "PARTIAL" || summary.Pipeline.Errors != 1 || !reflect.DeepEqual(processor.calls, want) {
		t.Fatalf("summary=%+v calls=%v err=%v", summary, processor.calls, err)
	}
}

func TestConcurrentCheckRejected(t *testing.T) {
	store := newFakeStore()
	store.locked = true
	runner := Runner{Store: store, Processor: &fakeProcessor{}}
	if _, err := runner.CheckNews(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err=%v", err)
	}
}

func TestRunnerSourceFailureStatus(t *testing.T) {
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name      string
		tglErr    error
		zkErr     error
		want      string
		advancing int
	}{
		{"TGL failure is isolated", errors.New("tgl unavailable"), nil, "PARTIAL", 1},
		{"both sources fail", errors.New("tgl unavailable"), errors.New("EIS unavailable"), "FAILED", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			runner := Runner{
				Store: store, Processor: &fakeProcessor{}, Now: func() time.Time { return now },
				Collectors: []Collector{
					&fakeCollector{name: "tgl", collection: Collection{Complete: tt.tglErr == nil}, err: tt.tglErr},
					&fakeCollector{name: "zakupki", collection: Collection{Complete: tt.zkErr == nil}, err: tt.zkErr},
				},
			}
			summary, err := runner.CheckNews(context.Background())
			if err != nil || summary.Status != tt.want || store.advanced["tgl"]+store.advanced["zakupki"] != tt.advancing {
				t.Fatalf("summary=%+v advanced=%v err=%v", summary, store.advanced, err)
			}
		})
	}
}

func TestPreflightUsesRuntimeWindowsWithoutWritesOrAgentCalls(t *testing.T) {
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	store := newFakeStore()
	known := item("tgl", "known", now.Add(-time.Hour))
	store.items[key(known)] = urruntime.ItemRecord{Item: known, State: urruntime.StateReadyToPublish}
	retry := item("zakupki", "retry", now.Add(-72*time.Hour))
	store.items[key(retry)] = urruntime.ItemRecord{Item: retry, State: urruntime.StateError, RetryStage: urruntime.StateDiscovery}
	tgl := &fakeCollector{name: "tgl", collection: Collection{Items: []source.SourceItem{known, item("tgl", "new", now)}, PagesRead: 2, Complete: true}}
	zk := &fakeCollector{name: "zakupki", collection: Collection{Items: []source.SourceItem{item("zakupki", "new", now)}, PagesRead: 3, Complete: true}}
	processor := &fakeProcessor{}
	runner := Runner{Store: store, Processor: processor, Collectors: []Collector{tgl, zk}, Now: func() time.Time { return now }}
	summary, err := runner.Preflight(context.Background())
	if err != nil || summary.Status != "SUCCESS" || summary.Sources["tgl"].New != 1 || summary.Sources["tgl"].Known != 1 || summary.Sources["zakupki"].New != 1 || summary.Sources["zakupki"].Retryable != 1 || summary.Pipeline.Processable != 3 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if store.upserts != 0 || store.starts != 0 || store.finishes != 0 || len(processor.calls) != 0 || len(store.advanced) != 0 {
		t.Fatalf("preflight wrote or processed: upserts=%d starts=%d finishes=%d calls=%v checkpoints=%v", store.upserts, store.starts, store.finishes, processor.calls, store.advanced)
	}
	if !tgl.windows[0].From.Equal(now.Add(-DefaultBootstrapLookback)) || !zk.windows[0].To.Equal(now) {
		t.Fatalf("windows tgl=%+v zak=%+v", tgl.windows, zk.windows)
	}
}
