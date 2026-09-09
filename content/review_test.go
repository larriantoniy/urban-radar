package content

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeReviewStore struct {
	calls    int
	draftID  int64
	decision ReviewDecision
	actor    string
	at       time.Time
	result   ReviewResult
	err      error
}

func (s *fakeReviewStore) TransitionContentDraftReview(_ context.Context, draftID int64, decision ReviewDecision, actor string, at time.Time) (ReviewResult, error) {
	s.calls++
	s.draftID, s.decision, s.actor, s.at = draftID, decision, actor, at
	return s.result, s.err
}

func TestReviewServiceRoutesExactDeterministicActions(t *testing.T) {
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.FixedZone("SAMT", 4*60*60))
	store := &fakeReviewStore{}
	service := ReviewService{Store: store, Now: func() time.Time { return now }}
	if _, err := service.ApproveDraft(context.Background(), 42, " telegram:123 "); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || store.draftID != 42 || store.decision != ReviewApprove || store.actor != "telegram:123" || !store.at.Equal(now.UTC()) {
		t.Fatalf("unexpected transition: %+v", store)
	}
	if _, err := service.RejectDraft(context.Background(), 43, "operator:radar"); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 || store.draftID != 43 || store.decision != ReviewReject || store.actor != "operator:radar" {
		t.Fatalf("unexpected transition: %+v", store)
	}
}

func TestReviewServiceRejectsInvalidRequestsWithoutCallingStore(t *testing.T) {
	store := &fakeReviewStore{}
	service := ReviewService{Store: store}
	for _, test := range []struct {
		id    int64
		actor string
	}{{0, "actor"}, {1, ""}, {1, "   "}} {
		if _, err := service.ApproveDraft(context.Background(), test.id, test.actor); !errors.Is(err, ErrInvalidReviewRequest) {
			t.Fatalf("id=%d actor=%q err=%v", test.id, test.actor, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("invalid request called store %d times", store.calls)
	}
}

func TestApprovalHashMakesPublishabilityFailClosed(t *testing.T) {
	draft := Draft{HumanReviewRequired: true, HumanReviewStatus: ReviewStatusApproved, PostText: "exact published text"}
	draft.ApprovedContentHash = draft.CurrentContentHash()
	if !IsPublishable(draft) {
		t.Fatal("approved exact text must be publishable")
	}
	draft.PostText = "changed after approval"
	if IsPublishable(draft) {
		t.Fatal("changed text must invalidate approval")
	}
	draft.HumanReviewStatus = ReviewStatusRejected
	if IsPublishable(draft) {
		t.Fatal("rejected draft must never be publishable")
	}
}

func TestContentHashUsesOnlyExactPostText(t *testing.T) {
	if ContentHash("same") != ContentHash("same") || ContentHash("same") == ContentHash("same\n") {
		t.Fatal("content hash must be deterministic over exact post_text bytes")
	}
}

func TestApprovalPayloadHashCanonicalContract(t *testing.T) {
	media := "aabb"
	withoutMedia := ComputeApprovalPayloadHash("text", nil)
	withMedia := ComputeApprovalPayloadHash("text", &media)
	if withoutMedia != ComputeApprovalPayloadHash("text", nil) {
		t.Fatal("same payload must hash identically")
	}
	if withoutMedia == ComputeApprovalPayloadHash("text changed", nil) {
		t.Fatal("text must affect payload hash")
	}
	if withMedia == ComputeApprovalPayloadHash("text", nil) {
		t.Fatal("null media must differ from media")
	}
	otherMedia := "ccdd"
	if withMedia == ComputeApprovalPayloadHash("text", &otherMedia) {
		t.Fatal("media hash must affect payload hash")
	}
}

func TestValidateApprovedPayloadFailsClosed(t *testing.T) {
	media := "media-a"
	draft := Draft{HumanReviewStatus: ReviewStatusApproved, PostText: "text"}
	draft.ApprovedPayloadHash = ComputeApprovalPayloadHash(draft.PostText, &media)
	if got := ValidateApprovedPayload(draft, &media).Status; got != ApprovalPayloadEligible {
		t.Fatalf("got %s", got)
	}
	draft.PostText = "changed"
	if got := ValidateApprovedPayload(draft, &media).Status; got != ApprovalPayloadMismatch {
		t.Fatalf("text got %s", got)
	}
	draft.PostText = "text"
	if got := ValidateApprovedPayload(draft, nil).Status; got != ApprovalPayloadMismatch {
		t.Fatalf("removal got %s", got)
	}
	draft.ApprovedPayloadHash = ComputeApprovalPayloadHash("text", nil)
	if got := ValidateApprovedPayload(draft, &media).Status; got != ApprovalPayloadMismatch {
		t.Fatalf("addition got %s", got)
	}
	draft.ApprovedPayloadHash = ""
	if got := ValidateApprovedPayload(draft, nil).Status; got != ApprovalPayloadMissingApprovedPayloadHash {
		t.Fatalf("legacy got %s", got)
	}
	draft.HumanReviewStatus = ReviewStatusRejected
	if got := ValidateApprovedPayload(draft, nil).Status; got != ApprovalPayloadNotApproved {
		t.Fatalf("rejected got %s", got)
	}
}
