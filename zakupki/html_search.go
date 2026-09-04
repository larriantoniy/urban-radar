package zakupki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ZakupkiSearchHTMLSource reads bounded public extended-search result pages.
type ZakupkiSearchHTMLSource struct {
	URL    string
	Client *http.Client
}

type SearchFetchResult struct {
	Raw         []RawProcurement `json:"raw"`
	PagesRead   int              `json:"pages_read"`
	EntriesSeen int              `json:"entries_seen"`
	Complete    bool             `json:"complete"`
}

func NewZakupkiSearchHTMLSource(client *http.Client) ZakupkiSearchHTMLSource {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return ZakupkiSearchHTMLSource{URL: os.Getenv("ZAKUPKI_SEARCH_URL"), Client: client}
}

func (s ZakupkiSearchHTMLSource) Fetch(ctx context.Context, q Query) ([]RawProcurement, error) {
	result, err := s.FetchWithOptions(ctx, SearchOptions{Limit: q.Limit})
	if err != nil {
		return nil, err
	}
	return result.Raw, nil
}

// FetchWithOptions reads bounded EIS pages while preserving every configured
// search parameter and changing only pageNumber between requests.
func (s ZakupkiSearchHTMLSource) FetchWithOptions(ctx context.Context, opts SearchOptions) (SearchFetchResult, error) {
	return s.fetchWithOptions(ctx, opts, true)
}

// FetchWindow reads the complete configured EIS date window. MaxPages remains
// a safety guard; reaching it returns Complete=false and must not advance a
// source checkpoint. There is deliberately no item-count correctness limit.
func (s ZakupkiSearchHTMLSource) FetchWindow(ctx context.Context, opts SearchOptions) (SearchFetchResult, error) {
	opts.Limit = 0
	return s.fetchWithOptions(ctx, opts, false)
}

func (s ZakupkiSearchHTMLSource) fetchWithOptions(ctx context.Context, opts SearchOptions, boundedItems bool) (SearchFetchResult, error) {
	limit := opts.Limit
	if boundedItems && limit <= 0 {
		limit = 20
	}
	if boundedItems && limit > 100 {
		limit = 100
	}
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	if maxPages > 100 {
		maxPages = 100
	}
	base, err := url.Parse(s.URL)
	if err != nil {
		return SearchFetchResult{}, fmt.Errorf("zakupki search URL: %w", err)
	}
	if base == nil || base.Host == "" {
		return SearchFetchResult{}, fmt.Errorf("zakupki search URL must be absolute")
	}
	values := base.Query()
	if !opts.PublishDateFrom.IsZero() {
		values.Set("publishDateFrom", eisDate(opts.PublishDateFrom))
	}
	if !opts.PublishDateTo.IsZero() {
		values.Set("publishDateTo", eisDate(opts.PublishDateTo))
	}
	values.Set("recordsPerPage", "_10")
	page := 1
	seen := map[string]bool{}
	all := make([]SearchHTMLEntry, 0, max(0, limit))
	pagesRead := 0
	complete := false
	for pagesRead < maxPages && (!boundedItems || len(all) < limit) {
		values.Set("pageNumber", fmt.Sprintf("%d", page))
		pageURL := *base
		pageURL.RawQuery = values.Encode()
		body, err := s.fetchPageURL(ctx, pageURL.String())
		if err != nil {
			return SearchFetchResult{}, err
		}
		entries, err := ParseSearchHTML(body, &pageURL)
		if err != nil {
			return SearchFetchResult{}, err
		}
		pagesRead++
		for _, entry := range entries {
			if entry.ID != "" && !seen[entry.ID] {
				seen[entry.ID] = true
				all = append(all, entry)
				if boundedItems && len(all) >= limit {
					break
				}
			}
		}
		next := nextSearchPage(body, page)
		if next <= page || len(entries) == 0 {
			complete = true
			break
		}
		page = next
	}
	sort.SliceStable(all, func(i, j int) bool {
		di, dj := parseSearchDate(all[i].PublishedAt), parseSearchDate(all[j].PublishedAt)
		if !di.Equal(dj) {
			return di.After(dj)
		}
		return all[i].ID < all[j].ID
	})
	return SearchFetchResult{Raw: searchEntriesToRawLimit(all, limit, boundedItems), PagesRead: pagesRead, EntriesSeen: len(all), Complete: complete}, nil
}

