// Command zakupki-eval evaluates the deterministic Zakupki source layer.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"urban-radar/zakupki"
)

type evalItem struct {
	ID             string  `json:"id"`
	SourceURL      string  `json:"source_url"`
	Object         string  `json:"object"`
	CustomerName   string  `json:"customer_name"`
	CustomerRegion string  `json:"customer_region"`
	Price          float64 `json:"price"`
	DeliveryPlace  string  `json:"delivery_place"`
	Address        string  `json:"address"`
	Stage          string  `json:"stage"`
	HumanLabel     *string `json:"human_label"`
}

func main() {
	input := flag.String("input", "data/evals/zakupki-v0/items.json", "input dataset JSON")
	flag.Parse()
	data, err := os.ReadFile(*input)
	if err != nil {
		fail(err)
	}
	var items []evalItem
	if err := json.Unmarshal(data, &items); err != nil {
		fail(err)
	}
	var tp, tn, fp, fn int
	labelled := 0
	candidates := 0
	normalizationErrors := 0
	type mistake struct{ id, object string }
	falsePositives, falseNegatives := []mistake{}, []mistake{}
	for _, item := range items {
		if item.ID == "" || item.Object == "" || item.SourceURL == "" {
			normalizationErrors++
		}
		p := zakupki.Procurement{ID: item.ID, URL: item.SourceURL, Object: item.Object, CustomerName: item.CustomerName, CustomerRegion: item.CustomerRegion, Price: item.Price, DeliveryPlace: item.DeliveryPlace, Address: item.Address, Stage: item.Stage}
		result := zakupki.Evaluate(p)
		if result.Candidate {
			candidates++
		}
		if item.HumanLabel == nil {
			continue
		}
		if *item.HumanLabel != "CANDIDATE" && *item.HumanLabel != "SKIP" {
			fail(fmt.Errorf("%s: human_label must be CANDIDATE or SKIP", item.ID))
		}
		labelled++
		expectedCandidate := *item.HumanLabel == "CANDIDATE"
		if expectedCandidate && result.Candidate {
			tp++
		} else if !expectedCandidate && !result.Candidate {
			tn++
		} else if !expectedCandidate {
			fp++
			falsePositives = append(falsePositives, mistake{item.ID, item.Object})
		} else {
			fn++
			falseNegatives = append(falseNegatives, mistake{item.ID, item.Object})
		}
	}
	fmt.Printf("items: %d\nlabelled: %d\ncandidates: %d\nnormalization_errors: %d\n", len(items), labelled, candidates, normalizationErrors)
	if labelled == 0 {
		fmt.Println("metrics: unavailable until human_label values are filled")
	} else {
		precision := rate(tp, tp+fp)
		recall := rate(tp, tp+fn)
		f1 := 0.0
		if precision+recall > 0 {
			f1 = 2 * precision * recall / (precision + recall)
		}
		fmt.Printf("TP: %d TN: %d FP: %d FN: %d\nprecision: %.4f\nrecall: %.4f\nf1: %.4f\n", tp, tn, fp, fn, precision, recall, f1)
		fmt.Println("false positives:")
		for _, item := range falsePositives {
			fmt.Printf("- %s | %s\n", item.id, item.object)
		}
		fmt.Println("false negatives:")
		for _, item := range falseNegatives {
			fmt.Printf("- %s | %s\n", item.id, item.object)
		}
	}
}

func rate(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}
func fail(err error) { fmt.Fprintln(os.Stderr, "zakupki-eval:", err); os.Exit(1) }
