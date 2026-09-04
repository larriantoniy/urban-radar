package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	row := r.db.QueryRowContext(ctx, `INSERT INTO source_items
(source,source_item_id,source_url,title,summary,body_text,published_at_source,retrieved_at,
 first_seen_at,last_seen_at,processing_state,metadata)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,'RECEIVED',$10)
ON CONFLICT (source,source_item_id) DO UPDATE SET
 source_url=EXCLUDED.source_url,title=EXCLUDED.title,summary=EXCLUDED.summary,
 body_text=EXCLUDED.body_text,published_at_source=EXCLUDED.published_at_source,
 retrieved_at=EXCLUDED.retrieved_at,last_seen_at=EXCLUDED.last_seen_at,
 metadata=EXCLUDED.metadata,updated_at=now()
RETURNING source,source_item_id,source_url,title,summary,body_text,published_at_source,
 retrieved_at,metadata,first_seen_at,last_seen_at,processing_state,retry_stage,
 research_rounds,last_error,runtime_data,updated_at,(xmax = 0)`,
		item.Source, item.SourceItemID, item.URL, item.Title, item.Summary, item.Text,
		nullTime(item.PublishedAt), item.RetrievedAt, seenAt, metadata)
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
 runtime_data=$7,updated_at=$8
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
	_, err = r.db.ExecContext(ctx, `UPDATE news_check_runs SET finished_at=$2,status=$3,summary=$4 WHERE run_id=$1`, summary.RunID, summary.FinishedAt, summary.Status, data)
	return err
}

const runtimeSelect = `SELECT source,source_item_id,source_url,title,summary,body_text,
published_at_source,retrieved_at,metadata,first_seen_at,last_seen_at,processing_state,
retry_stage,research_rounds,last_error,runtime_data,updated_at FROM source_items`

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
		&data, &record.UpdatedAt}
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
