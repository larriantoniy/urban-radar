package content

import (
	"context"
	"fmt"

	"urban-radar/runtime"
)

type ReviewNotifier interface {
	Notify(context.Context, *int64) ([]ReviewNotificationResult, error)
}

type ProcessReadyError struct {
	Source       string `json:"source"`
	SourceItemID string `json:"source_item_id"`
	Stage        string `json:"stage"`
	Error        string `json:"error"`
}

type ProcessReadySummary struct {
	ReadyFound           int                 `json:"ready_found"`
	DraftsCreated        int                 `json:"drafts_created"`
	DraftsReused         int                 `json:"drafts_reused"`
	NotificationsSent    int                 `json:"notifications_sent"`
	NotificationsSkipped int                 `json:"notifications_skipped"`
	Usage                runtime.Usage       `json:"usage"`
	Errors               []ProcessReadyError `json:"errors"`
}

type ReadyProcessor struct {
	Store    ReadyQueueStore
	Agent    Agent
	Notifier ReviewNotifier
	Model    string
	Provider string
}

// Process generates/reuses drafts from the stable READY queue, then notifies
// only the corresponding pending drafts. It has no publication dependency.
func (p ReadyProcessor) Process(ctx context.Context) (ProcessReadySummary, error) {
	if p.Store == nil || p.Agent == nil || p.Notifier == nil {
		return ProcessReadySummary{}, fmt.Errorf("process ready requires store, agent, and notifier")
	}
	events, err := p.Store.ListReadyEvents(ctx)
	if err != nil {
		return ProcessReadySummary{}, err
	}
	summary := ProcessReadySummary{ReadyFound: len(events), Errors: []ProcessReadyError{}}
	experiment := Experiment{Store: p.Store, Agent: p.Agent, Model: p.Model, Provider: p.Provider}
	for _, event := range events {
		draft, created, err := experiment.EnsureDraft(ctx, event)
		if err != nil {
			summary.Errors = append(summary.Errors, processReadyError(event, "generation", err))
			continue
		}
		summary.Usage.Add(draft.Usage)
		if created {
			summary.DraftsCreated++
		} else {
			summary.DraftsReused++
		}
		if draft.HumanReviewStatus != ReviewStatusPending {
			continue
		}
		results, err := p.Notifier.Notify(ctx, &draft.ContentDraftID)
		if err != nil {
			summary.Errors = append(summary.Errors, processReadyError(event, "notification", err))
			continue
		}
		for _, result := range results {
			switch result.Status {
			case "SENT":
				summary.NotificationsSent++
			case "SKIPPED":
				summary.NotificationsSkipped++
			case "ERROR":
				summary.Errors = append(summary.Errors, ProcessReadyError{Source: event.Item.Source, SourceItemID: event.Item.SourceItemID, Stage: "notification", Error: result.Error})
			}
		}
	}
	return summary, nil
}

func processReadyError(event ReadyEvent, stage string, err error) ProcessReadyError {
	return ProcessReadyError{Source: event.Item.Source, SourceItemID: event.Item.SourceItemID, Stage: stage, Error: err.Error()}
}
