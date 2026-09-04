package zakupki

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
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

func pageCard(id string) string {
	return fmt.Sprintf(`<div class="search-registry-entry-block"><div class="registry-entry__header-mid__number"><a href="/epz/order/notice/ea20/view/common-info.html?regNumber=%s">№ %s</a></div><div class="registry-entry__body-block"><div class="registry-entry__body-title">Объект закупки</div><div class="registry-entry__body-value">Объект %s</div></div><div class="registry-entry__header-mid__title">Подача заявок</div></div>`, id, id, id)
}

func TestFetchWithOptionsPaginationDedupAndDates(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RawQuery)
		switch r.URL.Query().Get("pageNumber") {
		case "1":
			fmt.Fprint(w, pageCard("11111111111")+pageCard("22222222222")+`<a class="paginator-button-next" data-pagenumber="2">next</a>`)
		case "2":
			fmt.Fprint(w, pageCard("22222222222")+pageCard("33333333333")+`<a class="paginator-button-next" data-pagenumber="3">next</a>`)
		case "3":
			fmt.Fprint(w, pageCard("44444444444"))
		}
	}))
	defer srv.Close()
	s := ZakupkiSearchHTMLSource{URL: srv.URL + "/search?searchString=%D0%A2%D0%BE%D0%BB%D1%8C%D1%8F%D1%82%D1%82%D0%B8&fz44=on&sortBy=UPDATE_DATE", Client: srv.Client()}
	res, err := s.FetchWithOptions(context.Background(), SearchOptions{Limit: 4, PublishDateFrom: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), PublishDateTo: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), MaxPages: 10})
	if err != nil || len(res.Raw) != 4 || res.PagesRead != 3 {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests=%v", requests)
	}
	for _, q := range requests {
		v, _ := url.ParseQuery(q)
		if v.Get("searchString") != "Тольятти" || v.Get("fz44") != "on" || v.Get("publishDateFrom") != "01.09.2026" || v.Get("publishDateTo") != "04.09.2026" || v.Get("recordsPerPage") != "_10" {
			t.Fatalf("query=%v", v)
		}
	}
}

func TestFetchWithOptionsSafetyBoundAndShortResult(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		fmt.Fprint(w, pageCard(fmt.Sprintf("%011d", count))+`<a class="paginator-button-next" data-pagenumber="`+fmt.Sprint(count+1)+`">next</a>`)
	}))
	defer srv.Close()
	s := ZakupkiSearchHTMLSource{URL: srv.URL + "/search", Client: srv.Client()}
	res, err := s.FetchWithOptions(context.Background(), SearchOptions{Limit: 50, MaxPages: 2})
	if err != nil || res.PagesRead != 2 || len(res.Raw) != 2 {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	res, err = s.FetchWithOptions(context.Background(), SearchOptions{Limit: 5, MaxPages: 10})
	if err != nil || res.PagesRead != 5 || len(res.Raw) != 5 {
		t.Fatalf("short result=%+v err=%v", res, err)
	}
}
