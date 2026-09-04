// Command zakupki-probe performs a bounded, read-only EIS probe.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"urban-radar/source"
	"urban-radar/zakupki"
)

func main() {
	limit := flag.Int("limit", 20, "maximum number of EIS documents to request (1..100)")
	sourceName := flag.String("source", "eis", "source: eis, rss or html")
	export := flag.String("export", "", "write normalized live items to this JSON path")
	saveRaw := flag.String("save-raw", "", "save exact HTML search response before parsing")
	saveCard := flag.String("save-card", "", "save exact first fetched detail HTML response")
	flag.Parse()
	if err := validateLimit(*limit); err != nil {
		fail(err.Error())
	}
	var raw []zakupki.RawProcurement
	var err error
	httpClient, err := zakupki.NewHTTPClientFromEnv(20 * time.Second)
	if err != nil {
		fail(err.Error())
	}
	if *sourceName == "rss" {
		raw, err = zakupki.NewRSSSource(httpClient).Fetch(context.Background(), zakupki.Query{Limit: *limit})
	} else if *sourceName == "html" {
		src := zakupki.NewZakupkiSearchHTMLSource(httpClient)
		if *saveRaw != "" {
			var body []byte
			body, err = src.FetchPage(context.Background())
			if err == nil {
				err = os.WriteFile(*saveRaw, body, 0o600)
			}
			if err == nil {
				raw, err = src.ParsePage(body, zakupki.Query{Limit: *limit})
			}
		} else {
			raw, err = src.Fetch(context.Background(), zakupki.Query{Limit: *limit})
		}
	} else if *sourceName == "eis" {
		raw, err = zakupki.NewEISClient(httpClient).Fetch(context.Background(), zakupki.Query{Limit: *limit})
	} else {
		fail("source must be eis, rss or html")
	}
	if err != nil {
		fail(err.Error())
	}
	result := make([]probeItem, 0, len(raw))
	normalizationErrors := make([]string, 0)
	enrichmentErrors := make([]string, 0)
	searchParsed, enrichmentSuccess := 0, 0
	for index, document := range raw {
		p, err := zakupki.ParseRaw(document)
		if err != nil {
			normalizationErrors = append(normalizationErrors, fmt.Sprintf("%d: %v", index+1, err))
			continue
		}
		searchParsed++
		if *sourceName == "rss" || *sourceName == "html" {
			card, cardErr := (zakupki.HTTPCardFetcher{Client: httpClient}).FetchCard(context.Background(), p.URL)
			if cardErr == nil && *saveCard != "" && index == 0 {
				if saveErr := os.WriteFile(*saveCard, card, 0o600); saveErr != nil {
					fmt.Fprintf(os.Stderr, "card debug save error: %v\n", saveErr)
				}
			}
			if cardErr == nil {
				p, cardErr = zakupki.ParseProcurementCard(card, p)
			}
			if cardErr != nil {
				enrichmentErrors = append(enrichmentErrors, fmt.Sprintf("%d: %v", index+1, cardErr))
			} else {
				enrichmentSuccess++
			}
		}
		if p.Object == "" {
			normalizationErrors = append(normalizationErrors, fmt.Sprintf("%d: missing procurement object after enrichment", index+1))
			continue
		}
		result = append(result, probeItem{Procurement: p, SourceItem: p.SourceItem(time.Now().UTC())})
	}
	if *sourceName == "html" || *sourceName == "rss" {
		fmt.Printf("search entries found: %d\nsearch entries parsed: %d\nenrichment success: %d\nenrichment failure: %d\n", len(raw), searchParsed, enrichmentSuccess, len(enrichmentErrors))
	}
	fmt.Printf("raw documents: %d\nnormalized: %d\nnormalization errors: %d\n", len(raw), len(result), len(normalizationErrors))
	for _, errorText := range normalizationErrors {
		fmt.Fprintf(os.Stderr, "normalization error: %s\n", errorText)
	}
	for _, errorText := range enrichmentErrors {
		fmt.Fprintf(os.Stderr, "enrichment error: %s\n", errorText)
	}
	for _, item := range result {
		fmt.Fprintf(os.Stdout, "- %s | %s | %s | %.2f %s | %s | %s | %s | %s\n", item.Procurement.ID, item.Procurement.Object, item.Procurement.CustomerName, item.Procurement.Price, item.Procurement.Currency, item.Procurement.DeliveryPlace, item.Procurement.PublishedAt.Format("2006-01-02"), item.Procurement.Stage, item.Procurement.URL)
	}
	if *export != "" {
		if err := writeExport(*export, result); err != nil {
			fail(err.Error())
		}
		if err := writeCaptureMetadata(*export, *limit, len(raw), len(result), len(normalizationErrors), *sourceName); err != nil {
			fail(err.Error())
		}
		if err := writeExportREADME(*export); err != nil {
			fail(err.Error())
		}
		fmt.Fprintf(os.Stderr, "exported %d normalized live items to %s\n", len(result), *export)
	}
}

