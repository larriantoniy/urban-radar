// Package runtime contains the deterministic per-item editorial state machine.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"urban-radar/source"
)

const MaxResearchRounds = 1

type State string

const (
	StateReceived            State = "RECEIVED"
	StateDiscovery           State = "DISCOVERY"
	StateEditor              State = "EDITOR"
	StateResearch            State = "RESEARCH"
	StateEditorReevaluation  State = "EDITOR_REEVALUATION"
	StateDiscoveryDropped    State = "DISCOVERY_DROPPED"
	StateIgnored             State = "IGNORED"
	StateReadyToPublish      State = "READY_TO_PUBLISH"
	StateProjectAction       State = "PROJECT_ACTION"
	StateResearchExhausted   State = "RESEARCH_EXHAUSTED"
	StateResearchUnsupported State = "RESEARCH_UNSUPPORTED"
	StateError               State = "ERROR"
)

func (s State) Terminal() bool {
	switch s {
	case StateDiscoveryDropped, StateIgnored, StateReadyToPublish, StateProjectAction, StateResearchExhausted, StateResearchUnsupported:
		return true
	default:
		return false
	}
}

type Usage struct {
	APICalls         int     `json:"api_calls"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

func (u *Usage) Add(other Usage) {
	u.APICalls += other.APICalls
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.TotalTokens += other.TotalTokens
	u.EstimatedCostUSD += other.EstimatedCostUSD
}

type StageResult struct {
	PolicyID  string          `json:"policy_id"`
	InputHash string          `json:"input_hash"`
	Output    json.RawMessage `json:"output"`
	Usage     Usage           `json:"usage"`
	At        time.Time       `json:"at"`
}

type ItemRecord struct {
	Item source.SourceItem `json:"item"`
	// SourceFingerprint records the most recently observed canonical source
	// payload. A terminal item is never automatically reopened in Runtime V0,
	// but this makes a later source-side change visible without retaining a
	// dropped item's heavy payload.
	SourceFingerprint string       `json:"source_fingerprint,omitempty"`
	FirstSeenAt       time.Time    `json:"first_seen_at"`
	LastSeenAt        time.Time    `json:"last_seen_at"`
	State             State        `json:"state"`
	RetryStage        State        `json:"retry_stage,omitempty"`
	ResearchRounds    int          `json:"research_rounds"`
	LastError         string       `json:"last_error,omitempty"`
	Discovery         *StageResult `json:"discovery,omitempty"`
	Editor            *StageResult `json:"editor,omitempty"`
	Research          *StageResult `json:"research,omitempty"`
	EditorReeval      *StageResult `json:"editor_reevaluation,omitempty"`
	Usage             Usage        `json:"usage"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

type Store interface {
	Get(context.Context, string, string) (ItemRecord, error)
	Save(context.Context, ItemRecord) error
}

var ErrNotFound = errors.New("runtime item not found")

type DiscoveryOutcome struct {
	Candidate bool
	Output    json.RawMessage
}

type EditorOutcome struct {
	Decision           string
	Importance         float64
	Reason             string
	MissingInformation []string
	Output             json.RawMessage
}

type ResearchRequest struct {
	Source             string   `json:"source"`
	SourceItemID       string   `json:"source_item_id"`
	SourceURL          string   `json:"source_url"`
	ResearchGoal       string   `json:"research_goal"`
	MissingInformation []string `json:"missing_information"`
}

type ResearchOutcome struct {
	Output json.RawMessage
}

type AgentExecutor interface {
	Discover(context.Context, source.SourceItem) (DiscoveryOutcome, Usage, error)
	Edit(context.Context, source.SourceItem, json.RawMessage, json.RawMessage) (EditorOutcome, Usage, error)
	Research(context.Context, ResearchRequest) (ResearchOutcome, Usage, error)
}

type Policies struct {
	Discovery string
	Editor    string
	Research  string
}

type ProcessResult struct {
	Source       string `json:"source"`
	SourceItemID string `json:"source_item_id"`
	State        State  `json:"state"`
	Usage        Usage  `json:"usage"`
	Error        string `json:"error,omitempty"`
}
