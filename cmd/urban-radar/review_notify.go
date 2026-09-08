package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"urban-radar/content"
	"urban-radar/storage"
)

const reviewNotifyCommandEnv = "URBAN_RADAR_REVIEW_NOTIFY_COMMAND"

type reviewNotifyBridgeResponse struct {
	Channel    string `json:"channel"`
	ExternalID string `json:"external_id"`
}

// cliReviewNotificationSender is deliberately a one-shot process bridge to
// the Hermes plugin-owned PTB sender. It never shells out through a command
// string, so callback data and draft text remain exact argument/JSON values.
type cliReviewNotificationSender struct{ command string }

func (s cliReviewNotificationSender) SendReviewNotification(ctx context.Context, notification content.ReviewNotification) (content.ReviewNotificationDelivery, error) {
	payload, err := json.Marshal(notification)
	if err != nil {
		return content.ReviewNotificationDelivery{}, err
	}
	command := exec.CommandContext(ctx, s.command)
	command.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return content.ReviewNotificationDelivery{}, fmt.Errorf("Hermes review notification sender failed: %w", err)
	}
	var response reviewNotifyBridgeResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return content.ReviewNotificationDelivery{}, fmt.Errorf("Hermes review notification sender returned malformed JSON: %w", err)
	}
	if strings.TrimSpace(response.Channel) == "" || strings.TrimSpace(response.ExternalID) == "" {
		return content.ReviewNotificationDelivery{}, fmt.Errorf("Hermes review notification sender did not confirm delivery")
	}
	return content.ReviewNotificationDelivery{Channel: response.Channel, ExternalID: response.ExternalID}, nil
}

type reviewNotifyCommandOutput struct {
	Results []content.ReviewNotificationResult `json:"results"`
	Error   string                             `json:"error,omitempty"`
}

func contentReviewNotify(args []string) {
	flags := flag.NewFlagSet("content review-notify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	draftIDText := flags.String("draft-id", "", "specific content draft ID")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		reviewNotifyFail("usage: urban-radar content review-notify [--draft-id <id>]")
	}
	var draftID *int64
	if *draftIDText != "" {
		parsed, err := strconv.ParseInt(*draftIDText, 10, 64)
		if err != nil || parsed <= 0 {
			reviewNotifyFail("draft-id must be a positive int64")
		}
		draftID = &parsed
	}
	command := strings.TrimSpace(os.Getenv(reviewNotifyCommandEnv))
	if command == "" {
		reviewNotifyFail(reviewNotifyCommandEnv + " is not set")
	}
	databaseURL, err := storage.DatabaseURLFromEnv()
	if err != nil {
		reviewNotifyFail(err.Error())
	}
	db, err := storage.OpenPostgres(context.Background(), databaseURL)
	if err != nil {
		reviewNotifyFail(err.Error())
	}
	defer db.Close()
	service := content.ReviewNotificationService{
		Store:  storage.NewPostgresStore(db),
		Sender: cliReviewNotificationSender{command: command},
	}
	results, err := service.Notify(context.Background(), draftID)
	if err != nil {
		reviewNotifyFail(err.Error())
	}
	output := reviewNotifyCommandOutput{Results: results}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		fail(err.Error())
	}
	for _, result := range results {
		if result.Status == "ERROR" {
			fail("one or more review notifications failed")
		}
	}
}

func reviewNotifyFail(message string) {
	_ = json.NewEncoder(os.Stdout).Encode(reviewNotifyCommandOutput{Error: message})
	fail(message)
}