func validateLimit(limit int) error {
	if limit < 1 || limit > 100 {
		return fmt.Errorf("limit must be between 1 and 100")
	}
	return nil
}

type probeItem struct {
	Procurement zakupki.Procurement `json:"procurement"`
	SourceItem  source.SourceItem   `json:"source_item"`
}

type exportItem struct {
	ID             string  `json:"id"`
	SourceURL      string  `json:"source_url"`
	Object         string  `json:"object"`
	CustomerName   string  `json:"customer_name"`
	CustomerRegion string  `json:"customer_region"`
	Price          float64 `json:"price"`
	Currency       string  `json:"currency,omitempty"`
	DeliveryPlace  string  `json:"delivery_place"`
	Address        string  `json:"address"`
	Stage          string  `json:"stage"`
	PublishedAt    string  `json:"published_at,omitempty"`
	UpdatedAt      string  `json:"updated_at,omitempty"`
	Law            string  `json:"law,omitempty"`
	HumanLabel     *string `json:"human_label"`
}

func writeExport(path string, items []probeItem) error {
	exported := make([]exportItem, 0, len(items))
	for _, item := range items {
		p := item.Procurement
		exported = append(exported, exportItem{ID: p.ID, SourceURL: p.URL, Object: p.Object, CustomerName: p.CustomerName, CustomerRegion: p.CustomerRegion, Price: p.Price, Currency: p.Currency, DeliveryPlace: p.DeliveryPlace, Address: p.Address, Stage: p.Stage, PublishedAt: formatTime(p.PublishedAt), UpdatedAt: formatTime(p.UpdatedAt), Law: p.Law})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02")
}

func writeCaptureMetadata(itemsPath string, requested, received, normalized, errors int, sourceNames ...string) error {
	sourceName := zakupki.SourceName
	if len(sourceNames) > 0 && sourceNames[0] != "" {
		sourceName = sourceNames[0]
	}
	urlHash := ""
	if value := os.Getenv("ZAKUPKI_RSS_URL"); value != "" {
		sum := sha256.Sum256([]byte(value))
		urlHash = fmt.Sprintf("%x", sum[:])
	}
	datasetHash := ""
	if data, err := os.ReadFile(itemsPath); err == nil {
		sum := sha256.Sum256(data)
		datasetHash = fmt.Sprintf("%x", sum[:])
	}
	metadata := map[string]any{
		"captured_at":          time.Now().UTC().Format(time.RFC3339),
		"source":               sourceName,
		"rss_url_sha256":       urlHash,
		"dataset_sha256":       datasetHash,
		"requested_limit":      requested,
		"received_count":       received,
		"normalized_count":     normalized,
		"normalization_errors": errors,
		"source_period":        nil,
		"human_label_status":   "null_pending_manual_review",
		"items_path":           filepath.Base(itemsPath),
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(itemsPath), "capture.json"), append(data, '\n'), 0o644)
}

func writeExportREADME(itemsPath string) error {
	text := "# Zakupki real V0 dataset\n\nThis directory was created by the bounded live EIS probe. `items.json` contains only normalized documents received from the configured live endpoint; `human_label` is null pending manual review. `capture.json` records the capture metadata. Do not edit source records after review starts; only human labels may be added.\n"
	return os.WriteFile(filepath.Join(filepath.Dir(itemsPath), "README.md"), []byte(text), 0o644)
}

func fail(message string) { fmt.Fprintln(os.Stderr, "zakupki-probe:", message); os.Exit(1) }
