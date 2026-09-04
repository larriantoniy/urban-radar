// Package tgl fetches city news from the official Togliatti administration site.
package tgl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"urban-radar/source"
)

const (
	cityNewsURL = "https://tgl.ru/news/city/"
	userAgent   = "urban-radar/0.1 (+https://github.com/urban-radar)"
)

// News is a list entry returned by ListNews.
type News struct {
	Title       string `json:"title"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url"`
	Summary     string `json:"summary"`
}

// SourceItem adapts a TGL list item to the shared source-level contract.
func (n News) SourceItem(retrievedAt time.Time) source.SourceItem {
	return source.SourceItem{
		Source: "tgl", SourceItemID: newsID(n.URL), URL: n.URL, Title: n.Title,
		Summary: n.Summary, PublishedAt: parseNewsDate(n.PublishedAt), RetrievedAt: retrievedAt,
	}
}

func newsID(rawURL string) string {
	parts := strings.Split(strings.Trim(rawURL, "/"), "/")
	if len(parts) > 0 {
		segment := parts[len(parts)-1]
		if dash := strings.IndexByte(segment, '-'); dash > 0 {
			return segment[:dash]
		}
		return segment
	}
	return rawURL
}

func parseNewsDate(value string) time.Time {
	parsed, _ := time.Parse("2006-01-02", value)
	return parsed
}

// Article is a full news item returned by GetNews.
type Article struct {
	Title       string `json:"title"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url"`
	Text        string `json:"text"`
}

// Client fetches tgl.ru pages using an ordinary HTTP client.
type Client struct {
	httpClient *http.Client
}

// ListOptions controls how much city-news history is read.
type ListOptions struct {
	Limit int
	Since time.Time
}

// NewClient creates a client with the supplied request timeout.
func NewClient(timeout time.Duration) *Client {
	return &Client{httpClient: &http.Client{Timeout: timeout}}
}

// ListNews fetches and parses the current city-news list.
func (c *Client) ListNews(ctx context.Context) ([]News, error) {
	body, err := c.fetch(ctx, cityNewsURL)
	if err != nil {
		return nil, err
	}
	return ParseList(body, cityNewsURL)
}

// ListNewsWithOptions reads the newest city news from the site's archive pages.
// A zero Limit uses the site's normal archive page size (20 items).
func (c *Client) ListNewsWithOptions(ctx context.Context, options ListOptions) ([]News, error) {
	if options.Limit < 0 {
		return nil, fmt.Errorf("limit must be zero or positive")
	}
	limit := options.Limit
	if limit == 0 {
		limit = 20
	}

	body, err := c.fetch(ctx, cityNewsURL)
	if err != nil {
		return nil, err
	}
	years, err := archiveYears(body, cityNewsURL)
	if err != nil {
		return nil, err
	}

	news := make([]News, 0, limit)
	seen := make(map[string]struct{})
	for _, yearURL := range years {
		pageURL := yearURL
		for pageURL != "" {
			body, err := c.fetch(ctx, pageURL)
			if err != nil {
				return nil, err
			}
			page, nextURL, err := parseListPage(body, pageURL)
			if err != nil {
				return nil, err
			}

			olderThanSince := false
			for _, item := range page {
				published, err := time.Parse("2006-01-02", item.PublishedAt)
				if err != nil {
					return nil, err
				}
				if !options.Since.IsZero() && published.Before(options.Since) {
					olderThanSince = true
					continue
				}
				if _, duplicate := seen[item.URL]; duplicate {
					continue
				}
				seen[item.URL] = struct{}{}
				news = append(news, item)
				if len(news) == limit {
					return news, nil
				}
			}
			if olderThanSince {
				return news, nil
			}
			pageURL = nextURL
		}
	}
	return news, nil
}

// GetNews fetches and parses one tgl.ru news item.
func (c *Client) GetNews(ctx context.Context, rawURL string) (Article, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || (u.Hostname() != "tgl.ru" && u.Hostname() != "www.tgl.ru") || !strings.HasPrefix(u.Path, "/news/item/") {
		return Article{}, fmt.Errorf("invalid tgl.ru news URL: %q", rawURL)
	}
	body, err := c.fetch(ctx, rawURL)
	if err != nil {
		return Article{}, err
	}
	return ParseArticle(body, rawURL)
}

func (c *Client) fetch(ctx context.Context, rawURL string) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return strings.NewReader(string(data)), nil
}
