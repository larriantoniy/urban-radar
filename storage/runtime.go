package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"urban-radar/content"
	"urban-radar/newscheck"
	urruntime "urban-radar/runtime"
	"urban-radar/source"
)

const newsCheckAdvisoryLock int64 = 0x555242414e524144

// PostgresStore persists the incremental runtime in the existing
// source_items table plus source-specific checkpoints and run summaries.
type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

var _ urruntime.Store = (*PostgresStore)(nil)
var _ newscheck.Store = (*PostgresStore)(nil)
var _ content.Store = (*PostgresStore)(nil)
var _ content.ReviewStore = (*PostgresStore)(nil)
var _ content.ReviewNotificationStore = (*PostgresStore)(nil)
var _ content.MediaStore = (*PostgresStore)(nil)

func (r *PostgresStore) AttachMedia(ctx context.Context, draftID int64, media content.Media) (content.Media, *content.Media, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return content.Media{}, nil, err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT human_review_status FROM content_drafts WHERE content_draft_id=$1 FOR UPDATE`, draftID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return content.Media{}, nil, content.ErrMediaDraftNotFound
	} else if err != nil {
		return content.Media{}, nil, err
	}
	if status != content.ReviewStatusPending {
		return content.Media{}, nil, content.ErrMediaFinalized
	}
	var old content.Media
	var oldUploaded, oldDeleted sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT id,content_draft_id,storage_path,sha256,uploaded_at,uploaded_by,deleted_at FROM content_media WHERE content_draft_id=$1 AND deleted_at IS NULL`, draftID).Scan(&old.ID, &old.ContentDraftID, &old.StoragePath, &old.SHA256, &oldUploaded, &old.UploadedBy, &oldDeleted)
	if err == nil {
		old.UploadedAt = &oldUploaded.Time
		if oldDeleted.Valid {
			old.DeletedAt = &oldDeleted.Time
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return content.Media{}, nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO content_media(content_draft_id,storage_path,sha256,uploaded_at,uploaded_by) VALUES($1,$2,$3,$4,$5) ON CONFLICT(content_draft_id) DO UPDATE SET storage_path=EXCLUDED.storage_path,sha256=EXCLUDED.sha256,uploaded_at=EXCLUDED.uploaded_at,uploaded_by=EXCLUDED.uploaded_by,deleted_at=NULL`, draftID, media.StoragePath, media.SHA256, media.UploadedAt, media.UploadedBy)
	if err != nil {
		return content.Media{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return content.Media{}, nil, err
	}
	if old.ID != 0 {
		return media, &old, nil
	}
	return media, nil, nil
}

func (r *PostgresStore) CleanupRejectedMedia(ctx context.Context, cutoff time.Time, dryRun bool) (int, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT cm.id,cm.storage_path FROM content_media cm JOIN content_drafts d ON d.content_draft_id=cm.content_draft_id WHERE d.human_review_status='REJECTED' AND d.rejected_at <= $1 AND cm.deleted_at IS NULL`, cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var path string
		if err = rows.Scan(&id, &path); err != nil {
			return count, err
		}
		count++
		if !dryRun {
			_ = os.Remove(filepath.Join(content.MediaRootFromEnv(), path))
			if _, err = r.db.ExecContext(ctx, `UPDATE content_media SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`, id); err != nil {
				return count, err
			}
		}
	}
	return count, rows.Err()
}

func (r *PostgresStore) Get(ctx context.Context, sourceName, sourceItemID string) (urruntime.ItemRecord, error) {
	row := r.db.QueryRowContext(ctx, runtimeSelect+` WHERE source=$1 AND source_item_id=$2`, sourceName, sourceItemID)
	record, err := scanRuntime(row)
	if errors.Is(err, sql.ErrNoRows) {
		return record, urruntime.ErrNotFound
	}
	return record, err
}

