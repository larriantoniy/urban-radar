package content

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeNotificationStore struct {
	drafts  []Draft
	marked  []int64
	markErr error
}

func (s *fakeNotificationStore) ListPendingReviewNotificationDrafts(_ context.Context, draftID *int64) ([]Draft, error) {
	if draftID == nil {
		return s.drafts, nil
	}
	for _, draft := range s.drafts {
		if draft.ContentDraftID == *draftID {
			return []Draft{draft}, nil
		}
	}
	return nil, nil
}

func (s *fakeNotificationStore) MarkReviewNotificationDelivered(_ context.Context, draftID int64, _ ReviewNotificationDelivery, _ time.Time) error {
	if s.markErr != nil {
		return s.markErr
	}
	s.marked = append(s.marked, draftID)
	for i := range s.drafts {
		if s.drafts[i].ContentDraftID == draftID {
			now := time.Unix(1, 0)
			s.drafts[i].ReviewNotifiedAt = &now
		}
	}
	return nil
}

type fakeNotificationSender struct {
	calls []ReviewNotification
	err   error
}

func (s *fakeNotificationSender) SendReviewNotification(_ context.Context, notification ReviewNotification) (ReviewNotificationDelivery, error) {
	s.calls = append(s.calls, notification)
	if s.err != nil {
		return ReviewNotificationDelivery{}, s.err
	}
	return ReviewNotificationDelivery{Channel: "telegram:1001", ExternalID: "77"}, nil
}

func pendingNotificationDraft(id int64) Draft {
	return Draft{ContentDraftID: id, HumanReviewRequired: true, HumanReviewStatus: ReviewStatusPending, PostText: "Городское изменение", SourceURL: "https://example.test/source"}
}

func TestBuildReviewNotificationUsesExactDraftAndCallbackContract(t *testing.T) {
	notification, err := BuildReviewNotification(pendingNotificationDraft(42))
	if err != nil {
		t.Fatal(err)
	}
	want := "Новый пост готов\n\nГородское изменение\n\nИсточник: https://example.test/source"
	if notification.Text != want || notification.ApproveCallbackData != "ur:approve:42" || notification.RejectCallbackData != "ur:reject:42" {
		t.Fatalf("notification=%+v", notification)
	}
}

func TestBuildReviewNotificationRejectsNonPendingAndOverlongMessages(t *testing.T) {
	draft := pendingNotificationDraft(1)
	draft.HumanReviewStatus = ReviewStatusApproved
	if _, err := BuildReviewNotification(draft); !errors.Is(err, ErrInvalidReviewNotification) {
		t.Fatalf("non-pending err=%v", err)
	}
	draft = pendingNotificationDraft(1)
	draft.PostText = string(make([]rune, 4096))
	if _, err := BuildReviewNotification(draft); !errors.Is(err, ErrInvalidReviewNotification) {
		t.Fatalf("overlong err=%v", err)
	}
}

func TestReviewNotificationServicePersistsSuccessAndSkipsReplay(t *testing.T) {
	store := &fakeNotificationStore{drafts: []Draft{pendingNotificationDraft(7)}}
	sender := &fakeNotificationSender{}
	service := ReviewNotificationService{Store: store, Sender: sender, Now: func() time.Time { return time.Unix(10, 0) }}
	id := int64(7)
	results, err := service.Notify(context.Background(), &id)
	if err != nil || len(results) != 1 || results[0].Status != "SENT" || len(sender.calls) != 1 || len(store.marked) != 1 {
		t.Fatalf("results=%+v calls=%d marked=%v err=%v", results, len(sender.calls), store.marked, err)
	}
	// The real store excludes persisted delivered rows. Simulate that contract.
	store.drafts = nil
	results, err = service.Notify(context.Background(), &id)
	if err != nil || len(results) != 1 || results[0].Status != "SKIPPED" || len(sender.calls) != 1 {
		t.Fatalf("replay results=%+v calls=%d err=%v", results, len(sender.calls), err)
	}
}

func TestReviewNotificationServiceLeavesFailedDeliveryRetryableAndTargetsExactlyOneDraft(t *testing.T) {
	store := &fakeNotificationStore{drafts: []Draft{pendingNotificationDraft(10), pendingNotificationDraft(11)}}
	sender := &fakeNotificationSender{err: errors.New("sender unavailable")}
	service := ReviewNotificationService{Store: store, Sender: sender}
	id := int64(11)
	results, err := service.Notify(context.Background(), &id)
	if err != nil || len(results) != 1 || results[0].DraftID != 11 || results[0].Status != "ERROR" || len(sender.calls) != 1 || len(store.marked) != 0 {
		t.Fatalf("results=%+v calls=%d marked=%v err=%v", results, len(sender.calls), store.marked, err)
	}
}

func TestReviewNotificationServiceDoesNotSendNonPendingDraft(t *testing.T) {
	draft := pendingNotificationDraft(12)
	draft.HumanReviewStatus = ReviewStatusApproved
	store := &fakeNotificationStore{drafts: []Draft{draft}}
	sender := &fakeNotificationSender{}
	service := ReviewNotificationService{Store: store, Sender: sender}
	results, err := service.Notify(context.Background(), nil)
	if err != nil || len(results) != 1 || results[0].Status != "ERROR" || len(sender.calls) != 0 || len(store.marked) != 0 {
		t.Fatalf("results=%+v calls=%d marked=%v err=%v", results, len(sender.calls), store.marked, err)
	}
}
