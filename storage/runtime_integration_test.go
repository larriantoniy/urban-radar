package storage

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"urban-radar/content"
	"urban-radar/newscheck"
	urruntime "urban-radar/runtime"
	"urban-radar/source"
)

// TestPostgresSelectiveMaterializationContract exercises the real store when
// an operator supplies an isolated local development database. It is skipped
// in ordinary unit-test runs and cleans up its one uniquely identified row.
func TestPostgresSelectiveMaterializationContract(t *testing.T) {
	url := os.Getenv("URBAN_RADAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URBAN_RADAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPostgresStore(db)
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	item := source.SourceItem{
		Source: "storage-test", SourceItemID: "selective-materialization-contract", URL: "https://example.test/item",
		Title: "compact me", Summary: "long summary", Text: "large source payload", PublishedAt: now, RetrievedAt: now,
		Metadata: map[string]any{"raw": "large metadata payload"},
	}
	defer db.ExecContext(ctx, `DELETE FROM source_items WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)

	record, inserted, err := store.UpsertSeen(ctx, item, now)
	if err != nil || !inserted {
		t.Fatalf("upsert inserted=%v err=%v", inserted, err)
	}
	record.State = urruntime.StateDiscoveryDropped
	record.Discovery = &urruntime.StageResult{PolicyID: "discovery", Output: []byte(`{"candidates":[]}`), At: now}
	record.UpdatedAt = now
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(ctx, item.Source, item.SourceItemID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != urruntime.StateDiscoveryDropped || stored.Discovery == nil || stored.Item.Summary != "" || stored.Item.Text != "" || len(stored.Item.Metadata) != 0 {
		t.Fatalf("drop was not compacted: %+v", stored)
	}
	if stored.Item.URL != item.URL || stored.Item.Title != item.Title || stored.SourceFingerprint == "" {
		t.Fatalf("drop lost compact audit/provenance: %+v", stored)
	}

	updated := item
	updated.Text = "source payload must not restore a terminal drop"
	updated.Metadata = map[string]any{"new": "payload"}
	_, inserted, err = store.UpsertSeen(ctx, updated, now.Add(time.Hour))
	if err != nil || inserted {
		t.Fatalf("overlap upsert inserted=%v err=%v", inserted, err)
	}
	stored, err = store.Get(ctx, item.Source, item.SourceItemID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Item.Summary != "" || stored.Item.Text != "" || len(stored.Item.Metadata) != 0 || !stored.LastSeenAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("overlap re-materialized terminal drop: %+v", stored)
	}
	if stored.SourceFingerprint != urruntime.SourceItemSemanticHash(updated) {
		t.Fatalf("overlap did not retain the latest source revision marker: %q", stored.SourceFingerprint)
	}
}

func TestPostgresStoreFinalizesNewsCheckLifecycle(t *testing.T) {
	url := os.Getenv("URBAN_RADAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URBAN_RADAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPostgresStore(db)
	started := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	runID := "storage-test-interrupted-lifecycle"
	defer db.ExecContext(ctx, `DELETE FROM news_check_runs WHERE run_id=$1`, runID)
	if err := store.StartNewsCheck(ctx, runID, started); err != nil {
		t.Fatal(err)
	}
	summary := newscheck.Summary{RunID: runID, Status: "PARTIAL", RunStatus: "INTERRUPTED", StartedAt: started, FinishedAt: started.Add(time.Second)}
	if err := store.FinishNewsCheck(ctx, summary); err != nil {
		t.Fatal(err)
	}
	var status string
	var finished time.Time
	if err := db.QueryRowContext(ctx, `SELECT status, finished_at FROM news_check_runs WHERE run_id=$1`, runID).Scan(&status, &finished); err != nil {
		t.Fatal(err)
	}
	if status != "INTERRUPTED" || !finished.Equal(summary.FinishedAt) {
		t.Fatalf("status=%q finished=%s", status, finished)
	}
}

func TestPostgresStorePersistsContentDraftSeparately(t *testing.T) {
	url := os.Getenv("URBAN_RADAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URBAN_RADAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPostgresStore(db)
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	item := source.SourceItem{Source: "storage-test", SourceItemID: "content-draft-contract", URL: "https://example.test/content", Title: "content", Text: "facts", RetrievedAt: now}
	_, _ = db.ExecContext(ctx, `DELETE FROM content_drafts WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	_, _ = db.ExecContext(ctx, `DELETE FROM source_items WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	defer db.ExecContext(ctx, `DELETE FROM source_items WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	defer db.ExecContext(ctx, `DELETE FROM content_drafts WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	record, _, err := store.UpsertSeen(ctx, item, now)
	if err != nil {
		t.Fatal(err)
	}
	record.State = urruntime.StateReadyToPublish
	record.Discovery = &urruntime.StageResult{Output: []byte(`{"outcome":"CANDIDATE"}`), At: now}
	record.Editor = &urruntime.StageResult{PolicyID: "editor-v1:test", InputHash: "editor-input", Output: []byte(`{"decisions":[{"decision":"PUBLISH"}]}`), At: now}
	record.UpdatedAt = now
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	draft := content.Draft{Source: item.Source, SourceItemID: item.SourceItemID, SchemaVersion: content.SchemaVersion, StyleVersion: content.StyleVersion, Platform: content.PlatformVK, EventType: "OTHER", Hook: "Hook", Body: "Body", SourceLabel: "example", SourceURL: item.URL, PostText: "Hook\n\nИсточник: https://example.test/content", FactWarnings: []string{}, HumanReviewRequired: true, HumanReviewStatus: "PENDING", SourceInputHash: "content-input", EditorPolicyID: "editor-v1:test", EditorInputHash: "editor-input", Model: "test", Provider: "test", GeneratedAt: now, RawOutput: []byte(`{}`)}
	if err := store.SaveContentDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT human_review_status FROM content_drafts WHERE source=$1 AND source_item_id=$2 AND source_input_hash=$3`, item.Source, item.SourceItemID, draft.SourceInputHash).Scan(&status); err != nil || status != "PENDING" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	stored, err := store.Get(ctx, item.Source, item.SourceItemID)
	if err != nil || stored.State != urruntime.StateReadyToPublish || stored.Editor == nil {
		t.Fatalf("source item was mutated: %+v err=%v", stored, err)
	}
}

func TestPostgresStoreContentDraftReviewTransition(t *testing.T) {
	url := os.Getenv("URBAN_RADAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URBAN_RADAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPostgresStore(db)
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	item := source.SourceItem{Source: "storage-test", SourceItemID: "content-draft-review", URL: "https://example.test/review", Title: "review", Text: "facts", RetrievedAt: now}
	_, _ = db.ExecContext(ctx, `DELETE FROM content_drafts WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	_, _ = db.ExecContext(ctx, `DELETE FROM source_items WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	defer db.ExecContext(ctx, `DELETE FROM source_items WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	defer db.ExecContext(ctx, `DELETE FROM content_drafts WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	record, _, err := store.UpsertSeen(ctx, item, now)
	if err != nil {
		t.Fatal(err)
	}
	record.State = urruntime.StateReadyToPublish
	record.Discovery = &urruntime.StageResult{Output: []byte(`{"outcome":"CANDIDATE"}`), At: now}
	record.Editor = &urruntime.StageResult{PolicyID: "editor-v1:test", InputHash: "editor-input", Output: []byte(`{"decisions":[{"decision":"PUBLISH"}]}`), At: now}
	record.UpdatedAt = now
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	draft := content.Draft{Source: item.Source, SourceItemID: item.SourceItemID, SchemaVersion: content.SchemaVersion, StyleVersion: content.StyleVersion, Platform: content.PlatformVK, EventType: "OTHER", Hook: "Hook", Body: "Body", SourceLabel: "example", SourceURL: item.URL, PostText: "exact review text", FactWarnings: []string{}, HumanReviewRequired: true, HumanReviewStatus: content.ReviewStatusPending, SourceInputHash: "review-input", EditorPolicyID: "editor-v1:test", EditorInputHash: "editor-input", Model: "test", Provider: "test", GeneratedAt: now, RawOutput: []byte(`{}`)}
	if err := store.SaveContentDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	draft, found, err := store.FindContentDraft(ctx, item.Source, item.SourceItemID, draft.StyleVersion, draft.Platform, draft.SourceInputHash)
	if err != nil || !found || draft.ContentDraftID == 0 {
		t.Fatalf("found=%v draft=%+v err=%v", found, draft, err)
	}
	defer db.ExecContext(ctx, `DELETE FROM content_media WHERE content_draft_id=$1`, draft.ContentDraftID)
	mediaSHA := "media-sha-at-approval"
	if _, err := db.ExecContext(ctx, `INSERT INTO content_media(content_draft_id,storage_path,sha256,uploaded_at,uploaded_by) VALUES($1,$2,$3,$4,$5)`, draft.ContentDraftID, "draft-test.jpg", mediaSHA, now, "telegram:123"); err != nil {
		t.Fatal(err)
	}
	service := content.ReviewService{Store: store, Now: func() time.Time { return now }}
	type approvalResult struct {
		result content.ReviewResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan approvalResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := service.ApproveDraft(ctx, draft.ContentDraftID, "telegram:123")
			results <- approvalResult{result, err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var approved content.ReviewResult
	applied := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.result.Applied {
			applied++
			approved = result.result
		}
	}
	if applied != 1 || approved.Draft.HumanReviewStatus != content.ReviewStatusApproved || approved.Draft.ApprovedAt == nil || approved.Draft.ApprovedBy != "telegram:123" || approved.Draft.RejectedAt != nil || approved.Draft.RejectedBy != "" || !content.IsPublishable(approved.Draft) {
		t.Fatalf("concurrent approved=%+v applied=%d", approved, applied)
	}
	if approved.Draft.ApprovedContentHash != content.ContentHash("exact review text") {
		t.Fatalf("wrong approval hash: %q", approved.Draft.ApprovedContentHash)
	}
	if approved.Draft.ApprovedPayloadHash != content.ComputeApprovalPayloadHash("exact review text", &mediaSHA) {
		t.Fatalf("wrong approval payload hash: %q", approved.Draft.ApprovedPayloadHash)
	}
	if validation, err := store.ValidateApprovedPayload(ctx, draft.ContentDraftID); err != nil || validation.Status != content.ApprovalPayloadEligible {
		t.Fatalf("validation=%+v err=%v", validation, err)
	}
	replay, err := service.ApproveDraft(ctx, draft.ContentDraftID, "telegram:123")
	if err != nil || replay.Applied || replay.Draft.ApprovedAt == nil || !replay.Draft.ApprovedAt.Equal(*approved.Draft.ApprovedAt) || replay.Draft.ApprovedPayloadHash != approved.Draft.ApprovedPayloadHash {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := service.RejectDraft(ctx, draft.ContentDraftID, "telegram:123"); !errors.Is(err, content.ErrInvalidReviewTransition) {
		t.Fatalf("approved -> rejected err=%v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE content_drafts SET post_text='mutated after approval' WHERE content_draft_id=$1`, draft.ContentDraftID); err != nil {
		t.Fatal(err)
	}
	mutated, found, err := store.FindContentDraft(ctx, item.Source, item.SourceItemID, draft.StyleVersion, draft.Platform, draft.SourceInputHash)
	if err != nil || !found || content.IsPublishable(mutated) {
		t.Fatalf("mutated=%+v found=%v err=%v", mutated, found, err)
	}
	if validation, err := store.ValidateApprovedPayload(ctx, draft.ContentDraftID); err != nil || validation.Status != content.ApprovalPayloadMismatch {
		t.Fatalf("text validation=%+v err=%v", validation, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE content_drafts SET post_text='exact review text' WHERE content_draft_id=$1`, draft.ContentDraftID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE content_media SET sha256='media-sha-replaced' WHERE content_draft_id=$1`, draft.ContentDraftID); err != nil {
		t.Fatal(err)
	}
	if validation, err := store.ValidateApprovedPayload(ctx, draft.ContentDraftID); err != nil || validation.Status != content.ApprovalPayloadMismatch {
		t.Fatalf("media validation=%+v err=%v", validation, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE content_media SET deleted_at=$2 WHERE content_draft_id=$1`, draft.ContentDraftID, now); err != nil {
		t.Fatal(err)
	}
	if validation, err := store.ValidateApprovedPayload(ctx, draft.ContentDraftID); err != nil || validation.Status != content.ApprovalPayloadMismatch {
		t.Fatalf("removed media validation=%+v err=%v", validation, err)
	}
	rejected := draft
	rejected.ContentDraftID = 0
	rejected.SourceInputHash = "rejected-input"
	rejected.PostText = "text rejected before publication"
	if err := store.SaveContentDraft(ctx, rejected); err != nil {
		t.Fatal(err)
	}
	rejected, found, err = store.FindContentDraft(ctx, item.Source, item.SourceItemID, rejected.StyleVersion, rejected.Platform, rejected.SourceInputHash)
	if err != nil || !found {
		t.Fatalf("rejected draft found=%v err=%v", found, err)
	}
	rejectedResult, err := service.RejectDraft(ctx, rejected.ContentDraftID, "operator:radar")
	if err != nil || !rejectedResult.Applied || rejectedResult.Draft.HumanReviewStatus != content.ReviewStatusRejected || rejectedResult.Draft.RejectedAt == nil || rejectedResult.Draft.RejectedBy != "operator:radar" || rejectedResult.Draft.ApprovedAt != nil || rejectedResult.Draft.ApprovedContentHash != "" || rejectedResult.Draft.ApprovedPayloadHash != "" || content.IsPublishable(rejectedResult.Draft) {
		t.Fatalf("rejected=%+v err=%v", rejectedResult, err)
	}
	if _, err := service.ApproveDraft(ctx, rejected.ContentDraftID, "operator:radar"); !errors.Is(err, content.ErrInvalidReviewTransition) {
		t.Fatalf("rejected -> approved err=%v", err)
	}

	var publicationID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO publications(content_draft_id,platform,status,created_at,updated_at) VALUES($1,'VK','PUBLISHING',$2,$2) RETURNING publication_id`, draft.ContentDraftID, now).Scan(&publicationID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverPublication(ctx, publicationID, "operator:recovery", now.Add(time.Minute))
	if err != nil || recovered.Status != content.PublicationRecoveryRequired || recovered.LastError != "OPERATOR_RECOVERY_REQUIRED" {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	var recoveredAt time.Time
	var recoveredBy string
	if err := db.QueryRowContext(ctx, `SELECT recovery_required_at,recovery_required_by FROM publications WHERE publication_id=$1`, publicationID).Scan(&recoveredAt, &recoveredBy); err != nil || !recoveredAt.Equal(now.Add(time.Minute)) || recoveredBy != "operator:recovery" {
		t.Fatalf("recovery audit at=%s by=%q err=%v", recoveredAt, recoveredBy, err)
	}
	if _, err := store.RecoverPublication(ctx, publicationID, "operator:recovery", now.Add(2*time.Minute)); err == nil {
		t.Fatal("RECOVERY_REQUIRED must not be recovered again")
	}
	if reconciled, err := store.ReconcilePublication(ctx, publicationID, "MARK_NOT_PUBLISHED", "", "operator:reconcile", now.Add(3*time.Minute)); err != nil || reconciled.Status != content.PublicationFailed {
		t.Fatalf("reconciled=%+v err=%v", reconciled, err)
	}
	if _, err := store.RecoverPublication(ctx, publicationID, "operator:recovery", now.Add(4*time.Minute)); err == nil {
		t.Fatal("FAILED must not enter operator recovery")
	}
}

func TestPostgresStoreReviewNotificationDelivery(t *testing.T) {
	url := os.Getenv("URBAN_RADAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URBAN_RADAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPostgresStore(db)
	now := time.Date(2026, 9, 8, 13, 0, 0, 0, time.UTC)
	item := source.SourceItem{Source: "storage-test", SourceItemID: "content-review-notification", URL: "https://example.test/review-notify", Title: "review notify", Text: "facts", RetrievedAt: now}
	_, _ = db.ExecContext(ctx, `DELETE FROM content_drafts WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	_, _ = db.ExecContext(ctx, `DELETE FROM source_items WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	defer db.ExecContext(ctx, `DELETE FROM source_items WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	defer db.ExecContext(ctx, `DELETE FROM content_drafts WHERE source=$1 AND source_item_id=$2`, item.Source, item.SourceItemID)
	record, _, err := store.UpsertSeen(ctx, item, now)
	if err != nil {
		t.Fatal(err)
	}
	record.State = urruntime.StateReadyToPublish
	record.Discovery = &urruntime.StageResult{Output: []byte(`{"outcome":"CANDIDATE"}`), At: now}
	record.Editor = &urruntime.StageResult{PolicyID: "editor-v1:test", InputHash: "editor-input", Output: []byte(`{"decisions":[{"decision":"PUBLISH"}]}`), At: now}
	record.UpdatedAt = now
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	draft := content.Draft{Source: item.Source, SourceItemID: item.SourceItemID, SchemaVersion: content.SchemaVersion, StyleVersion: content.StyleVersion, Platform: content.PlatformVK, EventType: "OTHER", Hook: "Hook", Body: "Body", SourceLabel: "example", SourceURL: item.URL, PostText: "review notification text", FactWarnings: []string{}, HumanReviewRequired: true, HumanReviewStatus: content.ReviewStatusPending, SourceInputHash: "review-notify-input", EditorPolicyID: "editor-v1:test", EditorInputHash: "editor-input", Model: "test", Provider: "test", GeneratedAt: now, RawOutput: []byte(`{}`)}
	if err := store.SaveContentDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	eligible, err := store.ListPendingReviewNotificationDrafts(ctx, nil)
	if err != nil || len(eligible) == 0 {
		t.Fatalf("eligible=%+v err=%v", eligible, err)
	}
	var stored content.Draft
	for _, candidate := range eligible {
		if candidate.Source == item.Source && candidate.SourceItemID == item.SourceItemID {
			stored = candidate
		}
	}
	if stored.ContentDraftID == 0 {
		t.Fatal("test draft was not eligible")
	}
	delivery := content.ReviewNotificationDelivery{Channel: "telegram:1001", ExternalID: "9001"}
	if err := store.MarkReviewNotificationDelivered(ctx, stored.ContentDraftID, delivery, now); err != nil {
		t.Fatal(err)
	}
	id := stored.ContentDraftID
	eligible, err = store.ListPendingReviewNotificationDrafts(ctx, &id)
	if err != nil || len(eligible) != 0 {
		t.Fatalf("delivered draft replay eligible=%+v err=%v", eligible, err)
	}
	if err := store.MarkReviewNotificationDelivered(ctx, stored.ContentDraftID, delivery, now); !errors.Is(err, content.ErrReviewNotificationSent) {
		t.Fatalf("second delivery mark err=%v", err)
	}
	var channel, externalID string
	var notified time.Time
	if err := db.QueryRowContext(ctx, `SELECT review_notified_at,review_notification_channel,review_notification_external_id FROM content_drafts WHERE content_draft_id=$1`, stored.ContentDraftID).Scan(&notified, &channel, &externalID); err != nil {
		t.Fatal(err)
	}
	if !notified.Equal(now) || channel != delivery.Channel || externalID != delivery.ExternalID {
		t.Fatalf("notified=%s channel=%q externalID=%q", notified, channel, externalID)
	}
}
