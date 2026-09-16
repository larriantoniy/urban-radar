package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"urban-radar/content"
	urruntime "urban-radar/runtime"
	"urban-radar/storage"
)

func contentProcessReady(args []string) {
	flags := flag.NewFlagSet("content process-ready", flag.ContinueOnError)
	model := flags.String("model", "deepseek/deepseek-v4-flash-0731", "Hermes model")
	provider := flags.String("provider", "openrouter", "Hermes provider")
	root := flags.String("repository-root", ".", "repository root containing agents/ and .hermes/skills/")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fail("usage: urban-radar content process-ready [flags]")
	}
	ctx := context.Background()
	databaseURL, err := storage.DatabaseURLFromEnv()
	if err != nil {
		fail("content process-ready requires persistent PostgreSQL: " + err.Error())
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
	command := strings.TrimSpace(os.Getenv(reviewNotifyCommandEnv))
	if command == "" {
		fail(reviewNotifyCommandEnv + " is not set")
	}
	store := storage.NewPostgresStore(db)
	notifier := content.ReviewNotificationService{Store: store, Sender: cliReviewNotificationSender{command: command}}
	summary, err := (content.ReadyProcessor{Store: store, Agent: agents, Notifier: notifier, Model: *model, Provider: *provider}).Process(ctx)
	if encodeErr := json.NewEncoder(os.Stdout).Encode(summary); encodeErr != nil {
		fail(encodeErr.Error())
	}
	if err != nil {
		fail(err.Error())
	}
	if len(summary.Errors) > 0 {
		fail(fmt.Sprintf("content process-ready completed with %d errors", len(summary.Errors)))
	}
}
