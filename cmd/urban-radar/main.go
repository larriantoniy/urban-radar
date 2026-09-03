package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"urban-radar/tgl"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "tgl" {
		fail("usage: urban-radar tgl list | urban-radar tgl get <url>")
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
		fail("usage: urban-radar tgl list | urban-radar tgl get <url>")
	}
	if err != nil {
		fail(err.Error())
	}
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fail(err.Error())
	}
}
func fail(message string) { fmt.Fprintln(os.Stderr, "urban-radar:", message); os.Exit(1) }
