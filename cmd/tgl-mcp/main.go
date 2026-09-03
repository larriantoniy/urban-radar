// Command tgl-mcp exposes Urban Radar's tgl.ru reader as a stdio MCP server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"urban-radar/tgl"
)

type listInput struct {
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of newest news items to return; omit for all available items"`
	Since string `json:"since,omitempty" jsonschema:"only items published on or after this ISO date (YYYY-MM-DD)"`
}

type getInput struct {
	URL string `json:"url" jsonschema:"absolute https://tgl.ru/news/item/... URL"`
}

func main() {
	// The MCP stdio transport owns stdout; never write logs there.
	log.SetOutput(os.Stderr)
	server := mcp.NewServer(&mcp.Implementation{Name: "urban-radar-tgl", Version: "0.1.0"}, nil)
	client := tgl.NewClient(15 * time.Second)

	mcp.AddTool(server, &mcp.Tool{
		Name: "tgl_list_news", Description: "Get current city news from the official tgl.ru website.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input listInput) (*mcp.CallToolResult, any, error) {
		news, err := client.ListNews(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		if input.Since != "" {
			since, err := time.Parse("2006-01-02", input.Since)
			if err != nil {
				return toolError(fmt.Errorf("since must use YYYY-MM-DD: %w", err)), nil, nil
			}
			filtered := news[:0]
			for _, item := range news {
				published, err := time.Parse("2006-01-02", item.PublishedAt)
				if err == nil && !published.Before(since) {
					filtered = append(filtered, item)
				}
			}
			news = filtered
		}
		if input.Limit < 0 {
			return toolError(fmt.Errorf("limit must be zero or positive")), nil, nil
		}
		if input.Limit > 0 && input.Limit < len(news) {
			news = news[:input.Limit]
		}
		return toolJSON(news), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "tgl_get_news", Description: "Get the full text of one official tgl.ru news item.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input getInput) (*mcp.CallToolResult, any, error) {
		article, err := client.GetNews(ctx, input.URL)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(article), nil, nil
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("tgl-mcp: %v", err)
	}
}

func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

func toolJSON(value any) *mcp.CallToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return toolError(err)
	}
	// Hermes 0.21.0's bundled MCP client validates structuredContent as an
	// object. Sending compact JSON text preserves the required top-level array
	// for tgl_list_news without wrapping or changing the tool contract.
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}
