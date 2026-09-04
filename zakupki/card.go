package zakupki

import (
	"bytes"
	"context"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// ProcurementCardFetcher and ProcurementCardParser isolate public-card enrichment.
type ProcurementCardFetcher interface {
	FetchCard(context.Context, string) ([]byte, error)
}
type HTTPCardFetcher struct{ Client *http.Client }

func (f HTTPCardFetcher) FetchCard(ctx context.Context, link string) ([]byte, error) {
	if strings.TrimSpace(link) == "" {
		return nil, fmt.Errorf("empty procurement card URL")
	}
	c := f.Client
	if c == nil {
		c = &http.Client{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "urban-radar/0.1 (procurement card reader)")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("card request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("card: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func ParseProcurementCard(data []byte, fallback Procurement) (Procurement, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return Procurement{}, fmt.Errorf("card HTML parse: %w", err)
	}
	text := strings.Join(strings.Fields(nodeText(doc)), " ")
	if fallback.Object == "" {
		fallback.Object = meta(doc, "og:title")
	}
	if fallback.Object == "" {
		fallback.Object = firstTextByTag(doc, "title")
	}
	if fallback.Object == "" {
		fallback.Object = stringBefore(text, "Заказка")
	}
	if fallback.Object == "" {
		return Procurement{}, fmt.Errorf("card: missing procurement object")
	}
	if fallback.URL == "" {
		fallback.URL = meta(doc, "og:url")
	}
	if fallback.CustomerName == "" {
		fallback.CustomerName = labeled(text, `Заказчик\s*[:—-]\s*([^;|]+)`)
	}
	if fallback.Address == "" {
		fallback.Address = labeled(text, `(Адрес места поставки|Место выполнения работ)\s*[:—-]\s*([^;|]+)`)
	}
	if fallback.Stage == "" {
		fallback.Stage = labeled(text, `(Статус|Этап)\s*[:—-]\s*([^;|]+)`)
	}
	if fallback.Price == 0 {
		if raw := labeled(text, `(Начальная \(максимальная\) цена|Цена контракта)\s*[:—-]\s*([0-9\s,.]+)`); raw != "" {
			fallback.Price = parseCardNumber(raw)
		}
	}
	return fallback, nil
}

func firstTextByTag(n *html.Node, tag string) string {
	var found string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if found != "" {
			return
		}
		if x.Type == html.ElementNode && x.Data == tag {
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

func labeled(text, pattern string) string {
	m := regexp.MustCompile(`(?i)` + pattern).FindStringSubmatch(text)
	if len(m) == 0 {
		return ""
	}
	return strings.TrimSpace(m[len(m)-1])
}
func parseCardNumber(s string) float64 {
	s = strings.ReplaceAll(strings.ReplaceAll(s, " ", ""), " ", "")
	if m := regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)?`).FindString(s); m != "" {
		s = m
	}
	s = strings.ReplaceAll(s, ",", ".")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(" ")
		b.WriteString(nodeText(c))
	}
	return b.String()
}
func meta(n *html.Node, name string) string {
	var found string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && x.Data == "meta" {
			var p, c string
			for _, a := range x.Attr {
				if a.Key == "property" || a.Key == "name" {
					p = a.Val
				}
				if a.Key == "content" {
					c = a.Val
				}
			}
			if strings.EqualFold(p, name) {
				found = strings.TrimSpace(c)
			}
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return found
}
func stringBefore(s, marker string) string {
	if i := strings.Index(s, marker); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return ""
}
