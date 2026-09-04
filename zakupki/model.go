// Package zakupki normalizes procurement notices from Russia's EIS/zakupki
// source before they reach the existing Discovery agent.
package zakupki

import (
	"context"
	"fmt"
	"strings"
	"time"

	"urban-radar/source"
)

const SourceName = "zakupki"

// Query is deliberately small; the live adapter may add source-specific
// parameters later without leaking them into filtering logic.
type Query struct {
	Limit int
	Since time.Time
}

// RawProcurement is the adapter boundary. Payload may contain the original
// XML/JSON document for audit, but business logic only consumes normalized
// fields after ParseRaw.
type RawProcurement struct {
	DocumentType string
	Payload      []byte
}

// Procurement is the normalized subset needed by Urban Radar V0.
type Procurement struct {
	ID                 string    `json:"id"`
	URL                string    `json:"url"`
	Law                string    `json:"law,omitempty"`
	PublishedAt        time.Time `json:"published_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
	CustomerName       string    `json:"customer_name,omitempty"`
	CustomerINN        string    `json:"customer_inn,omitempty"`
	CustomerRegion     string    `json:"customer_region,omitempty"`
	Object             string    `json:"object,omitempty"`
	Price              float64   `json:"price,omitempty"`
	Currency           string    `json:"currency,omitempty"`
	Stage              string    `json:"stage,omitempty"`
	DeliveryPlace      string    `json:"delivery_place,omitempty"`
	Address            string    `json:"address,omitempty"`
	OKPD2              []string  `json:"okpd2,omitempty"`
	ContractDate       time.Time `json:"contract_date,omitempty"`
	TenderDate         time.Time `json:"tender_date,omitempty"`
	RawSourceReference string    `json:"raw_source_reference,omitempty"`
	DocumentType       string    `json:"document_type,omitempty"`
}

// ProcurementSource fetches raw documents. Implementations include FixtureSource
// for deterministic tests and EISClient for bounded live probes.
type ProcurementSource interface {
	Fetch(context.Context, Query) ([]RawProcurement, error)
}

type FixtureSource struct {
	Items []RawProcurement
}

func (f FixtureSource) Fetch(_ context.Context, query Query) ([]RawProcurement, error) {
	limit := query.Limit
	if limit <= 0 || limit > len(f.Items) {
		limit = len(f.Items)
	}
	return append([]RawProcurement(nil), f.Items[:limit]...), nil
}

// Relevance is the deterministic source-level locality result.
type Relevance string

const (
	Relevant      Relevance = "relevant"
	MaybeRelevant Relevance = "maybe_relevant"
	Irrelevant    Relevance = "irrelevant"
)

type FilterResult struct {
	Relevance       Relevance `json:"relevance"`
	Reasons         []string  `json:"relevance_reasons"`
	InterestScore   int       `json:"interest_score"`
	Candidate       bool      `json:"candidate"`
	CategorySignals []string  `json:"category_signals,omitempty"`
}

type NormalizedItem struct {
	Procurement Procurement       `json:"procurement"`
	SourceItem  source.SourceItem `json:"source_item"`
}

// Normalize parses and deduplicates by the stable EIS identifier without
// making editorial decisions or invoking an LLM.
func Normalize(raws []RawProcurement, retrievedAt time.Time) ([]NormalizedItem, error) {
	seen := make(map[string]struct{}, len(raws))
	result := make([]NormalizedItem, 0, len(raws))
	for _, raw := range raws {
		procurement, err := ParseRaw(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[procurement.ID]; ok {
			continue
		}
		seen[procurement.ID] = struct{}{}
		result = append(result, NormalizedItem{Procurement: procurement, SourceItem: procurement.SourceItem(retrievedAt)})
	}
	return result, nil
}

func (p Procurement) SourceItem(retrievedAt time.Time) source.SourceItem {
	metadata := map[string]any{
		"registry_id": p.ID,
		"law":         p.Law, "customer_name": p.CustomerName, "customer_inn": p.CustomerINN,
		"customer_region": p.CustomerRegion, "price": p.Price, "currency": p.Currency,
		"stage": p.Stage, "delivery_place": p.DeliveryPlace, "address": p.Address,
		"okpd2": p.OKPD2, "contract_date": p.ContractDate, "tender_date": p.TenderDate,
		"raw_source_reference": p.RawSourceReference, "document_type": p.DocumentType,
	}
	return source.SourceItem{
		Source: SourceName, SourceItemID: p.ID, URL: p.URL, Title: p.Object,
		Summary: procurementSummary(p), Text: procurementText(p), PublishedAt: p.PublishedAt,
		RetrievedAt: retrievedAt, Metadata: metadata,
	}
}

func procurementText(p Procurement) string {
	parts := []string{p.Object}
	if p.CustomerName != "" {
		parts = append(parts, "Заказчик: "+p.CustomerName)
	}
	if p.DeliveryPlace != "" {
		parts = append(parts, "Место: "+p.DeliveryPlace)
	}
	if p.Address != "" {
		parts = append(parts, "Адрес: "+p.Address)
	}
	if p.Price != 0 {
		parts = append(parts, fmt.Sprintf("Цена: %.2f %s", p.Price, p.Currency))
	}
	if p.Stage != "" {
		parts = append(parts, "Этап: "+p.Stage)
	}
	return strings.Join(parts, ". ")
}

func procurementSummary(p Procurement) string {
	if p.CustomerName == "" {
		return p.Object
	}
	return p.CustomerName + ": " + p.Object
}
