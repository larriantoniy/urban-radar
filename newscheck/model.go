// Package newscheck coordinates incremental multi-source collection batches.
package newscheck

import (
	"context"
	"errors"
	"time"

	urruntime "urban-radar/runtime"
	"urban-radar/source"
)

const (
	DefaultOverlap           = 24 * time.Hour
	DefaultBootstrapLookback = 48 * time.Hour
	DefaultMaxPages          = 100
)

var ErrAlreadyRunning = errors.New("a news check is already running")

type Window struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	MaxPages int       `json:"max_pages"`
}

type Collection struct {
	Items     []source.SourceItem
	PagesRead int
	Complete  bool
}

type Collector interface {
	Name() string
	Collect(context.Context, Window) (Collection, error)
}

type Store interface {
	AcquireNewsCheck(context.Context) (func(context.Context) error, error)
	StartNewsCheck(context.Context, string, time.Time) error
	FinishNewsCheck(context.Context, Summary) error
	GetCheckpoint(context.Context, string) (time.Time, bool, error)
	AdvanceCheckpoint(context.Context, string, time.Time) error
	Get(context.Context, string, string) (urruntime.ItemRecord, error)
	UpsertSeen(context.Context, source.SourceItem, time.Time) (urruntime.ItemRecord, bool, error)
	ListProcessable(context.Context) ([]urruntime.ItemRecord, error)
}

type Processor interface {
	Process(context.Context, source.SourceItem) urruntime.ProcessResult
}

type SourceSummary struct {
	Status             string    `json:"status"`
	WindowFrom         time.Time `json:"window_from"`
	WindowTo           time.Time `json:"window_to"`
	PagesRead          int       `json:"pages_read"`
	Received           int       `json:"received"`
	Unique             int       `json:"unique"`
	New                int       `json:"new"`
	Known              int       `json:"known"`
	Retryable          int       `json:"retryable"`
	Errors             int       `json:"errors"`
	CheckpointAdvanced bool      `json:"checkpoint_advanced"`
	Error              string    `json:"error,omitempty"`
}

type PipelineSummary struct {
	Processable         int `json:"processable"`
	Completed           int `json:"completed"`
	Ignored             int `json:"ignored"`
	ReadyToPublish      int `json:"ready_to_publish"`
	ProjectAction       int `json:"project_action"`
	ResearchExhausted   int `json:"research_exhausted"`
	ResearchUnsupported int `json:"research_unsupported"`
	Errors              int `json:"errors"`
}

type Summary struct {
	RunID      string                   `json:"run_id"`
	Status     string                   `json:"status"`
	RunStatus  string                   `json:"run_status,omitempty"`
	StartedAt  time.Time                `json:"started_at"`
	FinishedAt time.Time                `json:"finished_at"`
	Config     ConfigSummary            `json:"config"`
	Sources    map[string]SourceSummary `json:"sources"`
	Pipeline   PipelineSummary          `json:"pipeline"`
	Usage      urruntime.Usage          `json:"usage"`
}

type ConfigSummary struct {
	OverlapSeconds           int64  `json:"overlap_seconds"`
	BootstrapLookbackSeconds int64  `json:"bootstrap_lookback_seconds"`
	MaxPages                 int    `json:"max_pages"`
	DiscoveryTimeoutSeconds  int64  `json:"discovery_timeout_seconds"`
	ProcessingOrder          string `json:"processing_order"`
	Model                    string `json:"model,omitempty"`
	Provider                 string `json:"provider,omitempty"`
}

type Config struct {
	Overlap           time.Duration
	BootstrapLookback time.Duration
	MaxPages          int
	Model             string
	Provider          string
	DiscoveryTimeout  time.Duration
}

func (c Config) withDefaults() Config {
	if c.Overlap <= 0 {
		c.Overlap = DefaultOverlap
	}
	if c.BootstrapLookback <= 0 {
		c.BootstrapLookback = DefaultBootstrapLookback
	}
	if c.MaxPages <= 0 {
		c.MaxPages = DefaultMaxPages
	}
	return c
}
