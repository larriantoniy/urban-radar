package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakePubStore struct {
	validation        ApprovalPayloadValidation
	payload           PublishPayload
	claim             PublicationClaim
	failed, published bool
	recovery          bool
	loads             int
	finalizeErr       error
}

func (s *fakePubStore) ValidateApprovedPayload(context.Context, int64) (ApprovalPayloadValidation, error) {
	return s.validation, nil
}
func (s *fakePubStore) LoadApprovedPublicationPayload(context.Context, int64) (PublishPayload, error) {
	s.loads++
	return s.payload, nil
}
func (s *fakePubStore) ClaimPublication(context.Context, int64, string, time.Time) (PublicationClaim, error) {
	return s.claim, nil
}
func (s *fakePubStore) MarkPublicationPublished(context.Context, int64, string, string, time.Time) error {
	s.published = true
	return s.finalizeErr
}
func (s *fakePubStore) MarkPublicationFailed(context.Context, int64, string, string, time.Time) error {
	s.failed = true
	return s.finalizeErr
}
func (s *fakePubStore) MarkPublicationRecoveryRequired(context.Context, int64, string, string, time.Time) error {
	s.recovery = true
	return nil
}
func (s *fakePubStore) ReconcilePublication(context.Context, int64, string, string, string, time.Time) (Publication, error) {
	return Publication{}, nil
}

type fakeVK struct {
	calls int
	id    string
	err   error
}
type ambiguousErr struct{}

func (ambiguousErr) Error() string          { return "timeout" }
func (ambiguousErr) AmbiguousPublish() bool { return true }

func (v *fakeVK) Publish(context.Context, string, *PublishMedia) (string, error) {
	v.calls++
	return v.id, v.err
}

func TestPublisherAmbiguousFailureRequiresRecovery(t *testing.T) {
	s := &fakePubStore{validation: ApprovalPayloadValidation{Status: ApprovalPayloadEligible}, claim: PublicationClaim{Publication: Publication{ID: 4}}}
	v := &fakeVK{err: ambiguousErr{}}
	r, e := (PublisherService{Store: s, VK: v}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "RECOVERY_REQUIRED" || !s.recovery || v.calls != 1 {
		t.Fatal(r, e)
	}
	s.claim.RecoveryRequired = true
	r, e = (PublisherService{Store: s, VK: v}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "RECOVERY_REQUIRED" || v.calls != 1 {
		t.Fatal(r, e)
	}
}

func TestPublisherPublishingClaimBlocksWithoutVKCall(t *testing.T) {
	s := &fakePubStore{
		validation: ApprovalPayloadValidation{Status: ApprovalPayloadEligible},
		claim: PublicationClaim{
			Publication: Publication{ID: 4, Status: PublicationPublishing},
			InProgress:  true,
		},
	}
	v := &fakeVK{}
	r, e := (PublisherService{Store: s, VK: v}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "BLOCKED" || r.Reason != "PUBLISHING_ACTIVE" || r.PublicationID != 4 || v.calls != 0 {
		t.Fatal(r, e)
	}
}

func TestOnlyPublishingMayEnterOperatorRecovery(t *testing.T) {
	for _, status := range []string{
		PublicationPending,
		PublicationPublished,
		PublicationFailed,
		PublicationRecoveryRequired,
	} {
		if CanRecoverPublication(status) {
			t.Fatalf("status %s unexpectedly recoverable", status)
		}
	}
	if !CanRecoverPublication(PublicationPublishing) {
		t.Fatal("PUBLISHING must be recoverable by an explicit operator action")
	}
}

func TestPublisherDoesNotReportFinalStateWhenConditionalFinalizationFails(t *testing.T) {
	s := &fakePubStore{
		validation:  ApprovalPayloadValidation{Status: ApprovalPayloadEligible},
		claim:       PublicationClaim{Publication: Publication{ID: 4, Status: PublicationPublishing}},
		finalizeErr: errors.New("publication is not publishing"),
	}
	v := &fakeVK{id: "77"}
	r, err := (PublisherService{Store: s, VK: v}).PublishVK(context.Background(), 1)
	if err == nil || r.Result != "" || !s.published || v.calls != 1 {
		t.Fatalf("result=%+v err=%v published=%v calls=%d", r, err, s.published, v.calls)
	}
}
func TestPublisherBlocksAndIsIdempotent(t *testing.T) {
	v := &fakeVK{}
	s := &fakePubStore{validation: ApprovalPayloadValidation{Status: ApprovalPayloadNotApproved}}
	r, e := (PublisherService{Store: s, VK: v}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "BLOCKED" || v.calls != 0 {
		t.Fatal(r, e)
	}
	s.validation.Status = ApprovalPayloadEligible
	s.claim = PublicationClaim{Publication: Publication{ID: 2, ExternalPostID: "9"}, Idempotent: true}
	r, e = (PublisherService{Store: s, VK: v}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "IDEMPOTENT" || v.calls != 0 {
		t.Fatal(r, e)
	}
	if s.loads != 0 {
		t.Fatal("confirmed publication must not require disposable media")
	}
}
func TestPublisherTextAndMedia(t *testing.T) {
	root := t.TempDir()
	v := &fakeVK{id: "77"}
	s := &fakePubStore{validation: ApprovalPayloadValidation{Status: ApprovalPayloadEligible}, payload: PublishPayload{DraftID: 1, Text: "text"}, claim: PublicationClaim{Publication: Publication{ID: 3}}}
	r, e := (PublisherService{Store: s, VK: v, MediaRoot: root}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "PUBLISHED" || !s.published || v.calls != 1 {
		t.Fatal(r, e)
	}
	b := []byte("image")
	h := ContentHash(string(b))
	if e = os.WriteFile(filepath.Join(root, "a.jpg"), b, 0600); e != nil {
		t.Fatal(e)
	}
	s.payload.Media = &PublishMedia{StoragePath: "a.jpg", SHA256: h}
	r, e = (PublisherService{Store: s, VK: v, MediaRoot: root}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "PUBLISHED" || len(s.payload.Media.Bytes) == 0 {
		t.Fatal(r, e)
	}
}
func TestPublisherBlocksBadMediaAndFailsVK(t *testing.T) {
	s := &fakePubStore{validation: ApprovalPayloadValidation{Status: ApprovalPayloadEligible}, payload: PublishPayload{Media: &PublishMedia{StoragePath: "missing", SHA256: "x"}}, claim: PublicationClaim{Publication: Publication{ID: 1}}}
	v := &fakeVK{}
	r, e := (PublisherService{Store: s, VK: v, MediaRoot: t.TempDir()}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "BLOCKED" || v.calls != 0 {
		t.Fatal(r, e)
	}
	s.payload.Media = nil
	v.err = errors.New("vk")
	r, e = (PublisherService{Store: s, VK: v}).PublishVK(context.Background(), 1)
	if e != nil || r.Result != "FAILED" || !s.failed {
		t.Fatal(r, e)
	}
}
