package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"urban-radar/content"
	"urban-radar/newscheck"
	urruntime "urban-radar/runtime"
	"urban-radar/storage"
	"urban-radar/tgl"
	"urban-radar/zakupki"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "news" && os.Args[2] == "check" {
		checkNews(os.Args[3:])
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "content" && os.Args[2] == "experiment-v1" {
		contentExperimentV1(os.Args[3:])
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "content" && os.Args[2] == "review" {
		contentReview(os.Args[3:])
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "content" && os.Args[2] == "review-notify" {
		contentReviewNotify(os.Args[3:])
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "media" && os.Args[2] == "attach" {
		mediaAttach(os.Args[3:])
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "media" && os.Args[2] == "cleanup" {
		mediaCleanup(os.Args[3:])
		return
	}
	if len(os.Args) < 3 || os.Args[1] != "tgl" {
		fail(usage())
	}
	client := tgl.NewClient(15 * time.Second)
	ctx := context.Background()
	var value any
	var err error
	switch os.Args[2] {
	case "list":
		if len(os.Args) != 3 {
			fail("usage: urban-radar tgl list")
		}
		value, err = client.ListNews(ctx)
	case "get":
		if len(os.Args) != 4 {
			fail("usage: urban-radar tgl get <url>")
		}
		value, err = client.GetNews(ctx, os.Args[3])
	default:
		fail(usage())
	}
	if err != nil {
		fail(err.Error())
	}
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fail(err.Error())
	}
}

func contentExperimentV1(args []string) {
	flags := flag.NewFlagSet("content experiment-v1", flag.ContinueOnError)
	model := flags.String("model", "deepseek/deepseek-v4-flash-0731", "Hermes model")
	provider := flags.String("provider", "openrouter", "Hermes provider")
	root := flags.String("repository-root", ".", "repository root containing agents/ and .hermes/skills/")
	var items sourceRefFlags
	flags.Var(&items, "item", "READY_TO_PUBLISH source item as source/source_item_id; repeatable")
	if err := flags.Parse(args); err != nil {
		fail(err.Error())
	}
	if flags.NArg() != 0 {
		fail("usage: urban-radar content experiment-v1 [--item source/source_item_id] [flags]")
	}
	ctx := context.Background()
	databaseURL, err := storage.DatabaseURLFromEnv()
	if err != nil {
		fail("content experiment requires persistent PostgreSQL: " + err.Error())
	}
	db, err := storage.OpenPostgres(ctx, databaseURL)
	if err != nil {
		fail(err.Error())
	}
	defer db.Close()
	repositoryRoot, err := filepath.Abs(*root)
	if err != nil {
		fail(err.Error())
	}
	agents, err := urruntime.NewHermesExecutor(repositoryRoot, *model, *provider)
	if err != nil {
		fail("load content agent: " + err.Error())
	}
	experiment := content.Experiment{Store: storage.NewPostgresStore(db), Agent: agents, Model: *model, Provider: *provider}
	var summary content.Summary
	if len(items) == 0 {
		summary, err = experiment.RunV1(ctx)
	} else {
		summary, err = experiment.Run(ctx, []content.SourceRef(items))
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(summary); encodeErr != nil {
		fail(encodeErr.Error())
	}
	if err != nil {
		fail(err.Error())
	}
}

type reviewCommandOutput struct {
	Result      string `json:"result"`
	DraftID     int64  `json:"draft_id,omitempty"`
	ReviewState string `json:"review_state,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Error       string `json:"error,omitempty"`
}

func contentReview(args []string) {
	if len(args) < 2 {
		reviewFail("INVALID_ARGUMENTS", "usage: urban-radar content review approve|reject <draft-id> --actor <actor>")
	}
	action, draftIDText := args[0], args[1]
	draftID, err := strconv.ParseInt(draftIDText, 10, 64)
	if err != nil || draftID <= 0 {
		reviewFail("INVALID_ARGUMENTS", "draft-id must be a positive int64")
	}
	flags := flag.NewFlagSet("content review", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	actor := flags.String("actor", "", "deterministic review actor")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*actor) == "" {
		reviewFail("INVALID_ARGUMENTS", "usage: urban-radar content review approve|reject <draft-id> --actor <actor>")
	}
	ctx := context.Background()
	databaseURL, err := storage.DatabaseURLFromEnv()
	if err != nil {
		reviewFail("DATABASE_ERROR", err.Error())
	}
	output, err := executeContentReview(ctx, databaseURL, action, draftID, *actor)
	if err != nil {
		reviewFail(reviewErrorCode(err), err.Error())
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		fail(err.Error())
	}
}

func executeContentReview(ctx context.Context, databaseURL, action string, draftID int64, actor string) (reviewCommandOutput, error) {
	db, err := storage.OpenPostgres(ctx, databaseURL)
	if err != nil {
		return reviewCommandOutput{}, err
	}
	defer db.Close()
	service := content.ReviewService{Store: storage.NewPostgresStore(db)}
	var result content.ReviewResult
	switch action {
	case "approve":
		result, err = service.ApproveDraft(ctx, draftID, actor)
	case "reject":
		result, err = service.RejectDraft(ctx, draftID, actor)
	default:
		return reviewCommandOutput{}, fmt.Errorf("%w: action must be approve or reject", content.ErrInvalidReviewRequest)
	}
	if err != nil {
		return reviewCommandOutput{}, err
	}
	status := "APPLIED"
	if !result.Applied {
		status = "IDEMPOTENT"
	}
	return reviewCommandOutput{Result: status, DraftID: result.Draft.ContentDraftID, ReviewState: result.Draft.HumanReviewStatus}, nil
}

func reviewErrorCode(err error) string {
	switch {
	case errors.Is(err, content.ErrReviewDraftNotFound):
		return "DRAFT_NOT_FOUND"
	case errors.Is(err, content.ErrInvalidReviewTransition):
		return "INVALID_TRANSITION"
	case errors.Is(err, content.ErrInvalidReviewRequest):
		return "INVALID_ARGUMENTS"
	default:
		return "DATABASE_ERROR"
	}
}

func reviewFail(code, message string) {
	_ = json.NewEncoder(os.Stdout).Encode(reviewCommandOutput{Result: "ERROR", ErrorCode: code, Error: message})
	fail(message)
}

type sourceRefFlags []content.SourceRef

func (f *sourceRefFlags) String() string { return "" }

func (f *sourceRefFlags) Set(value string) error {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("item must be source/source_item_id")
	}
	*f = append(*f, content.SourceRef{Source: parts[0], SourceItemID: parts[1]})
	return nil
}

func checkNews(args []string) {
	flags := flag.NewFlagSet("news check", flag.ContinueOnError)
	overlap := flags.Duration("overlap", newscheck.DefaultOverlap, "checkpoint overlap window")
	bootstrap := flags.Duration("bootstrap-lookback", newscheck.DefaultBootstrapLookback, "first-run collection window")
	maxPages := flags.Int("max-pages", newscheck.DefaultMaxPages, "per-source pagination safety bound")
	discoveryTimeout := flags.Duration("discovery-timeout", urruntime.DefaultDiscoveryTimeout, "end-to-end timeout for one Discovery invocation")
	preflight := flags.Bool("preflight", false, "collect and inspect without writing state or invoking agents")
	model := flags.String("model", "deepseek/deepseek-v4-flash-0731", "Hermes model")
	provider := flags.String("provider", "openrouter", "Hermes provider")
	root := flags.String("repository-root", ".", "repository root containing agents/")
	if err := flags.Parse(args); err != nil {
		fail(err.Error())
	}
	if *overlap <= 0 || *bootstrap <= 0 || *maxPages <= 0 || *discoveryTimeout <= 0 {
		fail("overlap, bootstrap-lookback, max-pages and discovery-timeout must be positive")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	databaseURL, err := storage.DatabaseURLFromEnv()
	if err != nil {
		fail("news check requires persistent PostgreSQL: " + err.Error())
	}
	db, err := storage.OpenPostgres(ctx, databaseURL)
	if err != nil {
		fail(err.Error())
	}
	defer db.Close()

	stateStore := storage.NewPostgresStore(db)
	zakupkiHTTP, err := zakupki.NewHTTPClientFromEnv(20 * time.Second)
	if err != nil {
		fail(err.Error())
	}
	runner := newscheck.Runner{
		Store: stateStore,
		Collectors: []newscheck.Collector{
			newscheck.TGLCollector{Client: tgl.NewClient(15 * time.Second)},
			newscheck.ZakupkiCollector{Search: zakupki.NewZakupkiSearchHTMLSource(zakupkiHTTP), CardFetcher: zakupki.HTTPCardFetcher{Client: zakupkiHTTP}},
		},
		Config: newscheck.Config{Overlap: *overlap, BootstrapLookback: *bootstrap, MaxPages: *maxPages, Model: *model, Provider: *provider, DiscoveryTimeout: *discoveryTimeout},
	}
	if *preflight {
		summary, err := runner.Preflight(ctx)
		writeSummary(summary, err)
		return
	}
	repositoryRoot, err := filepath.Abs(*root)
	if err != nil {
		fail(err.Error())
	}
	agents, err := urruntime.NewHermesExecutor(repositoryRoot, *model, *provider)
	if err != nil {
		fail("load agent policies: " + err.Error())
	}
	agents.DiscoveryTimeout = *discoveryTimeout
	runner.Processor = &urruntime.Coordinator{
		Store: stateStore, Agents: agents, Policies: agents.Policies(),
		ResearchSupportedSources: map[string]bool{zakupki.SourceName: true},
	}
	summary, err := runner.CheckNews(ctx)
	writeSummary(summary, err)
}

func writeSummary(summary newscheck.Summary, err error) {
	if encodeErr := json.NewEncoder(os.Stdout).Encode(summary); encodeErr != nil {
		fail(encodeErr.Error())
	}
	if err != nil {
		fail(err.Error())
	}
}

func usage() string {
	return "usage: urban-radar tgl list | urban-radar tgl get <url> | urban-radar news check [--preflight] [flags] | urban-radar content experiment-v1 [--item source/source_item_id] [flags] | urban-radar content review approve|reject <draft-id> --actor <actor> | urban-radar content review-notify [--draft-id <id>] | urban-radar media attach <draft-id> --actor <actor> | urban-radar media cleanup [--dry-run]"
}

func mediaAttach(args []string) {
	if len(args) < 1 {
		fail("usage: urban-radar media attach <draft-id> --actor <actor>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		fail("draft-id must be a positive int64")
	}
	f := flag.NewFlagSet("media attach", flag.ContinueOnError)
	actor := f.String("actor", "", "actor")
	if err = f.Parse(args[1:]); err != nil || f.NArg() != 0 || strings.TrimSpace(*actor) == "" {
		fail("usage: urban-radar media attach <draft-id> --actor <actor>")
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 20<<20))
	if err != nil || len(data) == 0 {
		fail("read image bytes")
	}
	u, err := storage.DatabaseURLFromEnv()
	if err != nil {
		fail(err.Error())
	}
	db, err := storage.OpenPostgres(context.Background(), u)
	if err != nil {
		fail(err.Error())
	}
	defer db.Close()
	m, err := content.SaveImage(context.Background(), storage.NewPostgresStore(db), content.MediaRootFromEnv(), id, data, *actor, time.Now().UTC())
	if err != nil {
		fail(err.Error())
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"result": "ATTACHED", "draft_id": id, "storage_path": m.StoragePath, "sha256": m.SHA256})
}
func mediaCleanup(args []string) {
	f := flag.NewFlagSet("media cleanup", flag.ContinueOnError)
	dry := f.Bool("dry-run", false, "report only")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		fail("usage: urban-radar media cleanup [--dry-run]")
	}
	u, err := storage.DatabaseURLFromEnv()
	if err != nil {
		fail(err.Error())
	}
	db, err := storage.OpenPostgres(context.Background(), u)
	if err != nil {
		fail(err.Error())
	}
	defer db.Close()
	n, err := storage.NewPostgresStore(db).CleanupRejectedMedia(context.Background(), time.Now().UTC().Add(-7*24*time.Hour), *dry)
	if err != nil {
		fail(err.Error())
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"count": n, "dry_run": *dry})
}

func fail(message string) { fmt.Fprintln(os.Stderr, "urban-radar:", message); os.Exit(1) }
