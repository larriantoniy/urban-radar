package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"urban-radar/content"
)

func TestCLIReviewNotificationSenderUsesExactJSONBridge(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.json")
	commandPath := filepath.Join(dir, "sender")
	script := "#!/bin/sh\ncat > \"$URBAN_RADAR_TEST_INPUT\"\nprintf '%s\\n' '{\"channel\":\"telegram:1001\",\"external_id\":\"77\"}'\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("URBAN_RADAR_TEST_INPUT", inputPath)
	notification := content.ReviewNotification{DraftID: 42, Text: "text", SourceURL: "https://example.test/source", ApproveCallbackData: "ur:approve:42", RejectCallbackData: "ur:reject:42"}
	delivery, err := (cliReviewNotificationSender{command: commandPath}).SendReviewNotification(context.Background(), notification)
	if err != nil || delivery.Channel != "telegram:1001" || delivery.ExternalID != "77" {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
	payload, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"content_draft_id":42`, `"approve_callback_data":"ur:approve:42"`, `"reject_callback_data":"ur:reject:42"`} {
		if !strings.Contains(string(payload), required) {
			t.Fatalf("bridge payload missing %s: %s", required, payload)
		}
	}
}

func TestCLIReviewNotificationSenderFailsClosed(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"non-zero":   "#!/bin/sh\nexit 1\n",
		"malformed":  "#!/bin/sh\nprintf 'not-json\\n'\n",
		"incomplete": "#!/bin/sh\nprintf '%s\\n' '{\"channel\":\"telegram:1001\"}'\n",
	} {
		t.Run(name, func(t *testing.T) {
			commandPath := filepath.Join(dir, name)
			if err := os.WriteFile(commandPath, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			_, err := (cliReviewNotificationSender{command: commandPath}).SendReviewNotification(context.Background(), content.ReviewNotification{DraftID: 1})
			if err == nil {
				t.Fatal("expected fail-closed sender error")
			}
		})
	}
}
