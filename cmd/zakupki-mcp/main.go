// Command zakupki-mcp exposes bounded primary-source EIS evidence tools.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"urban-radar/zakupki"
)

type registryInput struct {
	RegistryID string `json:"registry_id" jsonschema:"EIS registry number"`
	SourceURL  string `json:"source_url,omitempty" jsonschema:"canonical URL from a trusted SourceItem, optional"`
}
type documentInput struct {
	RegistryID string `json:"registry_id" jsonschema:"EIS registry number"`
	SourceURL  string `json:"source_url,omitempty" jsonschema:"canonical URL from a trusted SourceItem, optional"`
	DocumentID string `json:"document_id" jsonschema:"document_id returned by list_procurement_documents"`
}
type attachmentInput struct {
	RegistryID   string `json:"registry_id" jsonschema:"EIS registry number"`
	SourceURL    string `json:"source_url,omitempty" jsonschema:"canonical URL from a trusted SourceItem, optional"`
	AttachmentID string `json:"attachment_id" jsonschema:"attachment_id returned by list_procurement_attachments"`
}

func main() {
	log.SetOutput(os.Stderr)
	client, err := zakupki.NewHTTPClientFromEnv(20 * time.Second)
	if err != nil {
		log.Printf("zakupki-mcp: %v", err)
		return
	}
	access := zakupki.NewProcurementAccess(client)
	server := mcp.NewServer(&mcp.Implementation{Name: "urban-radar-zakupki", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "get_procurement", Description: "Get factual procurement card data from zakupki.gov.ru."}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryInput) (*mcp.CallToolResult, any, error) {
		v, err := access.GetProcurementRef(ctx, zakupki.ProcurementRef{RegistryID: in.RegistryID, SourceURL: in.SourceURL})
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(v), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_procurement_documents", Description: "List primary document links exposed by one procurement card."}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryInput) (*mcp.CallToolResult, any, error) {
		v, err := access.ListDocumentsRef(ctx, zakupki.ProcurementRef{RegistryID: in.RegistryID, SourceURL: in.SourceURL})
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(v), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_procurement_document", Description: "Fetch and extract bounded text from a listed procurement document."}, func(ctx context.Context, _ *mcp.CallToolRequest, in documentInput) (*mcp.CallToolResult, any, error) {
		v, err := access.GetDocumentRef(ctx, zakupki.ProcurementRef{RegistryID: in.RegistryID, SourceURL: in.SourceURL}, in.DocumentID)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(v), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_procurement_attachments", Description: "List trusted downloadable attachments exposed by a procurement card."}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryInput) (*mcp.CallToolResult, any, error) {
		v, err := access.ListAttachmentsRef(ctx, zakupki.ProcurementRef{RegistryID: in.RegistryID, SourceURL: in.SourceURL})
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(v), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_procurement_attachment", Description: "Download and extract bounded text from a trusted DOCX or XLSX attachment."}, func(ctx context.Context, _ *mcp.CallToolRequest, in attachmentInput) (*mcp.CallToolResult, any, error) {
		v, err := access.GetAttachmentRef(ctx, zakupki.ProcurementRef{RegistryID: in.RegistryID, SourceURL: in.SourceURL}, in.AttachmentID)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(v), nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("zakupki-mcp: %v", err)
	}
}

func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}
func toolJSON(value any) *mcp.CallToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return toolError(fmt.Errorf("encode tool result: %w", err))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}
