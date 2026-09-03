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
