package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var headingRE = regexp.MustCompile(`(?m)^##\s+\d+\s+[—-]\s+([A-Za-z0-9_-]+)\s*$`)
var labelRE = regexp.MustCompile("(?mi)^\\s*human_label:\\s*`?([^`\\s]+)`?\\s*$")

type syncStats struct{ Total, Found, Candidate, Skip, Unlabeled, Errors int }

func parseReview(data []byte) (map[string]string, map[string]struct{}, int, error) {
	matches := headingRE.FindAllSubmatchIndex(data, -1)
	labels := map[string]string{}
	ids := map[string]struct{}{}
	errorsCount := 0
	for i, m := range matches {
		id := string(data[m[2]:m[3]])
		if _, ok := ids[id]; ok {
			errorsCount++
			continue
		}
		ids[id] = struct{}{}
		end := len(data)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		block := data[m[1]:end]
		lm := labelRE.FindSubmatch(block)
		if len(lm) > 1 {
			v := strings.TrimSpace(string(lm[1]))
			if v == "CANDIDATE" || v == "SKIP" {
				labels[id] = v
			}
		}
	}
	return labels, ids, errorsCount, nil
}

func syncItems(data, review []byte) ([]byte, syncStats, error) {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, syncStats{}, fmt.Errorf("items JSON: %w", err)
	}
	reviewLabels, reviewIDs, duplicateErrors, err := parseReview(review)
	if err != nil {
		return nil, syncStats{}, err
	}
	ids := map[string]int{}
	for i, item := range items {
		var id string
		if raw := item["id"]; raw != nil {
			_ = json.Unmarshal(raw, &id)
		}
		if id != "" {
			ids[id] = i
		}
	}
	for id := range reviewIDs {
		if _, ok := ids[id]; !ok {
			return nil, syncStats{Errors: 1}, fmt.Errorf("review registry ID %s not found in items.json", id)
		}
	}
	stats := syncStats{Total: len(items), Found: len(reviewLabels), Errors: duplicateErrors}
	for _, item := range items {
		var id string
		_ = json.Unmarshal(item["id"], &id)
		if label, ok := reviewLabels[id]; ok {
			b, _ := json.Marshal(label)
			item["human_label"] = b
			if label == "CANDIDATE" {
				stats.Candidate++
			} else {
				stats.Skip++
			}
		} else {
			stats.Unlabeled++
		}
	}
	if stats.Errors > 0 {
		return nil, stats, fmt.Errorf("review contains %d duplicate registry ID(s)", stats.Errors)
	}
	out, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return nil, stats, err
	}
	return append(out, '\n'), stats, nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".label-sync-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(data)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func main() {
	input := flag.String("input", "data/evals/zakupki-real-v0/items.json", "dataset JSON")
	review := flag.String("review", "data/evals/zakupki-real-v0/HUMAN_REVIEW.md", "human review markdown")
	dry := flag.Bool("dry-run", false, "validate and report without writing")
	flag.Parse()
	data, err := os.ReadFile(*input)
	if err != nil {
		fail(err)
	}
	rev, err := os.ReadFile(*review)
	if err != nil {
		fail(err)
	}
	out, stats, err := syncItems(data, rev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "errors: %d\n", stats.Errors)
		fail(err)
	}
	fmt.Printf("total items: %d\nlabels found: %d\ncandidate labels: %d\nskip labels: %d\nunlabeled: %d\nerrors: %d\n", stats.Total, stats.Found, stats.Candidate, stats.Skip, stats.Unlabeled, stats.Errors)
	if !*dry {
		if err := atomicWrite(*input, out); err != nil {
			fail(err)
		}
	}
}
func fail(err error) {
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "zakupki-label-sync:", err)
	} else {
		fmt.Fprintln(os.Stderr, "zakupki-label-sync:", err)
	}
	os.Exit(1)
}