func (s ZakupkiSearchHTMLSource) ParsePage(body []byte, q Query) ([]RawProcurement, error) {
	entries, err := ParseSearchHTML(body, mustParseURL(s.URL))
	if err != nil {
		return nil, err
	}
	return searchEntriesToRaw(entries, q.Limit), nil
}

// FetchPage returns the exact HTTP response body before parsing.
func (s ZakupkiSearchHTMLSource) FetchPage(ctx context.Context) ([]byte, error) {
	if strings.TrimSpace(s.URL) == "" {
		return nil, fmt.Errorf("zakupki search is disabled: set ZAKUPKI_SEARCH_URL")
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, fmt.Errorf("zakupki search URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("zakupki search URL must be an absolute http(s) URL")
	}
	return s.fetchPageURL(ctx, u.String())
}

func (s ZakupkiSearchHTMLSource) fetchPageURL(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "urban-radar/0.1 (public search reader)")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zakupki search request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zakupki search: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func eisDate(t time.Time) string {
	return t.In(time.FixedZone("Europe/Samara", 4*60*60)).Format("02.01.2006")
}

func parseSearchDate(value string) time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "02.01.2006", "02.01.2006 15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, value, time.FixedZone("Europe/Samara", 4*60*60)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func nextSearchPage(data []byte, current int) int {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return 0
	}
	next := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" && classHas(n, "paginator-button-next") {
			if v, e := strconv.Atoi(attr(n, "data-pagenumber")); e == nil && v > current {
				next = v
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return next
}

func mustParseURL(value string) *url.URL { u, _ := url.Parse(value); return u }

func searchEntriesToRaw(entries []SearchHTMLEntry, requested int) []RawProcurement {
	limit := requested
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return searchEntriesToRawLimit(entries, limit, true)
}

func searchEntriesToRawLimit(entries []SearchHTMLEntry, limit int, bounded bool) []RawProcurement {
	seen := map[string]bool{}
	out := make([]RawProcurement, 0, limit)
	for _, e := range entries {
		if (bounded && len(out) >= limit) || e.ID == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		payload, _ := json.Marshal(e)
		out = append(out, RawProcurement{DocumentType: "html-search", Payload: payload})
	}
	return out
}

type SearchHTMLEntry struct {
	ID           string  `json:"id"`
	URL          string  `json:"url"`
	Object       string  `json:"object"`
	CustomerName string  `json:"customer_name,omitempty"`
	Price        float64 `json:"price,omitempty"`
	PublishedAt  string  `json:"published_at,omitempty"`
	UpdatedAt    string  `json:"updated_at,omitempty"`
	Stage        string  `json:"stage,omitempty"`
	Law          string  `json:"law,omitempty"`
}

var searchRegRE = regexp.MustCompile(`(?i)(?:regNumber|reestrNumber)=([0-9A-Za-z_-]+)`)

