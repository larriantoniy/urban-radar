package zakupki

import (
	"os"
	"testing"
)

func TestParseRSS20DedupAndRegistryID(t *testing.T) {
	b, err := os.ReadFile("testdata/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	e, err := parseRSSEntries(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(e) != 2 || stableRegistryID(e[0].Link, e[0].ID) != "123" {
		t.Fatalf("entries=%#v", e)
	}
}

func TestParseAtomAndMissingDate(t *testing.T) {
	b, err := os.ReadFile("testdata/feed.atom")
	if err != nil {
		t.Fatal(err)
	}
	e, err := parseRSSEntries(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(e) != 1 || e[0].Link == "" || e[0].Published == "" {
		t.Fatalf("entries=%#v", e)
	}
}

func TestRSSRawNormalizes(t *testing.T) {
	p, err := ParseRaw(RawProcurement{DocumentType: "rss", Payload: []byte(`{"id":"1","url":"https://example/1","object":"Ремонт","published_at":"2026-09-03"}`)})
	if err != nil || p.ID != "1" || p.PublishedAt.IsZero() {
		t.Fatalf("p=%#v err=%v", p, err)
	}
}

func TestCardParserUsesMetaTitle(t *testing.T) {
	p, err := ParseProcurementCard([]byte(`<html><head><meta property="og:title" content="Тендер на дорогу"/></head><body>card</body></html>`), Procurement{ID: "1", URL: "https://example/1"})
	if err != nil || p.Object != "Тендер на дорогу" {
		t.Fatalf("p=%#v err=%v", p, err)
	}
}

func TestCardFetcherRejectsBrokenLink(t *testing.T) {
	if _, err := (HTTPCardFetcher{}).FetchCard(t.Context(), "://broken"); err == nil {
		t.Fatal("expected broken URL error")
	}
}
