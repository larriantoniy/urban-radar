package zakupki

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseRawNamespacesAndOptionalFields(t *testing.T) {
	p, err := ParseRaw(RawProcurement{DocumentType: "notice", Payload: fixture(t, "notice.xml")})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "0159300000126000001" || p.Object != "Капитальный ремонт дороги в Тольятти" || p.Price != 12500000 {
		t.Fatalf("unexpected normalized procurement: %#v", p)
	}
	if p.Address != "г. Тольятти, ул. Мира, 10" || len(p.OKPD2) != 1 {
		t.Fatalf("missing optional fields: %#v", p)
	}
}

func TestParseRawMissingPriceAndMultipleAddresses(t *testing.T) {
	p, err := ParseRaw(RawProcurement{Payload: fixture(t, "missing-price.xml")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Price != 0 || p.Address != "г. Тольятти, ул. Ленина, 1; г. Тольятти, ул. Победы, 2" {
		t.Fatalf("unexpected optional handling: %#v", p)
	}
}

func TestParseRawRejectsMalformedAndUnknownDocument(t *testing.T) {
	if _, err := ParseRaw(RawProcurement{Payload: []byte("<notice>")}); err == nil {
		t.Fatal("expected malformed XML error")
	}
	if _, err := ParseRaw(RawProcurement{Payload: []byte("<other><id>x</id></other>")}); err == nil {
		t.Fatal("expected missing object error")
	}
	if _, err := ParseRaw(RawProcurement{DocumentType: "legacy-ftp", Payload: []byte("<notice/>")}); err == nil {
		t.Fatal("expected unsupported document type error")
	}
}

func TestFilterTogliattiAndNoise(t *testing.T) {
	good := Procurement{ID: "1", Object: "Строительство школы в Тольятти", DeliveryPlace: "Тольятти", Price: 1000000}
	result := Evaluate(good)
	if result.Relevance != Relevant || !result.Candidate {
		t.Fatalf("good procurement filtered: %#v", result)
	}
	noise := Procurement{ID: "2", Object: "Поставка канцтоваров", CustomerRegion: "Самарская область", CustomerName: "Администрация Самары"}
	result = Evaluate(noise)
	if result.Relevance != Irrelevant || result.Candidate {
		t.Fatalf("noise procurement passed: %#v", result)
	}
	maybe := Procurement{ID: "3", Object: "Поставка оборудования", CustomerName: "МБУ Тольятти"}
	result = Evaluate(maybe)
	if result.Relevance != MaybeRelevant {
		t.Fatalf("expected maybe relevant: %#v", result)
	}
}

func TestFixtureSourceLimitAndSourceItem(t *testing.T) {
	s := FixtureSource{Items: []RawProcurement{{Payload: fixture(t, "notice.xml")}, {Payload: fixture(t, "missing-price.xml")}}}
	items, err := s.Fetch(context.Background(), Query{Limit: 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("fixture fetch: %d, %v", len(items), err)
	}
	p, err := ParseRaw(items[0])
	if err != nil {
		t.Fatal(err)
	}
	item := p.SourceItem(time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	if item.Source != SourceName || item.SourceItemID == "" || item.Title == "" {
		t.Fatalf("bad source item: %#v", item)
	}
}

func TestEISDisabledWithoutEndpoint(t *testing.T) {
	client := NewEISClient(&http.Client{})
	client.Endpoint = ""
	if _, err := client.Fetch(context.Background(), Query{Limit: 1}); err == nil {
		t.Fatal("expected disabled EIS error")
	}
}

func TestNormalizeDeduplicatesStableID(t *testing.T) {
	raw := RawProcurement{Payload: fixture(t, "notice.xml")}
	items, err := Normalize([]RawProcurement{raw, raw}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SourceItem.SourceItemID != "0159300000126000001" {
		t.Fatalf("unexpected normalized items: %#v", items)
	}
}
