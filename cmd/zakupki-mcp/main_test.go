package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolSchemasExposeTrustedSourceURL(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := newServer(nil).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	result, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := map[string]string{
		"get_procurement":              "",
		"list_procurement_documents":   "",
		"get_procurement_document":     "document_id",
		"list_procurement_attachments": "",
		"get_procurement_attachment":   "attachment_id",
	}
	if len(result.Tools) != len(wantIDs) {
		t.Fatalf("tools=%d, want %d", len(result.Tools), len(wantIDs))
	}
	for _, tool := range result.Tools {
		id, ok := wantIDs[tool.Name]
		if !ok {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s schema marshal: %v", tool.Name, err)
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("%s schema decode: %v", tool.Name, err)
		}
		if _, ok := schema.Properties["source_url"]; !ok {
			t.Errorf("%s does not expose optional source_url", tool.Name)
		}
		if !contains(schema.Required, "registry_id") {
			t.Errorf("%s does not require registry_id", tool.Name)
		}
		if contains(schema.Required, "source_url") {
			t.Errorf("%s unexpectedly requires source_url", tool.Name)
		}
		if id != "" && !contains(schema.Required, id) {
			t.Errorf("%s does not require %s", tool.Name, id)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