func (r *PostgresStore) UpsertSeen(ctx context.Context, item source.SourceItem, seenAt time.Time) (urruntime.ItemRecord, bool, error) {
	metadata, err := metadataJSON(item.Metadata)
	if err != nil {
		return urruntime.ItemRecord{}, false, err
	}
	fingerprint := urruntime.SourceItemSemanticHash(item)
	row := r.db.QueryRowContext(ctx, `INSERT INTO source_items
(source,source_item_id,source_url,title,summary,body_text,published_at_source,retrieved_at,
 first_seen_at,last_seen_at,processing_state,metadata,source_fingerprint)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,'RECEIVED',$10,$11)
ON CONFLICT (source,source_item_id) DO UPDATE SET
 source_url=EXCLUDED.source_url,title=EXCLUDED.title,
 summary=CASE WHEN source_items.processing_state='DISCOVERY_DROPPED' THEN source_items.summary ELSE EXCLUDED.summary END,
 body_text=CASE WHEN source_items.processing_state='DISCOVERY_DROPPED' THEN source_items.body_text ELSE EXCLUDED.body_text END,
 metadata=CASE WHEN source_items.processing_state='DISCOVERY_DROPPED' THEN source_items.metadata ELSE EXCLUDED.metadata END,
 published_at_source=EXCLUDED.published_at_source,
 retrieved_at=EXCLUDED.retrieved_at,last_seen_at=EXCLUDED.last_seen_at,
 source_fingerprint=EXCLUDED.source_fingerprint,updated_at=now()
RETURNING source,source_item_id,source_url,title,summary,body_text,published_at_source,
 retrieved_at,metadata,first_seen_at,last_seen_at,processing_state,retry_stage,
 research_rounds,last_error,runtime_data,updated_at,source_fingerprint,(xmax = 0)`,
		item.Source, item.SourceItemID, item.URL, item.Title, item.Summary, item.Text,
		nullTime(item.PublishedAt), item.RetrievedAt, seenAt, metadata, fingerprint)
	record, inserted, err := scanRuntimeWithInserted(row)
	return record, inserted, err
}

