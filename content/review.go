package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const approvalPayloadVersion = "approval-payload-v1:"

const (
	ReviewStatusPending  = "PENDING"
	ReviewStatusApproved = "APPROVED"
	ReviewStatusRejected = "REJECTED"
)

type ReviewDecision string

const (
	ReviewApprove ReviewDecision = "APPROVE"
	ReviewReject  ReviewDecision = "REJECT"
)

var (
	ErrInvalidReviewRequest    = errors.New("invalid content draft review request")
	ErrInvalidReviewTransition = errors.New("invalid content draft review transition")
	ErrReviewDraftNotFound     = errors.New("content draft not found")
)

// ReviewStore owns the atomic persisted transition. It intentionally knows
// neither a Telegram identity nor a future publisher implementation.
type ReviewStore interface {
	TransitionContentDraftReview(context.Context, int64, ReviewDecision, string, time.Time) (ReviewResult, error)
}

type ReviewResult struct {
	Draft   Draft
	Applied bool
}

type ApprovalPayloadStatus string

const (
	ApprovalPayloadEligible                   ApprovalPayloadStatus = "ELIGIBLE"
	ApprovalPayloadNotApproved                ApprovalPayloadStatus = "NOT_APPROVED"
	ApprovalPayloadMissingApprovedPayloadHash ApprovalPayloadStatus = "MISSING_APPROVED_PAYLOAD_HASH"
	ApprovalPayloadMismatch                   ApprovalPayloadStatus = "PAYLOAD_MISMATCH"
)

type ApprovalPayloadValidation struct{ Status ApprovalPayloadStatus }

// ComputeApprovalPayloadHash hashes versioned canonical JSON. A struct, not a
// map, fixes the field order; a nil media SHA serializes as JSON null.
func ComputeApprovalPayloadHash(postText string, mediaSHA256 *string) string {
	payload := struct {
		PostText    string  `json:"post_text"`
		MediaSHA256 *string `json:"media_sha256"`
	}{PostText: postText, MediaSHA256: mediaSHA256}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(append([]byte(approvalPayloadVersion), encoded...))
	return hex.EncodeToString(sum[:])
}

func ValidateApprovedPayload(draft Draft, mediaSHA256 *string) ApprovalPayloadValidation {
	if draft.HumanReviewStatus != ReviewStatusApproved {
		return ApprovalPayloadValidation{Status: ApprovalPayloadNotApproved}
	}
	if draft.ApprovedPayloadHash == "" {
		return ApprovalPayloadValidation{Status: ApprovalPayloadMissingApprovedPayloadHash}
	}
	if draft.ApprovedPayloadHash != ComputeApprovalPayloadHash(draft.PostText, mediaSHA256) {
		return ApprovalPayloadValidation{Status: ApprovalPayloadMismatch}
	}
	return ApprovalPayloadValidation{Status: ApprovalPayloadEligible}
}

// ReviewService is the deterministic boundary that a future transport adapter
// can call after it has authenticated an actor and parsed an exact draft ID.
type ReviewService struct {
	Store ReviewStore
	Now   func() time.Time
}

func (s ReviewService) ApproveDraft(ctx context.Context, draftID int64, actor string) (ReviewResult, error) {
	return s.transition(ctx, draftID, ReviewApprove, actor)
}

func (s ReviewService) RejectDraft(ctx context.Context, draftID int64, actor string) (ReviewResult, error) {
	return s.transition(ctx, draftID, ReviewReject, actor)
}

func (s ReviewService) transition(ctx context.Context, draftID int64, decision ReviewDecision, actor string) (ReviewResult, error) {
	if s.Store == nil || draftID <= 0 || strings.TrimSpace(actor) == "" {
		return ReviewResult{}, ErrInvalidReviewRequest
	}
	if decision != ReviewApprove && decision != ReviewReject {
		return ReviewResult{}, fmt.Errorf("%w: %q", ErrInvalidReviewRequest, decision)
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return s.Store.TransitionContentDraftReview(ctx, draftID, decision, strings.TrimSpace(actor), now)
}

// ContentHash is SHA-256 of the exact UTF-8 post_text bytes. post_text is the
// only currently defined publishable ContentDraft field; IDs, audit metadata,
// source provenance, and timestamps are intentionally excluded.
func ContentHash(postText string) string {
	sum := sha256.Sum256([]byte(postText))
	return hex.EncodeToString(sum[:])
}

func (d Draft) CurrentContentHash() string { return ContentHash(d.PostText) }

// IsPublishable is the future publisher's fail-closed integrity gate. An
// APPROVED status alone never authorizes publishing a changed draft.
func IsPublishable(d Draft) bool {
	return d.HumanReviewRequired &&
		d.HumanReviewStatus == ReviewStatusApproved &&
		d.ApprovedContentHash != "" &&
		d.ApprovedContentHash == d.CurrentContentHash()
}
