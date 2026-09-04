// Command zakupki-review renders the Zakupki dataset for manual labelling.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"urban-radar/zakupki"
)

const (
	inputPath  = "data/evals/zakupki-v0/items.json"
	outputPath = "data/evals/zakupki-v0/HUMAN_REVIEW.md"
)

type evalItem struct {
	ID             string  `json:"id"`
	SourceURL      string  `json:"source_url"`
	Object         string  `json:"object"`
	CustomerName   string  `json:"customer_name"`
	Price          float64 `json:"price"`
	DeliveryPlace  string  `json:"delivery_place"`
	Address        string  `json:"address"`
	Stage          string  `json:"stage"`
	PublishedAt    string  `json:"published_at"`
	CustomerRegion string  `json:"customer_region"`
	HumanLabel     *string `json:"human_label"`
}

func main() {
	input := flag.String("input", inputPath, "input dataset JSON")
	output := flag.String("output", outputPath, "output human-review Markdown")
	flag.Parse()
	items, err := readItems(*input)
	if err != nil {
		fail(err)
	}
	if err := writeReview(*output, renderReview(items)); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %d items to %s\n", len(items), *output)
}

func readItems(path string) ([]evalItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []evalItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func writeReview(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func renderReview(items []evalItem) string {

	var builder strings.Builder
	builder.WriteString("# Zakupki V0 — human review\n\n")
	builder.WriteString("Human label ставится независимо от текущего решения системы.\n\n")
	builder.WriteString("**CANDIDATE** — из закупки потенциально может получиться интересная новость для жителей Тольятти.\n\n")
	builder.WriteString("**SKIP** — закупка локальная, но является рутинной, мелкой или неинтересной для новостной группы.\n\n")
	builder.WriteString("Не использовать `candidate` и `relevance` системы как ground truth.\n\n")

	candidates, labelled := 0, 0
	for _, item := range items {
		p := zakupki.Procurement{ID: item.ID, URL: item.SourceURL, Object: item.Object, CustomerName: item.CustomerName, CustomerRegion: item.CustomerRegion, Price: item.Price, DeliveryPlace: item.DeliveryPlace, Address: item.Address, Stage: item.Stage}
		if zakupki.Evaluate(p).Candidate {
			candidates++
		}
		if item.HumanLabel != nil {
			labelled++
		}
	}
	builder.WriteString("## Summary\n\n")
	fmt.Fprintf(&builder, "- Total items: %d\n- Human labelled: %d\n- System candidates: %d\n- System skipped: %d\n\n", len(items), labelled, candidates, len(items)-candidates)
	builder.WriteString("Metrics are calculated by the separate `go run ./cmd/zakupki-eval` command.\n\n---\n\n")

	for index, item := range items {
		p := zakupki.Procurement{ID: item.ID, URL: item.SourceURL, Object: item.Object, CustomerName: item.CustomerName, CustomerRegion: item.CustomerRegion, Price: item.Price, DeliveryPlace: item.DeliveryPlace, Address: item.Address, Stage: item.Stage}
		result := zakupki.Evaluate(p)
		fmt.Fprintf(&builder, "## %02d — %s\n\n", index+1, item.ID)
		fmt.Fprintf(&builder, "**%s**\n\n", display(item.Object))
		fmt.Fprintf(&builder, "Заказчик: %s\n", display(item.CustomerName))
		fmt.Fprintf(&builder, "Стоимость: %s\n", formatMoney(item.Price))
		fmt.Fprintf(&builder, "Где: %s\n", display(location(item.DeliveryPlace, item.Address)))
		fmt.Fprintf(&builder, "Стадия: %s\n", display(item.Stage))
		fmt.Fprintf(&builder, "Дата публикации: %s\n", display(item.PublishedAt))
		if item.SourceURL != "" {
			fmt.Fprintf(&builder, "Источник: [%s](%s)\n\n", item.SourceURL, item.SourceURL)
		} else {
			builder.WriteString("Источник: —\n\n")
		}
		builder.WriteString("Система:\n\n")
		fmt.Fprintf(&builder, "- relevance: `%s`\n- candidate: `%t`\n- reasons:\n", result.Relevance, result.Candidate)
		if len(result.Reasons) == 0 {
			builder.WriteString("  - —\n")
		} else {
			for _, reason := range result.Reasons {
				fmt.Fprintf(&builder, "  - %s\n", reason)
			}
		}
		builder.WriteString("\n### Human decision\n\nhuman_label: ")
		if item.HumanLabel == nil {
			builder.WriteString("null\n\n")
		} else {
			fmt.Fprintf(&builder, "`%s`\n\n", *item.HumanLabel)
		}
		builder.WriteString("---\n\n")
	}
	return builder.String()
}

func display(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func location(place, address string) string {
	place, address = strings.TrimSpace(place), strings.TrimSpace(address)
	if place == "" {
		return address
	}
	if address == "" || address == place {
		return place
	}
	return place + ", " + address
}

func formatMoney(value float64) string {
	if value == 0 {
		return "—"
	}
	unit, divisor := "₽", 1.0
	if value >= 1_000_000 {
		unit, divisor = "млн ₽", 1_000_000
	}
	if value >= 1_000 && value < 1_000_000 {
		unit, divisor = "тыс. ₽", 1_000
	}
	number := strconv.FormatFloat(value/divisor, 'f', 1, 64)
	number = strings.TrimSuffix(strings.TrimSuffix(number, "0"), ".")
	number = strings.Replace(number, ".", ",", 1)
	return number + " " + unit
}

func fail(err error) { fmt.Fprintln(os.Stderr, "zakupki-review:", err); os.Exit(1) }
