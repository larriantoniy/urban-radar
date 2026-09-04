package zakupki

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// RSSSource reads one bounded page of a public EIS search RSS feed.
type RSSSource struct {
	URL    string
	Client *http.Client
}

func NewRSSSource(client *http.Client) RSSSource {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return RSSSource{URL: os.Getenv("ZAKUPKI_RSS_URL"), Client: client}
}

type rssEntry struct{ ID, Title, Link, Published, Description string }

func (s RSSSource) Fetch(ctx context.Context, q Query) ([]RawProcurement, error) {
	if strings.TrimSpace(s.URL) == "" {
		return nil, fmt.Errorf("zakupki RSS is disabled: set ZAKUPKI_RSS_URL")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, fmt.Errorf("zakupki RSS URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("zakupki RSS URL must be an absolute http(s) URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")
	req.Header.Set("User-Agent", "urban-radar/0.1 (RSS reader)")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zakupki RSS request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zakupki RSS: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	entries, err := parseRSSEntries(body)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]RawProcurement, 0, limit)
	for _, e := range entries {
		if len(out) >= limit {
			break
		}
		e.ID = stableRegistryID(e.Link, e.ID)
		if e.ID == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		payload := []byte(fmt.Sprintf(`{"id":%q,"url":%q,"object":%q,"published_at":%q,"raw_source_reference":%q,"document_type":"rss"}`, e.ID, e.Link, e.Title, e.Published, e.ID))
		out = append(out, RawProcurement{DocumentType: "rss", Payload: payload})
	}
	return out, nil
}

func parseRSSEntries(data []byte) ([]rssEntry, error) {
	var doc struct {
		Channel struct {
			Items []struct {
				GUID        string `xml:"guid"`
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				PubDate     string `xml:"pubDate"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
		Entries []struct {
			ID        string   `xml:"id"`
			Title     string   `xml:"title"`
			Link      atomLink `xml:"link"`
			Published string   `xml:"published"`
			Updated   string   `xml:"updated"`
			Summary   string   `xml:"summary"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("zakupki RSS parse: %w", err)
	}
	out := make([]rssEntry, 0, len(doc.Channel.Items)+len(doc.Entries))
	for _, i := range doc.Channel.Items {
		out = append(out, rssEntry{ID: i.GUID, Title: strings.TrimSpace(i.Title), Link: strings.TrimSpace(i.Link), Published: strings.TrimSpace(i.PubDate), Description: strings.TrimSpace(i.Description)})
	}
	for _, e := range doc.Entries {
		p := e.Published
		if p == "" {
			p = e.Updated
		}
		link := e.Link.URL
		if link == "" {
			link = e.Link.Href
		}
		out = append(out, rssEntry{ID: e.ID, Title: strings.TrimSpace(e.Title), Link: strings.TrimSpace(link), Published: strings.TrimSpace(p), Description: strings.TrimSpace(e.Summary)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("zakupki RSS parse: no entries")
	}
	return out, nil
}

type atomLink struct {
	URL  string `xml:",chardata"`
	Href string `xml:"href,attr"`
}

var regNumberRE = regexp.MustCompile(`(?i)(?:regNumber|reestrNumber)=([0-9A-Za-z_-]+)`)

func stableRegistryID(link, guid string) string {
	if m := regNumberRE.FindStringSubmatch(link); len(m) > 1 {
		return m[1]
	}
	return strings.TrimSpace(guid)
}
