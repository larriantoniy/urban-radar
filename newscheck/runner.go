package newscheck

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	urruntime "urban-radar/runtime"
	"urban-radar/source"
)

type Runner struct {
	Store      Store
	Processor  Processor
	Collectors []Collector
	Config     Config
	Now        func() time.Time
}

func (r *Runner) CheckNews(ctx context.Context) (summary Summary, runErr error) {
	started := r.now()
	config := r.Config.withDefaults()
	summary = r.newSummary(started, config)
	release, err := r.Store.AcquireNewsCheck(ctx)
	if err != nil {
		return summary, err
	}
	defer release(context.Background())
	if err := r.Store.StartNewsCheck(ctx, summary.RunID, started); err != nil {
		return summary, err
	}
	defer func() {
		summary.FinishedAt = r.now()
		switch {
		case errors.Is(ctx.Err(), context.Canceled), errors.Is(ctx.Err(), context.DeadlineExceeded):
			summary.RunStatus = "INTERRUPTED"
		case runErr != nil || summary.Status == "FAILED":
			summary.RunStatus = "FAILED"
		default:
			summary.RunStatus = "COMPLETED"
		}
		if err := r.Store.FinishNewsCheck(context.Background(), summary); err != nil && runErr == nil {
			runErr = fmt.Errorf("finish news check: %w", err)
		}
	}()

	sourceSuccesses := 0
	for _, collector := range r.Collectors {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		name := collector.Name()
		collection, sourceSummary, collectErr := r.collectSource(ctx, collector, started, config)
		if sourceSummary.Status == "ERROR" && collection.Items == nil {
			summary.Sources[name] = sourceSummary
			continue
		}
		seen := make(map[string]struct{})
		for _, item := range collection.Items {
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			key := itemKey(item)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			_, inserted, upsertErr := r.Store.UpsertSeen(ctx, item, started)
			if upsertErr != nil {
				collectErr = fmt.Errorf("upsert %s/%s: %w", item.Source, item.SourceItemID, upsertErr)
				break
			}
			if inserted {
				sourceSummary.New++
			} else {
				sourceSummary.Known++
			}
		}
		if collectErr != nil || !collection.Complete {
			sourceSummary.Status = "ERROR"
			sourceSummary.Errors = 1
			if collectErr != nil {
				sourceSummary.Error = collectErr.Error()
			} else {
				sourceSummary.Error = "source pagination safety bound reached before the temporal window completed"
			}
		} else if err := r.Store.AdvanceCheckpoint(ctx, name, started); err != nil {
			sourceSummary.Status, sourceSummary.Errors, sourceSummary.Error = "ERROR", 1, err.Error()
		} else {
			sourceSummary.CheckpointAdvanced = true
			sourceSuccesses++
		}
		summary.Sources[name] = sourceSummary
	}

	processable, err := r.Store.ListProcessable(ctx)
	if err != nil {
		if sourceSuccesses > 0 {
			summary.Status = "PARTIAL"
		}
		summary.Pipeline.Errors = 1
		return summary, err
	}
	sort.SliceStable(processable, func(i, j int) bool {
		a, b := processable[i].Item, processable[j].Item
		if !a.PublishedAt.Equal(b.PublishedAt) {
			return a.PublishedAt.Before(b.PublishedAt)
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.SourceItemID < b.SourceItemID
	})
	summary.Pipeline.Processable = len(processable)
	for _, record := range processable {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if record.State == urruntime.StateError {
			s := summary.Sources[record.Item.Source]
			s.Retryable++
			summary.Sources[record.Item.Source] = s
		}
		result := r.Processor.Process(ctx, record.Item)
		summary.Usage.Add(result.Usage)
		switch result.State {
		case urruntime.StateDiscoveryDropped, urruntime.StateIgnored:
			summary.Pipeline.Ignored++
			summary.Pipeline.Completed++
		case urruntime.StateReadyToPublish:
			summary.Pipeline.ReadyToPublish++
			summary.Pipeline.Completed++
		case urruntime.StateProjectAction:
			summary.Pipeline.ProjectAction++
			summary.Pipeline.Completed++
		case urruntime.StateResearchExhausted:
			summary.Pipeline.ResearchExhausted++
			summary.Pipeline.Completed++
		case urruntime.StateResearchUnsupported:
			summary.Pipeline.ResearchUnsupported++
			summary.Pipeline.Completed++
		default:
			summary.Pipeline.Errors++
		}
	}
	if sourceSuccesses == 0 {
		summary.Status = "FAILED"
	} else if sourceSuccesses != len(r.Collectors) || summary.Pipeline.Errors > 0 {
		summary.Status = "PARTIAL"
	} else {
		summary.Status = "SUCCESS"
	}
	return summary, nil
}

// Preflight uses the same source windows and collectors as CheckNews, but is
// read-only: it never creates a run, upserts an item, advances a checkpoint,
// or invokes the per-item Coordinator.
func (r *Runner) Preflight(ctx context.Context) (Summary, error) {
	started := r.now()
	config := r.Config.withDefaults()
	summary := r.newSummary(started, config)
	release, err := r.Store.AcquireNewsCheck(ctx)
	if err != nil {
		return summary, err
	}
	defer release(context.Background())

	knownProcessable := make(map[string]struct{})
	retryableBySource := make(map[string]int)
	processable, listErr := r.Store.ListProcessable(ctx)
	if listErr != nil {
		summary.Status, summary.Pipeline.Errors, summary.FinishedAt = "FAILED", 1, r.now()
		return summary, listErr
	}
	for _, record := range processable {
		knownProcessable[itemKey(record.Item)] = struct{}{}
		if record.State == urruntime.StateError {
			retryableBySource[record.Item.Source]++
		}
	}

	sourceSuccesses := 0
	newItems := make(map[string]struct{})
	for _, collector := range r.Collectors {
		name := collector.Name()
		collection, sourceSummary, _ := r.collectSource(ctx, collector, started, config)
		seen := make(map[string]struct{})
		for _, item := range collection.Items {
			key := itemKey(item)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			if _, err := r.Store.Get(ctx, item.Source, item.SourceItemID); err == nil {
				sourceSummary.Known++
			} else if errors.Is(err, urruntime.ErrNotFound) {
				sourceSummary.New++
				newItems[key] = struct{}{}
			} else {
				sourceSummary.Status, sourceSummary.Errors, sourceSummary.Error = "ERROR", 1, err.Error()
			}
		}
		if sourceSummary.Status == "SUCCESS" {
			sourceSuccesses++
		}
		sourceSummary.Retryable = retryableBySource[name]
		summary.Sources[name] = sourceSummary
	}
	for key := range newItems {
		knownProcessable[key] = struct{}{}
	}
	summary.Pipeline.Processable = len(knownProcessable)
	if sourceSuccesses == 0 {
		summary.Status = "FAILED"
	} else if sourceSuccesses != len(r.Collectors) {
		summary.Status = "PARTIAL"
	} else {
		summary.Status = "SUCCESS"
	}
	summary.FinishedAt = r.now()
	return summary, nil
}

func (r *Runner) collectSource(ctx context.Context, collector Collector, started time.Time, config Config) (Collection, SourceSummary, error) {
	checkpoint, found, err := r.Store.GetCheckpoint(ctx, collector.Name())
	if err != nil {
		return Collection{}, SourceSummary{Status: "ERROR", Errors: 1, Error: err.Error()}, err
	}
	from := started.Add(-config.BootstrapLookback)
	if found {
		from = checkpoint.Add(-config.Overlap)
	}
	window := Window{From: from, To: started, MaxPages: config.MaxPages}
	collection, err := collector.Collect(ctx, window)
	summary := SourceSummary{Status: "SUCCESS", WindowFrom: from, WindowTo: started, PagesRead: collection.PagesRead, Received: len(collection.Items), Unique: uniqueItemCount(collection.Items)}
	if err != nil || !collection.Complete {
		summary.Status, summary.Errors = "ERROR", 1
		if err != nil {
			summary.Error = err.Error()
		} else {
			summary.Error = "source pagination safety bound reached before the temporal window completed"
		}
	}
	return collection, summary, err
}

func (r *Runner) newSummary(started time.Time, config Config) Summary {
	return Summary{
		RunID: started.Format("20060102T150405.000000000Z"), Status: "FAILED", StartedAt: started,
		Config: ConfigSummary{
			OverlapSeconds: int64(config.Overlap.Seconds()), BootstrapLookbackSeconds: int64(config.BootstrapLookback.Seconds()),
			MaxPages: config.MaxPages, ProcessingOrder: "published_at ASC, source ASC, source_item_id ASC",
			Model: config.Model, Provider: config.Provider, DiscoveryTimeoutSeconds: int64(config.DiscoveryTimeout.Seconds()),
		},
		Sources: make(map[string]SourceSummary),
	}
}

func itemKey(item source.SourceItem) string { return item.Source + "\x00" + item.SourceItemID }

func uniqueItemCount(items []source.SourceItem) int {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		seen[itemKey(item)] = struct{}{}
	}
	return len(seen)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
