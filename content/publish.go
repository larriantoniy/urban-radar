package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var vkPostIDPattern = regexp.MustCompile(`^(?:https://vk\.com/)?wall-?[0-9]+_([1-9][0-9]*)$`)

func NormalizeVKPostID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if n, e := strconv.ParseInt(value, 10, 64); e == nil && n > 0 {
		return strconv.FormatInt(n, 10), nil
	}
	m := vkPostIDPattern.FindStringSubmatch(value)
	if len(m) == 2 {
		return m[1], nil
	}
	return "", errors.New("invalid VK post ID")
}

const PlatformPublicationVK = "VK"
const (
	PublicationPending          = "PENDING"
	PublicationPublishing       = "PUBLISHING"
	PublicationPublished        = "PUBLISHED"
	PublicationFailed           = "FAILED"
	PublicationRecoveryRequired = "RECOVERY_REQUIRED"
)

type Publication struct {
	ID, ContentDraftID                          int64
	Platform, Status, LastError, ExternalPostID string
	AttemptCount                                int
	CreatedAt, PublishedAt, UpdatedAt           time.Time
}
type PublishMedia struct {
	StoragePath, SHA256 string
	Bytes               []byte
}
type PublishPayload struct {
	DraftID int64
	Text    string
	Media   *PublishMedia
}
type PublicationClaim struct {
	Publication      Publication
	Idempotent       bool
	RecoveryRequired bool
}
type PublicationStore interface {
	ValidateApprovedPayload(context.Context, int64) (ApprovalPayloadValidation, error)
	LoadApprovedPublicationPayload(context.Context, int64) (PublishPayload, error)
	ClaimPublication(context.Context, int64, string, time.Time) (PublicationClaim, error)
	MarkPublicationPublished(context.Context, int64, string, string, time.Time) error
	MarkPublicationFailed(context.Context, int64, string, string, time.Time) error
	MarkPublicationRecoveryRequired(context.Context, int64, string, string, time.Time) error
	ReconcilePublication(context.Context, int64, string, string, string, time.Time) (Publication, error)
}
type VKPublisher interface {
	Publish(context.Context, string, *PublishMedia) (string, error)
}
type AmbiguousPublishError interface {
	error
	AmbiguousPublish() bool
}
type PublishResult struct {
	Result         string `json:"result"`
	PublicationID  int64  `json:"publication_id,omitempty"`
	ExternalPostID string `json:"external_post_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
}
type PublisherService struct {
	Store     PublicationStore
	VK        VKPublisher
	MediaRoot string
	Now       func() time.Time
}

func (s PublisherService) PublishVK(ctx context.Context, draftID int64) (PublishResult, error) {
	v, e := s.Store.ValidateApprovedPayload(ctx, draftID)
	if e != nil {
		return PublishResult{}, e
	}
	if v.Status != ApprovalPayloadEligible {
		return PublishResult{Result: "BLOCKED", Reason: string(v.Status)}, nil
	}
	p, e := s.Store.LoadApprovedPublicationPayload(ctx, draftID)
	if e != nil {
		return PublishResult{}, e
	}
	if e = verifyPublishMedia(s.MediaRoot, p.Media); e != nil {
		return PublishResult{Result: "BLOCKED", Reason: e.Error()}, nil
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	c, e := s.Store.ClaimPublication(ctx, draftID, PlatformPublicationVK, now)
	if e != nil {
		return PublishResult{}, e
	}
	if c.Idempotent {
		return PublishResult{Result: "IDEMPOTENT", PublicationID: c.Publication.ID, ExternalPostID: c.Publication.ExternalPostID}, nil
	}
	if c.RecoveryRequired {
		return PublishResult{Result: "RECOVERY_REQUIRED", PublicationID: c.Publication.ID, Reason: "PUBLISHING_RECOVERY_REQUIRED"}, nil
	}
	id, e := s.VK.Publish(ctx, p.Text, p.Media)
	if e != nil {
		var a AmbiguousPublishError
		if errors.As(e, &a) && a.AmbiguousPublish() {
			if err := s.Store.MarkPublicationRecoveryRequired(ctx, c.Publication.ID, PlatformPublicationVK, "VK_FINAL_SIDE_EFFECT_AMBIGUOUS", now); err != nil {
				return PublishResult{}, err
			}
			return PublishResult{Result: "RECOVERY_REQUIRED", PublicationID: c.Publication.ID, Reason: "VK_FINAL_SIDE_EFFECT_AMBIGUOUS"}, nil
		}
		_ = s.Store.MarkPublicationFailed(ctx, c.Publication.ID, PlatformPublicationVK, e.Error(), now)
		return PublishResult{Result: "FAILED", PublicationID: c.Publication.ID, Reason: "VK_ERROR"}, nil
	}
	if e = s.Store.MarkPublicationPublished(ctx, c.Publication.ID, PlatformPublicationVK, id, now); e != nil {
		return PublishResult{}, e
	}
	return PublishResult{Result: "PUBLISHED", PublicationID: c.Publication.ID, ExternalPostID: id}, nil
}
func verifyPublishMedia(root string, m *PublishMedia) error {
	if m == nil {
		return nil
	}
	b, e := os.ReadFile(filepath.Join(root, m.StoragePath))
	if e != nil {
		return errors.New("MEDIA_FILE_MISSING")
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != m.SHA256 {
		return errors.New("MEDIA_SHA256_MISMATCH")
	}
	m.Bytes = b
	return nil
}
