package tgl

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestListNewsWithOptionsPaginatesAndDeduplicates(t *testing.T) {
	pages := archiveTestPages()
	var requested []string
	client := archiveTestClient(t, pages, &requested)

	got, err := client.ListNewsWithOptions(context.Background(), ListOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d news items, want 3", len(got))
	}
	for i, want := range []string{"https://tgl.ru/news/item/newest/", "https://tgl.ru/news/item/second/", "https://tgl.ru/news/item/third/"} {
		if got[i].URL != want {
			t.Fatalf("item %d URL = %q, want %q", i, got[i].URL, want)
		}
	}
	if strings.Contains(strings.Join(requested, ","), "/news/city/2025/") {
		t.Fatalf("unnecessary older year request: %v", requested)
	}
}

func TestListNewsWithOptionsStopsAtSince(t *testing.T) {
	pages := archiveTestPages()
	var requested []string
	client := archiveTestClient(t, pages, &requested)
	since, err := time.Parse("2006-01-02", "2026-09-02")
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.ListNewsWithOptions(context.Background(), ListOptions{Limit: 100, Since: since})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].PublishedAt != "2026-09-03" || got[1].PublishedAt != "2026-09-02" {
		t.Fatalf("unexpected since result: %#v", got)
	}
	if strings.Contains(strings.Join(requested, ","), "/news/city/2025/") {
		t.Fatalf("fetched history older than since: %v", requested)
	}
}

func archiveTestClient(t *testing.T, pages map[string]string, requested *[]string) *Client {
	t.Helper()
	return &Client{httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*requested = append(*requested, r.URL.String())
		body, ok := pages[r.URL.String()]
		if !ok {
			t.Fatalf("unexpected request: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
}

func archiveTestPages() map[string]string {
	return map[string]string{
		"https://tgl.ru/news/city/":        `<ul id="newsyears"><li class="active"><a href="/news/city/2026/">2026</a></li><li><a href="/news/city/2025/">2025</a></li></ul>`,
		"https://tgl.ru/news/city/2026/":   archivePage("<div class=\"item\"><h3><a href=\"/news/item/newest/\">Newest</a></h3><dd>Newest summary<p class=\"date\">3 сентября</p></dd></div><div class=\"item\"><h3><a href=\"/news/item/second/\">Second</a></h3><dd>Second summary<p class=\"date\">2 сентября</p></dd></div>", `<ul class="pagination"><li class="active"><a href="/news/city/2026/1/">1</a></li><li><a href="/news/city/2026/2/">2</a></li></ul>`),
		"https://tgl.ru/news/city/2026/2/": archivePage("<div class=\"item\"><h3><a href=\"/news/item/second/\">Second duplicate</a></h3><dd>Duplicate summary<p class=\"date\">2 сентября</p></dd></div><div class=\"item\"><h3><a href=\"/news/item/third/\">Third</a></h3><dd>Third summary<p class=\"date\">1 сентября</p></dd></div>", `<ul class="pagination"><li><a href="/news/city/2026/1/">1</a></li><li class="active"><a href="/news/city/2026/2/">2</a></li></ul>`),
	}
}

func archivePage(items, pagination string) string {
	return `<div class="newslist_short"><h2>2026 года</h2>` + items + `</div>` + pagination
}
