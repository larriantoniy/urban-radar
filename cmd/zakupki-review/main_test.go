package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomReviewInputOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "items.json")
	output := filepath.Join(dir, "review.md")
	data := []byte(`[{"id":"1","source_url":"https://example.test/1","object":"Ремонт дороги","customer_name":"МБУ Тольятти","customer_region":"Самарская область","price":12500000,"delivery_place":"Тольятти","address":"ул. Мира","stage":"Контракт","human_label":null}]`)
	if err := os.WriteFile(input, data, 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := readItems(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeReview(output, renderReview(items)); err != nil {
		t.Fatal(err)
	}
	review, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(review)
	for _, want := range []string{"Total items: 1", "12,5 млн ₽", "human_label: null"} {
		if !strings.Contains(text, want) {
			t.Fatalf("review missing %q:\n%s", want, text)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  string
	}{{12500000, "12,5 млн ₽"}, {480000000, "480 млн ₽"}, {900000, "900 тыс. ₽"}, {0, "—"}} {
		if got := formatMoney(test.value); got != test.want {
			t.Errorf("formatMoney(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}
