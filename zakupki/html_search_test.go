package zakupki

import (
	"net/url"
	"os"
	"testing"
)

func TestParseSearchHTMLCardsAndDedup(t *testing.T) {
	b, err := os.ReadFile("testdata/search.html")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://zakupki.gov.ru/epz/order/extendedsearch/results.html")
	items, err := ParseSearchHTML(b, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "111" || items[0].Price != 12500000 {
		t.Fatalf("items=%#v", items)
	}
	if items[0].URL != "https://zakupki.gov.ru/epz/order/notice/ea44/view/common-info.html?regNumber=111" {
		t.Fatalf("url=%q", items[0].URL)
	}
	if items[1].CustomerName != "" {
		t.Fatalf("expected missing customer: %#v", items[1])
	}
}

func TestParseSearchHTMLMalformedCard(t *testing.T) {
	if _, err := ParseSearchHTML([]byte(`<html><a href="/bad">no id</a></html>`), nil); err == nil {
		t.Fatal("expected no cards error")
	}
}

func TestParseSearchHTMLCanonicalLinkWinsOverPrintForms(t *testing.T) {
	b, err := os.ReadFile("testdata/search_canonical.html")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://zakupki.gov.ru/epz/order/extendedsearch/results.html")
	items, err := ParseSearchHTML(b, base)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if items[0].ID != "333" || items[0].URL != "https://zakupki.gov.ru/epz/order/notice/eap20/view/common-info.html?regNumber=333" {
		t.Fatalf("canonical=%#v", items[0])
	}
	if items[0].Object != "Ремонт школы" || items[0].Stage != "Подача заявок" || items[0].Law != "44-ФЗ Запрос котировок" || items[0].Price != 900000 {
		t.Fatalf("fields=%#v", items[0])
	}
}
