package zakupki

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateProcurementRef(t *testing.T) {
	valid := ProcurementRef{RegistryID: "0142200001326017137", SourceURL: "https://zakupki.gov.ru/epz/order/notice/zk20/view/common-info.html?regNumber=0142200001326017137"}
	if err := validateProcurementRef(valid); err != nil {
		t.Fatalf("valid reference rejected: %v", err)
	}
	for name, ref := range map[string]ProcurementRef{
		"mismatch":        {RegistryID: valid.RegistryID, SourceURL: "https://zakupki.gov.ru/epz/order/notice/zk20/view/common-info.html?regNumber=0142200001326017138"},
		"foreign host":    {RegistryID: valid.RegistryID, SourceURL: "https://evil.example/epz/order/notice/zk20/view/common-info.html?regNumber=0142200001326017137"},
		"http":            {RegistryID: valid.RegistryID, SourceURL: "http://zakupki.gov.ru/epz/order/notice/zk20/view/common-info.html?regNumber=0142200001326017137"},
		"signature modal": {RegistryID: valid.RegistryID, SourceURL: "https://zakupki.gov.ru/epz/order/notice/printForm/listModal.html?regNumber=0142200001326017137"},
		"arbitrary path":  {RegistryID: valid.RegistryID, SourceURL: "https://zakupki.gov.ru/epz/order/notice/zk20/view/documents.html?regNumber=0142200001326017137"},
		"custom port":     {RegistryID: valid.RegistryID, SourceURL: "https://zakupki.gov.ru:443/epz/order/notice/zk20/view/common-info.html?regNumber=0142200001326017137"},
		"userinfo":        {RegistryID: valid.RegistryID, SourceURL: "https://user@zakupki.gov.ru/epz/order/notice/zk20/view/common-info.html?regNumber=0142200001326017137"},
		"fragment":        {RegistryID: valid.RegistryID, SourceURL: "https://zakupki.gov.ru/epz/order/notice/zk20/view/common-info.html?regNumber=0142200001326017137#section"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateProcurementRef(ref); err == nil {
				t.Fatal("expected reference rejection")
			}
		})
	}
}

type refRoundTripper struct{ searchCalls int }

func (r *refRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "extendedsearch") {
		r.searchCalls++
	}
	body := `<html><head><meta property="og:title" content="Благоустройство"/></head><body><div class="registry-entry__body-value">Благоустройство</div><a href="/epz/order/notice/zk20/view/documents.html?regNumber=0142200001326017137">Документы</a></body></html>`
	if strings.Contains(req.URL.Path, "documents.html") {
		body = `<html><a href="/files/spec.pdf">Техническое задание</a><div class="attachment"><a href="/44fz/filestore/public/1.0/download/priz/file.html?uid=att-1" title="Техническое задание.docx">Техническое задание</a></div></html>`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestSourceURLSkipsSearchAndResolvesDocuments(t *testing.T) {
	rt := &refRoundTripper{}
	a := NewProcurementAccess(&http.Client{Transport: rt})
	a.SearchURL = "https://zakupki.gov.ru/epz/order/extendedsearch/results.html"
	ref := ProcurementRef{RegistryID: "0142200001326017137", SourceURL: "https://zakupki.gov.ru/epz/order/notice/zk20/view/common-info.html?regNumber=0142200001326017137"}
	if _, err := a.GetProcurementRef(context.Background(), ref); err != nil {
		t.Fatalf("get with source URL: %v", err)
	}
	docs, err := a.ListDocumentsRef(context.Background(), ref)
	if err != nil {
		t.Fatalf("list documents with source URL: %v", err)
	}
	if len(docs.Documents) != 1 {
		t.Fatalf("documents=%+v", docs.Documents)
	}
	if _, err := a.ListAttachmentsRef(context.Background(), ref); err != nil {
		t.Fatalf("list attachments with source URL: %v", err)
	}
	if rt.searchCalls != 0 {
		t.Fatalf("source URL unexpectedly triggered %d search calls", rt.searchCalls)
	}
}

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