func (r *PostgresStore) Save(ctx context.Context, record urruntime.ItemRecord) error {
	data, err := json.Marshal(runtimePayload(record))
	if err != nil {
		return fmt.Errorf("encode runtime state: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE source_items SET
	processing_state=$3,retry_stage=$4,research_rounds=$5,last_error=$6,
	runtime_data=$7,updated_at=$8,
	summary=CASE WHEN $3='DISCOVERY_DROPPED' THEN '' ELSE summary END,
	body_text=CASE WHEN $3='DISCOVERY_DROPPED' THEN '' ELSE body_text END,
	metadata=CASE WHEN $3='DISCOVERY_DROPPED' THEN '{}'::jsonb ELSE metadata END
WHERE source=$1 AND source_item_id=$2`, record.Item.Source, record.Item.SourceItemID,
		record.State, record.RetryStage, record.ResearchRounds, record.LastError, data, record.UpdatedAt)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows != 1 {
		return urruntime.ErrNotFound
	}
	return err
}

func (r *PostgresStore) ListProcessable(ctx context.Context) ([]urruntime.ItemRecord, error) {
	rows, err := r.db.QueryContext(ctx, runtimeSelect+` WHERE processing_state IN
('RECEIVED','DISCOVERY','EDITOR','RESEARCH','EDITOR_REEVALUATION','ERROR')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []urruntime.ItemRecord
	for rows.Next() {
		record, err := scanRuntime(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (r *PostgresStore) GetCheckpoint(ctx context.Context, sourceName string) (time.Time, bool, error) {
	var value time.Time
	err := r.db.QueryRowContext(ctx, `SELECT last_successful_collection_at FROM source_checkpoints WHERE source=$1`, sourceName).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return value, err == nil, err
}

func (r *PostgresStore) AdvanceCheckpoint(ctx context.Context, sourceName string, boundary time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO source_checkpoints(source,last_successful_collection_at,updated_at)
VALUES($1,$2,now()) ON CONFLICT(source) DO UPDATE SET
last_successful_collection_at=EXCLUDED.last_successful_collection_at,updated_at=now()`, sourceName, boundary)
	return err
}

func (r *PostgresStore) AcquireNewsCheck(ctx context.Context) (func(context.Context) error, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, newsCheckAdvisoryLock).Scan(&acquired); err != nil {
		conn.Close()
		return nil, err
	}
	if !acquired {
		conn.Close()
		return nil, newscheck.ErrAlreadyRunning
	}
	return func(releaseCtx context.Context) error {
		defer conn.Close()
		var released bool
		if err := conn.QueryRowContext(releaseCtx, `SELECT pg_advisory_unlock($1)`, newsCheckAdvisoryLock).Scan(&released); err != nil {
			return err
		}
		if !released {
			return fmt.Errorf("news check advisory lock was not held")
		}
		return nil
	}, nil
}

func (r *PostgresStore) StartNewsCheck(ctx context.Context, runID string, started time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO news_check_runs(run_id,started_at,status) VALUES($1,$2,'RUNNING')`, runID, started)
	return err
}

func (r *PostgresStore) FinishNewsCheck(ctx context.Context, summary newscheck.Summary) error {
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	runStatus := summary.RunStatus
	if runStatus == "" {
		runStatus = "COMPLETED"
		if summary.Status == "FAILED" {
			runStatus = "FAILED"
		}
	}
	_, err = r.db.ExecContext(ctx, `UPDATE news_check_runs SET finished_at=$2,status=$3,summary=$4 WHERE run_id=$1`, summary.RunID, summary.FinishedAt, runStatus, data)
	return err
}

// LoadReadyEvents returns only explicitly requested terminal editorial records.
// It never invokes, advances, or rewrites the runtime pipeline.
func (r *PostgresStore) LoadReadyEvents(ctx context.Context, refs []content.SourceRef) ([]content.ReadyEvent, error) {
	result := make([]content.ReadyEvent, 0, len(refs))
	for _, ref := range refs {
		record, err := r.Get(ctx, ref.Source, ref.SourceItemID)
		if err != nil {
			return nil, err
		}
		if record.State != urruntime.StateReadyToPublish || record.Discovery == nil || record.Editor == nil {
			return nil, fmt.Errorf("%s/%s is not a materialized READY_TO_PUBLISH event", ref.Source, ref.SourceItemID)
		}
		if !editorOutputPublishes(record.Editor.Output) {
			return nil, fmt.Errorf("%s/%s has no persisted Editor PUBLISH decision", ref.Source, ref.SourceItemID)
		}
		result = append(result, content.ReadyEvent{Item: record.Item, Discovery: *record.Discovery, Editor: *record.Editor})
	}
	return result, nil
}

func editorOutputPublishes(raw json.RawMessage) bool {
	var value struct {
		Decisions []struct {
			Decision string `json:"decision"`
		} `json:"decisions"`
	}
	return json.Unmarshal(raw, &value) == nil && len(value.Decisions) == 1 && value.Decisions[0].Decision == "PUBLISH"
}

func (r *PostgresStore) SaveContentDraft(ctx context.Context, draft content.Draft) error {
	warnings, err := json.Marshal(draft.FactWarnings)
	if err != nil {
		return err
	}
	usage, err := json.Marshal(draft.Usage)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO content_drafts
(source,source_item_id,schema_version,style_version,platform,event_type,hook,body,closing,
 source_label,source_url,post_text,fact_warnings,human_review_required,human_review_status,
 source_input_hash,editor_policy_id,editor_input_hash,model,provider,generated_at,usage,raw_output)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
ON CONFLICT (source,source_item_id,style_version,platform,source_input_hash) DO UPDATE SET
 event_type=EXCLUDED.event_type,hook=EXCLUDED.hook,body=EXCLUDED.body,closing=EXCLUDED.closing,
 source_label=EXCLUDED.source_label,source_url=EXCLUDED.source_url,post_text=EXCLUDED.post_text,
 fact_warnings=EXCLUDED.fact_warnings,human_review_required=EXCLUDED.human_review_required,
 model=EXCLUDED.model,provider=EXCLUDED.provider,generated_at=EXCLUDED.generated_at,
 usage=EXCLUDED.usage,raw_output=EXCLUDED.raw_output
WHERE content_drafts.human_review_status='PENDING'`,
		draft.Source, draft.SourceItemID, draft.SchemaVersion, draft.StyleVersion, draft.Platform,
		draft.EventType, draft.Hook, draft.Body, draft.Closing, draft.SourceLabel, draft.SourceURL,
		draft.PostText, warnings, draft.HumanReviewRequired, draft.HumanReviewStatus,
		draft.SourceInputHash, draft.EditorPolicyID, draft.EditorInputHash, draft.Model, draft.Provider,
		draft.GeneratedAt, usage, draft.RawOutput)
	return err
}

func (r *PostgresStore) FindContentDraft(ctx context.Context, sourceName, sourceItemID, styleVersion, platform, sourceInputHash string) (content.Draft, bool, error) {
	draft, err := scanContentDraft(r.db.QueryRowContext(ctx, contentDraftSelect+` WHERE source=$1 AND source_item_id=$2 AND style_version=$3 AND platform=$4 AND source_input_hash=$5`,
		sourceName, sourceItemID, styleVersion, platform, sourceInputHash))
	if errors.Is(err, sql.ErrNoRows) {
		return content.Draft{}, false, nil
	}
	if err != nil {
		return content.Draft{}, false, err
	}
	return draft, true, nil
}

const contentDraftSelect = `SELECT content_draft_id,source,source_item_id,schema_version,style_version,platform,event_type,hook,body,closing,
source_label,source_url,post_text,fact_warnings,human_review_required,human_review_status,human_review_notes,
approved_at,approved_by,approved_content_hash,rejected_at,rejected_by,source_input_hash,
review_notified_at,review_notification_channel,review_notification_external_id,
editor_policy_id,editor_input_hash,model,provider,generated_at,usage,raw_output FROM content_drafts`

func scanContentDraft(row rowScanner) (content.Draft, error) {
	var draft content.Draft
	var warnings, usage, raw []byte
	var notes, approvedBy, approvedHash, rejectedBy, notificationChannel, notificationExternalID sql.NullString
	var approvedAt, rejectedAt, notifiedAt sql.NullTime
	err := row.Scan(&draft.ContentDraftID, &draft.Source, &draft.SourceItemID, &draft.SchemaVersion, &draft.StyleVersion,
		&draft.Platform, &draft.EventType, &draft.Hook, &draft.Body, &draft.Closing, &draft.SourceLabel,
		&draft.SourceURL, &draft.PostText, &warnings, &draft.HumanReviewRequired, &draft.HumanReviewStatus,
		&notes, &approvedAt, &approvedBy, &approvedHash, &rejectedAt, &rejectedBy, &draft.SourceInputHash, &notifiedAt, &notificationChannel, &notificationExternalID,
		&draft.EditorPolicyID, &draft.EditorInputHash, &draft.Model, &draft.Provider, &draft.GeneratedAt, &usage, &raw)
	if err != nil {
		return content.Draft{}, err
	}
	if err := json.Unmarshal(warnings, &draft.FactWarnings); err != nil {
		return content.Draft{}, err
	}
	if err := json.Unmarshal(usage, &draft.Usage); err != nil {
		return content.Draft{}, err
	}
	draft.RawOutput = raw
	if notes.Valid {
		draft.HumanReviewNotes = notes.String
	}
	if approvedAt.Valid {
		value := approvedAt.Time
		draft.ApprovedAt = &value
	}
	if approvedBy.Valid {
		draft.ApprovedBy = approvedBy.String
	}
	if approvedHash.Valid {
		draft.ApprovedContentHash = approvedHash.String
	}
	if rejectedAt.Valid {
		value := rejectedAt.Time
		draft.RejectedAt = &value
	}
	if rejectedBy.Valid {
		draft.RejectedBy = rejectedBy.String
	}
	if notifiedAt.Valid {
		value := notifiedAt.Time
		draft.ReviewNotifiedAt = &value
	}
	if notificationChannel.Valid {
		draft.ReviewChannel = notificationChannel.String
	}
	if notificationExternalID.Valid {
		draft.ReviewExternalID = notificationExternalID.String
	}
	return draft, nil
}

func (r *PostgresStore) ListPendingReviewNotificationDrafts(ctx context.Context, draftID *int64) ([]content.Draft, error) {
	query := contentDraftSelect + ` WHERE human_review_status='PENDING' AND review_notified_at IS NULL`
	args := []any{}
	if draftID != nil {
		query += ` AND content_draft_id=$1`
		args = append(args, *draftID)
	}
	query += ` ORDER BY generated_at, content_draft_id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var drafts []content.Draft
	for rows.Next() {
		draft, err := scanContentDraft(rows)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

func (r *PostgresStore) MarkReviewNotificationDelivered(ctx context.Context, draftID int64, delivery content.ReviewNotificationDelivery, at time.Time) error {
	if draftID <= 0 || delivery.Channel == "" || delivery.ExternalID == "" {
		return content.ErrInvalidReviewNotification
	}
	result, err := r.db.ExecContext(ctx, `UPDATE content_drafts SET review_notified_at=$2,review_notification_channel=$3,review_notification_external_id=$4 WHERE content_draft_id=$1 AND human_review_status='PENDING' AND review_notified_at IS NULL`, draftID, at, delivery.Channel, delivery.ExternalID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return content.ErrReviewNotificationSent
	}
	return nil
}

// TransitionContentDraftReview serializes competing callbacks with SELECT FOR
// UPDATE. Same-decision replay is idempotent; terminal state changes fail
// closed and never rewrite the audit record.
func (r *PostgresStore) TransitionContentDraftReview(ctx context.Context, draftID int64, decision content.ReviewDecision, actor string, at time.Time) (content.ReviewResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return content.ReviewResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	draft, err := scanContentDraft(tx.QueryRowContext(ctx, contentDraftSelect+` WHERE content_draft_id=$1 FOR UPDATE`, draftID))
	if errors.Is(err, sql.ErrNoRows) {
		return content.ReviewResult{}, fmt.Errorf("%w: %d", content.ErrReviewDraftNotFound, draftID)
	}
	if err != nil {
		return content.ReviewResult{}, err
	}
	if draft.HumanReviewStatus == content.ReviewStatusPending {
		switch decision {
		case content.ReviewApprove:
			draft.HumanReviewStatus = content.ReviewStatusApproved
			draft.ApprovedAt = &at
			draft.ApprovedBy = actor
			draft.ApprovedContentHash = draft.CurrentContentHash()
			_, err = tx.ExecContext(ctx, `UPDATE content_drafts SET human_review_status=$2,approved_at=$3,approved_by=$4,approved_content_hash=$5 WHERE content_draft_id=$1`, draftID, draft.HumanReviewStatus, at, actor, draft.ApprovedContentHash)
		case content.ReviewReject:
			draft.HumanReviewStatus = content.ReviewStatusRejected
			draft.RejectedAt = &at
			draft.RejectedBy = actor
			_, err = tx.ExecContext(ctx, `UPDATE content_drafts SET human_review_status=$2,rejected_at=$3,rejected_by=$4 WHERE content_draft_id=$1`, draftID, draft.HumanReviewStatus, at, actor)
		default:
			return content.ReviewResult{}, fmt.Errorf("%w: %q", content.ErrInvalidReviewRequest, decision)
		}
		if err != nil {
			return content.ReviewResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return content.ReviewResult{}, err
		}
		return content.ReviewResult{Draft: draft, Applied: true}, nil
	}
	if (decision == content.ReviewApprove && draft.HumanReviewStatus == content.ReviewStatusApproved) ||
		(decision == content.ReviewReject && draft.HumanReviewStatus == content.ReviewStatusRejected) {
		if err := tx.Commit(); err != nil {
			return content.ReviewResult{}, err
		}
		return content.ReviewResult{Draft: draft, Applied: false}, nil
	}
	return content.ReviewResult{}, fmt.Errorf("%w: draft %d is %s", content.ErrInvalidReviewTransition, draftID, draft.HumanReviewStatus)
}

const runtimeSelect = `SELECT source,source_item_id,source_url,title,summary,body_text,
published_at_source,retrieved_at,metadata,first_seen_at,last_seen_at,processing_state,
retry_stage,research_rounds,last_error,runtime_data,updated_at,source_fingerprint FROM source_items`

type rowScanner interface{ Scan(...any) error }

func scanRuntime(row rowScanner) (urruntime.ItemRecord, error) {
	record, _, err := scanRuntimeValues(row, false)
	return record, err
}

func scanRuntimeWithInserted(row rowScanner) (urruntime.ItemRecord, bool, error) {
	return scanRuntimeValues(row, true)
}

func scanRuntimeValues(row rowScanner, withInserted bool) (urruntime.ItemRecord, bool, error) {
	var record urruntime.ItemRecord
	var published sql.NullTime
	var metadata, data []byte
	args := []any{&record.Item.Source, &record.Item.SourceItemID, &record.Item.URL,
		&record.Item.Title, &record.Item.Summary, &record.Item.Text, &published,
		&record.Item.RetrievedAt, &metadata, &record.FirstSeenAt, &record.LastSeenAt,
		&record.State, &record.RetryStage, &record.ResearchRounds, &record.LastError,
		&data, &record.UpdatedAt, &record.SourceFingerprint}
	var inserted bool
	if withInserted {
		args = append(args, &inserted)
	}
	if err := row.Scan(args...); err != nil {
		return record, false, err
	}
	if published.Valid {
		record.Item.PublishedAt = published.Time
	}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &record.Item.Metadata); err != nil {
			return record, false, err
		}
	}
	if len(data) > 0 {
		var payload persistedRuntime
		if err := json.Unmarshal(data, &payload); err != nil {
			return record, false, err
		}
		applyPayload(&record, payload)
	}
	return record, inserted, nil
}

type persistedRuntime struct {
	Discovery    *urruntime.StageResult `json:"discovery,omitempty"`
	Editor       *urruntime.StageResult `json:"editor,omitempty"`
	Research     *urruntime.StageResult `json:"research,omitempty"`
	EditorReeval *urruntime.StageResult `json:"editor_reevaluation,omitempty"`
	Usage        urruntime.Usage        `json:"usage"`
}

func runtimePayload(record urruntime.ItemRecord) persistedRuntime {
	return persistedRuntime{record.Discovery, record.Editor, record.Research, record.EditorReeval, record.Usage}
}
func applyPayload(record *urruntime.ItemRecord, value persistedRuntime) {
	record.Discovery, record.Editor, record.Research, record.EditorReeval, record.Usage = value.Discovery, value.Editor, value.Research, value.EditorReeval, value.Usage
}
func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
