package newscheck

import (
	"context"
	"fmt"
	"sort"
	"time"

	urruntime "urban-radar/runtime"
)

type Runner struct {
	Store      Store
	Processor  Processor
	Collectors []Collector
	Config     Config
	Now        func() time.Time
}

func (r *Runner) CheckNews(ctx context.Context) (Summary, error) {
	started := r.now()
	config := r.Config.withDefaults()
	summary := Summary{
		RunID: started.Format("20060102T150405.000000000Z"), Status: "FAILED", StartedAt: started,
		Config: ConfigSummary{
			OverlapSeconds: int64(config.Overlap.Seconds()), BootstrapLookbackSeconds: int64(config.BootstrapLookback.Seconds()),
			MaxPages: config.MaxPages, ProcessingOrder: "published_at ASC, source ASC, source_item_id ASC",
			Model: config.Model, Provider: config.Provider,
		},
		Sources: make(map[string]SourceSummary),
	}
	release, err := r.Store.AcquireNewsCheck(ctx)
	if err != nil {
		return summary, err
	}
	defer release(context.Background())
	if err := r.Store.StartNewsCheck(ctx, summary.RunID, started); err != nil {
		return summary, err
	}

	sourceSuccesses := 0
	for _, collector := range r.Collectors {
		name := collector.Name()
		checkpoint, found, checkpointErr := r.Store.GetCheckpoint(ctx, name)
		if checkpointErr != nil {
			summary.Sources[name] = SourceSummary{Status: "ERROR", Errors: 1, Error: checkpointErr.Error()}
			continue
		}
		from := started.Add(-config.BootstrapLookback)
		if found {
			from = checkpoint.Add(-config.Overlap)
		}
		window := Window{From: from, To: started, MaxPages: config.MaxPages}
		collection, collectErr := collector.Collect(ctx, window)
		sourceSummary := SourceSummary{Status: "SUCCESS", WindowFrom: from, WindowTo: started, PagesRead: collection.PagesRead, Received: len(collection.Items)}
		for _, item := range collection.Items {
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
		summary.FinishedAt = r.now()
		_ = r.Store.FinishNewsCheck(ctx, summary)
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
	summary.FinishedAt = r.now()
	if err := r.Store.FinishNewsCheck(ctx, summary); err != nil {
		return summary, err
	}
	return summary, nil
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
