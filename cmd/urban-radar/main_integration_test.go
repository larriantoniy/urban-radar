package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"urban-radar/content"
	urruntime "urban-radar/runtime"
	"urban-radar/source"
	"urban-radar/storage"
)

// TestExecuteContentReviewIntegration proves the same deterministic boundary
// used by the CLI against PostgreSQL. It is opt-in and touches only its own
// storage-test rows.
func TestExecuteContentReviewIntegration(t *testing.T) {
	url := os.Getenv("URBAN_RADAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URBAN_RADAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := storage.OpenPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := storage.NewPostgresStore(db)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	item := source.SourceItem{Source: "storage-test", SourceItemID: "content-review-cli", URL: "https://example.test/review-cli", Title: "review cli", Text: "facts", RetrievedAt: now}
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
	draft := content.Draft{Source: item.Source, SourceItemID: item.SourceItemID, SchemaVersion: content.SchemaVersion, StyleVersion: content.StyleVersion, Platform: content.PlatformVK, EventType: "OTHER", Hook: "Hook", Body: "Body", SourceLabel: "example", SourceURL: item.URL, PostText: "review-cli text", FactWarnings: []string{}, HumanReviewRequired: true, HumanReviewStatus: content.ReviewStatusPending, SourceInputHash: "review-cli-input", EditorPolicyID: "editor-v1:test", EditorInputHash: "editor-input", Model: "test", Provider: "test", GeneratedAt: now, RawOutput: []byte(`{}`)}
	if err := store.SaveContentDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	draft, found, err := store.FindContentDraft(ctx, item.Source, item.SourceItemID, draft.StyleVersion, draft.Platform, draft.SourceInputHash)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	draftB := draft
	draftB.ContentDraftID = 0
	draftB.SourceInputHash = "review-cli-input-b"
	draftB.PostText = "review-cli text B"
	draftB.HumanReviewStatus = content.ReviewStatusPending
	if err := store.SaveContentDraft(ctx, draftB); err != nil {
		t.Fatal(err)
	}
	draftB, found, err = store.FindContentDraft(ctx, item.Source, item.SourceItemID, draftB.StyleVersion, draftB.Platform, draftB.SourceInputHash)
	if err != nil || !found {
		t.Fatalf("draft B found=%v err=%v", found, err)
	}

	approved, err := runReviewCLI(url, "approve", draft.ContentDraftID, "telegram:42")
	if err != nil || approved.Result != "APPLIED" || approved.DraftID != draft.ContentDraftID || approved.ReviewState != content.ReviewStatusApproved {
		t.Fatalf("approved=%+v err=%v", approved, err)
	}
	var state, actor, hash, payloadHash string
	if err := db.QueryRowContext(ctx, `SELECT human_review_status,approved_by,approved_content_hash,approved_payload_hash FROM content_drafts WHERE content_draft_id=$1`, draft.ContentDraftID).Scan(&state, &actor, &hash, &payloadHash); err != nil {
		t.Fatal(err)
	}
	if state != content.ReviewStatusApproved || actor != "telegram:42" || hash != content.ContentHash(draft.PostText) || payloadHash != content.ComputeApprovalPayloadHash(draft.PostText, nil) {
		t.Fatalf("state=%q actor=%q hash=%q payload_hash=%q", state, actor, hash, payloadHash)
	}
	if err := db.QueryRowContext(ctx, `SELECT human_review_status FROM content_drafts WHERE content_draft_id=$1`, draftB.ContentDraftID).Scan(&state); err != nil || state != content.ReviewStatusPending {
		t.Fatalf("draft B changed after callback for A: state=%q err=%v", state, err)
	}
	replay, err := executeContentReview(ctx, url, "approve", draft.ContentDraftID, "telegram:42")
	if err != nil || replay.Result != "IDEMPOTENT" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := executeContentReview(ctx, url, "reject", draft.ContentDraftID, "telegram:42"); !errors.Is(err, content.ErrInvalidReviewTransition) {
		t.Fatalf("approved -> rejected err=%v", err)
	}
	rejected, err := executeContentReview(ctx, url, "reject", draftB.ContentDraftID, "telegram:99")
	if err != nil || rejected.Result != "APPLIED" || rejected.DraftID != draftB.ContentDraftID || rejected.ReviewState != content.ReviewStatusRejected {
		t.Fatalf("rejected=%+v err=%v", rejected, err)
	}
	var rejectedBy string
	if err := db.QueryRowContext(ctx, `SELECT rejected_by FROM content_drafts WHERE content_draft_id=$1`, draftB.ContentDraftID).Scan(&rejectedBy); err != nil || rejectedBy != "telegram:99" {
		t.Fatalf("rejected_by=%q err=%v", rejectedBy, err)
	}
	if _, err := executeContentReview(ctx, url, "approve", 9_223_372_036_854_775_807, "telegram:42"); !errors.Is(err, content.ErrReviewDraftNotFound) {
		t.Fatalf("unknown draft err=%v", err)
	}
	if reviewErrorCode(content.ErrReviewDraftNotFound) != "DRAFT_NOT_FOUND" || reviewErrorCode(content.ErrInvalidReviewTransition) != "INVALID_TRANSITION" || reviewErrorCode(content.ErrInvalidReviewRequest) != "INVALID_ARGUMENTS" {
		t.Fatal("review error codes are not deterministic")
	}
}

func runReviewCLI(databaseURL, action string, draftID int64, actor string) (reviewCommandOutput, error) {
	root, err := filepath.Abs("../..")
	if err != nil {
		return reviewCommandOutput{}, err
	}
	command := exec.Command("go", "run", "./cmd/urban-radar", "content", "review", action, strconv.FormatInt(draftID, 10), "--actor", actor)
	command.Dir = root
	command.Env = append(os.Environ(), "DATABASE_URL="+databaseURL, "GOCACHE=/private/tmp/urban-radar-go-cache")
	output, err := command.Output()
	if err != nil {
		return reviewCommandOutput{}, err
	}
	var result reviewCommandOutput
	if err := json.Unmarshal(output, &result); err != nil {
		return reviewCommandOutput{}, err
	}
	return result, nil
}
