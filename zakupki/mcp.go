package zakupki

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// MCPErrorCode is a stable, machine-readable error classification.
type MCPErrorCode string

const (
	ErrProcurementNotFound MCPErrorCode = "PROCUREMENT_NOT_FOUND"
	ErrDocumentNotFound    MCPErrorCode = "DOCUMENT_NOT_FOUND"
	ErrUnsupportedType     MCPErrorCode = "UNSUPPORTED_DOCUMENT_TYPE"
	ErrDownloadTooLarge    MCPErrorCode = "DOWNLOAD_TOO_LARGE"
	ErrUpstream            MCPErrorCode = "UPSTREAM_ERROR"
	ErrParse               MCPErrorCode = "PARSE_ERROR"
	ErrResolution          MCPErrorCode = "RESOLUTION_ERROR"
)

type MCPError struct {
	Code    MCPErrorCode `json:"code"`
	Message string       `json:"message"`
}

func (e *MCPError) Error() string { return string(e.Code) + ": " + e.Message }

type ProcurementDocument struct {
	DocumentID    string `json:"document_id"`
	SourceID      string `json:"source_id,omitempty"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int64  `json:"size,omitempty"`
	Category      string `json:"category,omitempty"`
	PublishedDate string `json:"published_date,omitempty"`
}

type ProcurementProvenance struct {
	RegistryID  string    `json:"registry_id"`
	SourceURL   string    `json:"source_url"`
	SourceType  string    `json:"source_type"`
	RetrievedAt time.Time `json:"retrieved_at"`
}

type ProcurementResult struct {
	Procurement Procurement           `json:"procurement"`
	Provenance  ProcurementProvenance `json:"provenance"`
}

type DocumentsResult struct {
	RegistryID string                `json:"registry_id"`
	SourceURL  string                `json:"source_url"`
	Documents  []ProcurementDocument `json:"documents"`
	Provenance ProcurementProvenance `json:"provenance"`
}

type DocumentResult struct {
	Document   ProcurementDocument   `json:"document"`
	Text       string                `json:"text,omitempty"`
	Provenance ProcurementProvenance `json:"provenance"`
}

// ProcurementAccess provides bounded, primary-source access for the future
// Research Agent. BaseURL is injectable only for deterministic tests.
type ProcurementAccess struct {
	Client      *http.Client
	BaseURL     string
	MaxDownload int64
	MaxText     int
	SearchURL   string
}

func NewProcurementAccess(client *http.Client) *ProcurementAccess {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &ProcurementAccess{Client: client, BaseURL: "https://zakupki.gov.ru", SearchURL: os.Getenv("ZAKUPKI_SEARCH_URL"), MaxDownload: 8 << 20, MaxText: 200000}
}

var registryIDRE = regexp.MustCompile(`^[0-9]{11,20}$`)

func validateRegistryID(id string) error {
	id = strings.TrimSpace(id)
	if !registryIDRE.MatchString(id) {
		return &MCPError{Code: ErrProcurementNotFound, Message: "invalid registry_id"}
	}
	return nil
}

// ResolveProcurement gets the canonical notice URL from a trusted EIS search
// response. It never guesses notice types or falls back to print-form modals.
func (a *ProcurementAccess) ResolveProcurement(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if err := validateRegistryID(id); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.SearchURL) == "" {
		return "", &MCPError{Code: ErrResolution, Message: "set ZAKUPKI_SEARCH_URL to resolve canonical procurement URL"}
	}
	body, _, err := a.fetch(ctx, a.SearchURL, 0)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(a.SearchURL)
	if err != nil {
		return "", &MCPError{Code: ErrResolution, Message: err.Error()}
	}
	entries, err := ParseSearchHTML(body, base)
	if err != nil {
		return "", &MCPError{Code: ErrResolution, Message: err.Error()}
	}
	for _, entry := range entries {
		if entry.ID == id {
			canonical, e := canonicalProcurementURL(entry, base.Host)
			if e != nil {
				return "", &MCPError{Code: ErrResolution, Message: e.Error()}
			}
			return canonical, nil
		}
	}
	return "", &MCPError{Code: ErrProcurementNotFound, Message: "registry_id not found in configured EIS search response"}
}

func (a *ProcurementAccess) fetch(ctx context.Context, rawURL string, limit int64) ([]byte, string, error) {
	base, _ := url.Parse(a.BaseURL)
	u, err := url.Parse(rawURL)
	if err != nil || base == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host != base.Host {
		return nil, "", &MCPError{Code: ErrUpstream, Message: "URL is outside the trusted zakupki.gov.ru host"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", &MCPError{Code: ErrUpstream, Message: err.Error()}
	}
	req.Header.Set("User-Agent", "urban-radar/0.1 (evidence reader)")
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, "", &MCPError{Code: ErrUpstream, Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", &MCPError{Code: ErrProcurementNotFound, Message: resp.Status}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", &MCPError{Code: ErrUpstream, Message: resp.Status}
	}
	if limit <= 0 {
		limit = a.MaxDownload
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", &MCPError{Code: ErrUpstream, Message: err.Error()}
	}
	if int64(len(data)) > limit {
		return nil, "", &MCPError{Code: ErrDownloadTooLarge, Message: "response exceeds configured limit"}
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func (a *ProcurementAccess) GetProcurement(ctx context.Context, id string) (*ProcurementResult, error) {
	u, err := a.ResolveProcurement(ctx, id)
	if err != nil {
		return nil, err
	}
	body, _, err := a.fetch(ctx, u, 0)
	if err != nil {
		return nil, err
	}
	p, err := ParseProcurementCard(body, Procurement{ID: id, URL: u, DocumentType: "html-card"})
	if err != nil {
		return nil, &MCPError{Code: ErrParse, Message: err.Error()}
	}
	return &ProcurementResult{Procurement: p, Provenance: ProcurementProvenance{RegistryID: id, SourceURL: u, SourceType: "PROCUREMENT_CARD", RetrievedAt: time.Now().UTC()}}, nil
}

func canonicalDocumentID(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (a *ProcurementAccess) ListDocuments(ctx context.Context, id string) (*DocumentsResult, error) {
	u, err := a.ResolveProcurement(ctx, id)
	if err != nil {
		return nil, err
	}
	body, _, err := a.fetch(ctx, u, 0)
	if err != nil {
		return nil, err
	}
	docs, err := parseDocumentLinks(body, u, id)
	if err != nil {
		return nil, &MCPError{Code: ErrParse, Message: err.Error()}
	}
	return &DocumentsResult{RegistryID: id, SourceURL: u, Documents: docs, Provenance: ProcurementProvenance{RegistryID: id, SourceURL: u, SourceType: "DOCUMENT_INDEX", RetrievedAt: time.Now().UTC()}}, nil
}

func (a *ProcurementAccess) GetDocument(ctx context.Context, id, documentID string) (*DocumentResult, error) {
	list, err := a.ListDocuments(ctx, id)
	if err != nil {
		return nil, err
	}
	var doc ProcurementDocument
	for _, d := range list.Documents {
		if d.DocumentID == documentID {
			doc = d
			break
		}
	}
	if doc.DocumentID == "" {
		return nil, &MCPError{Code: ErrDocumentNotFound, Message: "document_id is not listed for this registry_id"}
	}
	body, ct, err := a.fetch(ctx, doc.URL, 0)
	if err != nil {
		return nil, err
	}
	if ct == "" {
		ct = doc.ContentType
	}
	text, err := extractDocumentText(body, ct, a.MaxText)
	if err != nil {
		return nil, err
	}
	doc.ContentType = ct
	doc.Size = int64(len(body))
	return &DocumentResult{Document: doc, Text: text, Provenance: ProcurementProvenance{RegistryID: id, SourceURL: doc.URL, SourceType: "PROCUREMENT_DOCUMENT", RetrievedAt: time.Now().UTC()}}, nil
}

func parseDocumentLinks(data []byte, baseURL, id string) ([]ProcurementDocument, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(baseURL)
	seen := map[string]bool{}
	var out []ProcurementDocument
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			var href, name string
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
			name = strings.Join(strings.Fields(nodeText(n)), " ")
			if href != "" && (strings.Contains(href, "/documents") || strings.Contains(href, "/contract") || strings.Contains(href, "/plan") || strings.Contains(strings.ToLower(path.Ext(href)), ".pdf") || strings.Contains(strings.ToLower(path.Ext(href)), ".xml")) {
				u, e := base.Parse(href)
				if e == nil && u.Host == base.Host && !seen[u.String()] {
					seen[u.String()] = true
					cat := "DOCUMENT"
					if strings.Contains(href, "contract") {
						cat = "CONTRACT"
					}
					if strings.Contains(href, "plan") {
						cat = "PLAN"
					}
					out = append(out, ProcurementDocument{DocumentID: canonicalDocumentID(u.String()), Name: name, URL: u.String(), Category: cat})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	_ = id
	return out, nil
}

func extractDocumentText(data []byte, contentType string, max int) (string, error) {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "pdf") || strings.Contains(ct, "word") || strings.Contains(ct, "excel") || strings.HasSuffix(ct, "zip") {
		return "", &MCPError{Code: ErrUnsupportedType, Message: "document format is not supported in V0"}
	}
	text := string(data)
	if strings.Contains(ct, "html") || bytes.Contains(bytes.ToLower(data), []byte("<html")) {
		d, err := html.Parse(bytes.NewReader(data))
		if err != nil {
			return "", &MCPError{Code: ErrParse, Message: err.Error()}
		}
		text = strings.Join(strings.Fields(nodeText(d)), " ")
	}
	if max > 0 && len(text) > max {
		text = text[:max]
	}
	return text, nil
}