// ParseSearchHTML contains only DOM extraction; filtering remains in Evaluate.
func ParseSearchHTML(data []byte, base *url.URL) ([]SearchHTMLEntry, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("zakupki search HTML parse: %w", err)
	}
	var out []SearchHTMLEntry
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && classHas(n, "search-registry-entry-block") {
			number := canonicalNumberLink(n)
			if number != nil {
				id := registryIDFromLinkOrText(attr(number, "href"), nodeText(number))
				if !seen[id] {
					link := attr(number, "href")
					if base != nil {
						if u, e := base.Parse(link); e == nil {
							link = u.String()
						}
					}
					out = append(out, SearchHTMLEntry{ID: id, URL: link, Object: bodyValue(n, "Объект закупки"), CustomerName: bodyValue(n, "Заказчик"), Price: parseCardNumber(firstTextByClass(n, "price-block__value")), PublishedAt: dataValue(n, "Размещено"), UpdatedAt: dataValue(n, "Обновлено"), Stage: firstTextByClass(n, "registry-entry__header-mid__title"), Law: firstTextByClass(n, "registry-entry__header-top__title")})
					seen[id] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(out) == 0 {
		return nil, fmt.Errorf("zakupki search HTML parse: no procurement cards")
	}
	return out, nil
}

// CanonicalProcurementURL returns the notice card URL supplied by the EIS
// search result. It deliberately ignores print/signature/modal links.
func CanonicalProcurementURL(entry SearchHTMLEntry) (string, error) {
	return canonicalProcurementURL(entry, "zakupki.gov.ru")
}

func canonicalProcurementURL(entry SearchHTMLEntry, expectedHost string) (string, error) {
	if entry.ID == "" || entry.URL == "" {
		return "", fmt.Errorf("zakupki: search entry has no canonical procurement URL")
	}
	u, err := url.Parse(entry.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host != expectedHost {
		return "", fmt.Errorf("zakupki: invalid canonical procurement URL")
	}
	if strings.Contains(u.Path, "/printForm/") || strings.Contains(u.Path, "/signview/") || strings.Contains(u.Path, "listModal") {
		return "", fmt.Errorf("zakupki: non-canonical procurement URL")
	}
	return u.String(), nil
}

func canonicalNumberLink(card *html.Node) *html.Node {
	var block *html.Node
	findDesc(card, func(x *html.Node) bool {
		return x.Type == html.ElementNode && classHas(x, "registry-entry__header-mid__number")
	}, &block)
	if block == nil {
		return nil
	}
	var link *html.Node
	findDesc(block, func(x *html.Node) bool {
		return x.Type == html.ElementNode && x.Data == "a" && registryIDFromLinkOrText(attr(x, "href"), nodeText(x)) != ""
	}, &link)
	return link
}

func registryIDFromLinkOrText(link, text string) string {
	if id := stableRegistryID(link, ""); id != "" {
		return id
	}
	if m := regexp.MustCompile(`\b([0-9]{11,20})\b`).FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}
	return ""
}

func classHas(n *html.Node, value string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == value {
			return true
		}
	}
	return false
}
func findDesc(n *html.Node, pred func(*html.Node) bool, out **html.Node) {
	if *out != nil {
		return
	}
	if pred(n) {
		*out = n
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if pred(c) {
			*out = c
			return
		}
		findDesc(c, pred, out)
		if *out != nil {
			return
		}
	}
}
func firstTextByClass(n *html.Node, cls string) string {
	var found string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if found != "" {
			return
		}
		if x.Type == html.ElementNode && classHas(x, cls) {
			found = strings.Join(strings.Fields(nodeText(x)), " ")
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(found)
}
func bodyValue(n *html.Node, label string) string {
	var found string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if found != "" {
			return
		}
		if x.Type == html.ElementNode && classHas(x, "registry-entry__body-block") {
			t := firstClassChild(x, "registry-entry__body-title")
			if t != nil && strings.TrimSpace(nodeText(t)) == label {
				v := firstClassChild(x, "registry-entry__body-value")
				if v == nil {
					v = firstClassChild(x, "registry-entry__body-href")
				}
				if v != nil {
					found = strings.Join(strings.Fields(nodeText(v)), " ")
				}
				return
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}
func firstClassChild(n *html.Node, cls string) *html.Node {
	var found *html.Node
	findDesc(n, func(x *html.Node) bool { return x.Type == html.ElementNode && classHas(x, cls) }, &found)
	return found
}
func dataValue(n *html.Node, label string) string {
	var found string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if found != "" {
			return
		}
		if x.Type == html.ElementNode && classHas(x, "data-block") {
			if t := firstClassChild(x, "data-block__title"); t != nil && strings.TrimSpace(nodeText(t)) == label {
				found = firstTextByClass(x, "data-block__value")
				return
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}
func nearestBlock(n *html.Node) *html.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && (p.Data == "li" || p.Data == "article" || p.Data == "div") {
			cls := attr(p, "class")
			if strings.Contains(cls, "registry") || strings.Contains(cls, "search") || p.Data != "div" {
				return p
			}
		}
	}
	return n
}
func extractField(text, pattern string) string {
	m := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}
