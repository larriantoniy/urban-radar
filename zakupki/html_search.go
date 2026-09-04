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
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ZakupkiSearchHTMLSource reads exactly one public extended-search result page.
type ZakupkiSearchHTMLSource struct {
	URL    string
	Client *http.Client
}

func NewZakupkiSearchHTMLSource(client *http.Client) ZakupkiSearchHTMLSource {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return ZakupkiSearchHTMLSource{URL: os.Getenv("ZAKUPKI_SEARCH_URL"), Client: client}
}

func (s ZakupkiSearchHTMLSource) Fetch(ctx context.Context, q Query) ([]RawProcurement, error) {
	body, err := s.FetchPage(ctx)
	if err != nil {
		return nil, err
	}
	return s.ParsePage(body, q)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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

func mustParseURL(value string) *url.URL { u, _ := url.Parse(value); return u }

func searchEntriesToRaw(entries []SearchHTMLEntry, requested int) []RawProcurement {
	limit := requested
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	seen := map[string]bool{}
	out := make([]RawProcurement, 0, limit)
	for _, e := range entries {
		if len(out) >= limit || e.ID == "" || seen[e.ID] {
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
			var number *html.Node
			findDesc(n, func(x *html.Node) bool {
				return x.Type == html.ElementNode && x.Data == "a" && stableRegistryID(attr(x, "href"), "") != ""
			}, &number)
			if number != nil {
				id := stableRegistryID(attr(number, "href"), "")
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
