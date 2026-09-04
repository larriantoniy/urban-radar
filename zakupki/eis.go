package zakupki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// EISClient is a bounded adapter boundary for the current EIS API/SOAP
// deployment. Endpoint and bearer token are environment-controlled; no FTP
// fallback is provided because the historical zakupki.gov.ru FTP is deprecated.
type EISClient struct {
	Endpoint string
	Token    string
	Client   *http.Client
}

func NewEISClient(client *http.Client) EISClient {
	if client == nil {
		client = &http.Client{}
	}
	return EISClient{Endpoint: os.Getenv("ZAKUPKI_EIS_URL"), Token: os.Getenv("ZAKUPKI_EIS_TOKEN"), Client: client}
}

func (c EISClient) Fetch(ctx context.Context, query Query) ([]RawProcurement, error) {
	if strings.TrimSpace(c.Endpoint) == "" {
		return nil, fmt.Errorf("zakupki EIS is disabled: set ZAKUPKI_EIS_URL (and ZAKUPKI_EIS_TOKEN when required)")
	}
	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("zakupki EIS request: %w", err)
	}
	q := req.URL.Query()
	q.Set("limit", strconv.Itoa(limit))
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json, application/xml")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zakupki EIS request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zakupki EIS: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(body, &documents); err == nil {
		out := make([]RawProcurement, 0, len(documents))
		for _, document := range documents {
			out = append(out, RawProcurement{DocumentType: "json", Payload: document})
		}
		return out, nil
	}
	var envelope struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Items != nil {
		out := make([]RawProcurement, 0, len(envelope.Items))
		for _, document := range envelope.Items {
			out = append(out, RawProcurement{DocumentType: "json", Payload: document})
		}
		return out, nil
	}
	return []RawProcurement{{DocumentType: "xml", Payload: body}}, nil
}
