package zakupki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProcurementAccessAndDocuments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			_, _ = w.Write([]byte(`<div class="search-registry-entry-block"><div class="registry-entry__header-mid__number"><a href="/epz/order/notice/ea20/view/common-info.html?regNumber=32616328759">№ 32616328759</a></div><div class="registry-entry__body-block"><div class="registry-entry__body-title">Объект закупки</div><div class="registry-entry__body-value">Ремонт дороги</div></div></div>`))
			return
		}
		if strings.Contains(r.URL.Path, "documents") {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><a href="/files/spec.html">Техническое задание</a></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="Ремонт дороги"/></head><body><a href="/epz/order/notice/notice223/documents.html?purchaseNoticeNumber=32616328759">Документы</a><div>Заказчик: Муниципальное учреждение</div></body></html>`))
	}))
	defer srv.Close()
	a := NewProcurementAccess(srv.Client())
	a.BaseURL = srv.URL
	a.SearchURL = srv.URL + "/search"
	p, err := a.GetProcurement(context.Background(), "32616328759")
	if err != nil {
		t.Fatal(err)
	}
	if p.Procurement.ID != "32616328759" || p.Procurement.Object != "Ремонт дороги" {
		t.Fatalf("unexpected procurement: %+v", p.Procurement)
	}
	d, err := a.ListDocuments(context.Background(), "32616328759")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Documents) != 1 || d.Documents[0].Category != "DOCUMENT" {
		t.Fatalf("documents: %+v", d.Documents)
	}
	x, err := a.GetDocument(context.Background(), "32616328759", d.Documents[0].DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(x.Text, "Техническое задание") {
		t.Fatalf("text: %q", x.Text)
	}
}

func TestProcurementAccessErrorsAndSSRF(t *testing.T) {
	a := NewProcurementAccess(http.DefaultClient)
	if _, err := a.GetProcurement(context.Background(), "https://evil.example/x"); err == nil {
		t.Fatal("expected invalid registry id")
	}
	if _, _, err := a.fetch(context.Background(), "https://evil.example/x", 1024); err == nil {
		t.Fatal("expected trusted-host rejection")
	}
}

func TestExtractUnsupportedDocument(t *testing.T) {
	if _, err := extractDocumentText([]byte("pdf"), "application/pdf", 100); err == nil || !strings.Contains(err.Error(), string(ErrUnsupportedType)) {
		t.Fatalf("err=%v", err)
	}
}

func TestProcurementAccessSizeAndParseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			_, _ = w.Write([]byte(`<div class="search-registry-entry-block"><div class="registry-entry__header-mid__number"><a href="/epz/order/notice/ea20/view/common-info.html?regNumber=32616328759">№ 32616328759</a></div></div>`))
			return
		}
		if r.URL.Query().Get("bad") == "1" {
			_, _ = w.Write([]byte(`<html><body>no procurement object</body></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html><body>` + strings.Repeat("x", 64) + `</body></html>`))
	}))
	defer srv.Close()
	a := NewProcurementAccess(srv.Client())
	a.BaseURL = srv.URL
	a.SearchURL = srv.URL + "/search"
	a.MaxDownload = 16
	if _, err := a.GetProcurement(context.Background(), "32616328759"); err == nil || !strings.Contains(err.Error(), string(ErrDownloadTooLarge)) {
		t.Fatalf("size error=%v", err)
	}
	a.MaxDownload = 1 << 20
	if _, _, err := a.fetch(context.Background(), srv.URL+"/?bad=1", 0); err != nil {
		t.Fatal(err)
	}
}
