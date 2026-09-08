package content

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf16"
)

const telegramMessageUTF16Limit = 4096

var (
	ErrInvalidReviewNotification = errors.New("invalid content draft review notification")
	ErrReviewNotificationSent    = errors.New("content draft review notification already sent")
)

type ReviewNotification struct {
	DraftID             int64  `json:"content_draft_id"`
	Text                string `json:"text"`
	SourceURL           string `json:"source_url"`
	ApproveCallbackData string `json:"approve_callback_data"`
	RejectCallbackData  string `json:"reject_callback_data"`
}

type ReviewNotificationDelivery struct {
	Channel    string `json:"channel"`
	ExternalID string `json:"external_id"`
}

type ReviewNotificationSender interface {
	SendReviewNotification(context.Context, ReviewNotification) (ReviewNotificationDelivery, error)
}

type ReviewNotificationStore interface {
	ListPendingReviewNotificationDrafts(context.Context, *int64) ([]Draft, error)
	MarkReviewNotificationDelivered(context.Context, int64, ReviewNotificationDelivery, time.Time) error
}

type ReviewNotificationResult struct {
	DraftID int64  `json:"content_draft_id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

type ReviewNotificationService struct {
	Store  ReviewNotificationStore
	Sender ReviewNotificationSender
	Now    func() time.Time
}

func (s ReviewNotificationService) Notify(ctx context.Context, draftID *int64) ([]ReviewNotificationResult, error) {
	if s.Store == nil || s.Sender == nil || (draftID != nil && *draftID <= 0) {
		return nil, ErrInvalidReviewNotification
	}
	drafts, err := s.Store.ListPendingReviewNotificationDrafts(ctx, draftID)
	if err != nil {
		return nil, err
	}
	if len(drafts) == 0 && draftID != nil {
		return []ReviewNotificationResult{{DraftID: *draftID, Status: "SKIPPED"}}, nil
	}
	results := make([]ReviewNotificationResult, 0, len(drafts))
	for _, draft := range drafts {
		notification, err := BuildReviewNotification(draft)
		if err != nil {
			results = append(results, ReviewNotificationResult{DraftID: draft.ContentDraftID, Status: "ERROR", Error: err.Error()})
			continue
		}
		delivery, err := s.Sender.SendReviewNotification(ctx, notification)
		if err != nil {
			results = append(results, ReviewNotificationResult{DraftID: draft.ContentDraftID, Status: "ERROR", Error: err.Error()})
			continue
		}
		now := time.Now().UTC()
		if s.Now != nil {
			now = s.Now().UTC()
		}
		if err := s.Store.MarkReviewNotificationDelivered(ctx, draft.ContentDraftID, delivery, now); err != nil {
			results = append(results, ReviewNotificationResult{DraftID: draft.ContentDraftID, Status: "ERROR", Error: err.Error()})
			continue
		}
		results = append(results, ReviewNotificationResult{DraftID: draft.ContentDraftID, Status: "SENT"})
	}
	return results, nil
}

// BuildReviewNotification forms plain deterministic Telegram text. It does
// not summarize, transform, or otherwise generate ContentDraft content.
func BuildReviewNotification(draft Draft) (ReviewNotification, error) {
	if draft.ContentDraftID <= 0 || draft.HumanReviewStatus != ReviewStatusPending || draft.PostText == "" || draft.SourceURL == "" {
		return ReviewNotification{}, ErrInvalidReviewNotification
	}
	approve := "ur:approve:" + strconv.FormatInt(draft.ContentDraftID, 10)
	reject := "ur:reject:" + strconv.FormatInt(draft.ContentDraftID, 10)
	if len(approve) > 64 || len(reject) > 64 {
		return ReviewNotification{}, ErrInvalidReviewNotification
	}
	text := "Новый пост готов\n\n" + draft.PostText + "\n\nИсточник: " + draft.SourceURL
	if len(utf16.Encode([]rune(text))) > telegramMessageUTF16Limit {
		return ReviewNotification{}, fmt.Errorf("%w: Telegram message exceeds %d UTF-16 code units", ErrInvalidReviewNotification, telegramMessageUTF16Limit)
	}
	return ReviewNotification{
		DraftID:             draft.ContentDraftID,
		Text:                text,
		SourceURL:           draft.SourceURL,
		ApproveCallbackData: approve,
		RejectCallbackData:  reject,
	}, nil
}
