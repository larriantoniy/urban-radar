package zakupki

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ParseRaw parses the small common subset emitted by EIS notice documents.
// XML namespaces are intentionally matched by Local name, as EIS prefixes
// differ between document versions.
func ParseRaw(raw RawProcurement) (Procurement, error) {
	if raw.DocumentType == "html-search" {
		var e SearchHTMLEntry
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return Procurement{}, fmt.Errorf("zakupki search entry: %w", err)
		}
		if e.ID == "" || e.URL == "" {
			return Procurement{}, fmt.Errorf("zakupki: search entry missing identifier or URL")
		}
		return Procurement{ID: e.ID, URL: e.URL, Object: e.Object, CustomerName: e.CustomerName, Price: e.Price, PublishedAt: parseDate(e.PublishedAt), UpdatedAt: parseDate(e.UpdatedAt), Stage: e.Stage, Law: e.Law, DocumentType: raw.DocumentType}, nil
	}
	if raw.DocumentType == "rss" {
		var v struct {
			ID                 string `json:"id"`
			URL                string `json:"url"`
			Object             string `json:"object"`
			PublishedAt        string `json:"published_at"`
			RawSourceReference string `json:"raw_source_reference"`
		}
		if err := json.Unmarshal(raw.Payload, &v); err != nil {
			return Procurement{}, fmt.Errorf("zakupki RSS entry: %w", err)
		}
		if v.ID == "" || v.Object == "" {
			return Procurement{}, fmt.Errorf("zakupki: RSS entry missing identifier or title")
		}
		return Procurement{ID: v.ID, URL: v.URL, Object: v.Object, PublishedAt: parseDate(v.PublishedAt), RawSourceReference: v.RawSourceReference, DocumentType: "rss"}, nil
	}
	if raw.DocumentType == "json" {
		var p Procurement
		if err := json.Unmarshal(raw.Payload, &p); err != nil {
			return Procurement{}, fmt.Errorf("zakupki JSON: %w", err)
		}
		if p.ID == "" || p.Object == "" {
			return Procurement{}, fmt.Errorf("zakupki: JSON missing procurement identifier or object")
		}
		p.DocumentType = raw.DocumentType
		return p, nil
	}
	if raw.DocumentType != "" && raw.DocumentType != "xml" && raw.DocumentType != "notice" {
		return Procurement{}, fmt.Errorf("zakupki: unsupported document type %q", raw.DocumentType)
	}
	if len(bytes.TrimSpace(raw.Payload)) == 0 {
		return Procurement{}, fmt.Errorf("zakupki: empty document")
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw.Payload))
	p := Procurement{DocumentType: raw.DocumentType}
	var stack []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Procurement{}, fmt.Errorf("zakupki XML: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			stack = append(stack, strings.ToLower(value.Name.Local))
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			field := stack[len(stack)-1]
			text := strings.TrimSpace(string(value))
			if text == "" {
				continue
			}
			switch field {
			case "id", "noticeid", "registry_number", "registrynumber", "regnum":
				if p.ID == "" {
					p.ID = text
				}
			case "url", "noticelink", "purchaseurl":
				if p.URL == "" {
					p.URL = text
				}
			case "law", "lawtype":
				if p.Law == "" {
					p.Law = text
				}
			case "object", "purchaseobject", "title", "name":
				if p.Object == "" {
					p.Object = text
				}
			case "customername", "customer":
				if p.CustomerName == "" {
					p.CustomerName = text
				}
			case "inn", "customerinn":
				if p.CustomerINN == "" {
					p.CustomerINN = text
				}
			case "customerregion", "region":
				if p.CustomerRegion == "" {
					p.CustomerRegion = text
				}
			case "price", "maxprice", "initialprice":
				if p.Price == 0 {
					p.Price = parseNumber(text)
				}
			case "currency":
				if p.Currency == "" {
					p.Currency = text
				}
			case "stage", "status":
				if p.Stage == "" {
					p.Stage = text
				}
			case "deliveryplace", "place", "workplace":
				if p.DeliveryPlace == "" {
					p.DeliveryPlace = text
				}
			case "address", "objectaddress":
				if p.Address == "" {
					p.Address = text
				} else if !strings.Contains(p.Address, text) {
					p.Address += "; " + text
				}
			case "okpd2", "code":
				if len(text) >= 2 {
					p.OKPD2 = append(p.OKPD2, text)
				}
			case "publishedat", "publicationdate", "published":
				if p.PublishedAt.IsZero() {
					p.PublishedAt = parseDate(text)
				}
			case "updatedat", "updatedate", "updated":
				if p.UpdatedAt.IsZero() {
					p.UpdatedAt = parseDate(text)
				}
			case "contractdate":
				if p.ContractDate.IsZero() {
					p.ContractDate = parseDate(text)
				}
			case "tenderdate", "deadline":
				if p.TenderDate.IsZero() {
					p.TenderDate = parseDate(text)
				}
			case "rawsourcereference", "sourceid":
				if p.RawSourceReference == "" {
					p.RawSourceReference = text
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if p.ID == "" {
		return Procurement{}, fmt.Errorf("zakupki: missing procurement identifier")
	}
	if p.Object == "" {
		return Procurement{}, fmt.Errorf("zakupki: missing procurement object")
	}
	return p, nil
}

func parseNumber(value string) float64 {
	value = strings.ReplaceAll(strings.ReplaceAll(value, " ", ""), " ", "")
	value = strings.ReplaceAll(value, ",", ".")
	number, _ := strconv.ParseFloat(value, 64)
	return number
}

func parseDate(value string) time.Time {
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, "2006-01-02", "02.01.2006", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
