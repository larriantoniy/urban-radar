package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
	return "usage: urban-radar tgl list | urban-radar tgl get <url> | urban-radar news check [--preflight] [flags] | urban-radar content experiment-v1 [--item source/source_item_id] [flags]"
}

func fail(message string) { fmt.Fprintln(os.Stderr, "urban-radar:", message); os.Exit(1) }
