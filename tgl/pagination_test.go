package tgl

import (
	"context"
	"fmt"
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

func TestListNewsWindowHasNoItemLimitAndReportsSafetyBound(t *testing.T) {
	pages := archiveTestPages()
	client := archiveTestClient(t, pages, &[]string{})
	from := time.Date(2026, 9, 1, 12, 0, 0, 0, samaraLocation)
	to := time.Date(2026, 9, 4, 9, 0, 0, 0, samaraLocation)
	got, err := client.ListNewsWindow(context.Background(), from, to, 10)
	if err != nil || !got.Complete || got.PagesRead != 2 || len(got.News) != 3 {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	if got.News[0].PublishedAt != "2026-09-01" || got.News[2].PublishedAt != "2026-09-03" {
		t.Fatalf("ordering=%+v", got.News)
	}

	got, err = client.ListNewsWindow(context.Background(), from, to, 1)
	if err != nil || got.Complete || got.PagesRead != 1 {
		t.Fatalf("bounded result=%+v err=%v", got, err)
	}
}

func TestListNewsWindowReadsMoreThanTypicalItemCaps(t *testing.T) {
	client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/news/city/" {
			return htmlResponse(`<ul id="newsyears"><li class="active"><a href="/news/city/2026/">2026</a></li></ul>`), nil
		}
		page := 1
		if _, err := fmt.Sscanf(r.URL.Path, "/news/city/2026/%d/", &page); err != nil && r.URL.Path != "/news/city/2026/" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var items strings.Builder
		for i := 0; i < 25; i++ {
			id := (page-1)*25 + i + 1
			fmt.Fprintf(&items, `<div class="item"><h3><a href="/news/item/%d-item/">Item</a></h3><dd>Summary<p class="date">3 сентября</p></dd></div>`, id)
		}
		pagination := `<ul class="pagination"><li class="active"><a href="#">current</a></li>`
		if page < 5 {
			pagination += fmt.Sprintf(`<li><a href="/news/city/2026/%d/">next</a></li>`, page+1)
		}
		return htmlResponse(archivePage(items.String(), pagination+`</ul>`)), nil
	})}}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, samaraLocation)
	to := time.Date(2026, 9, 4, 0, 0, 0, 0, samaraLocation)
	result, err := client.ListNewsWindow(context.Background(), from, to, 10)
	if err != nil || !result.Complete || result.PagesRead != 5 || len(result.News) != 125 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func htmlResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
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
