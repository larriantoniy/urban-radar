package zakupki

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAttachmentLinksExcludesSignature(t *testing.T) {
	h := `<div class="attachment row"><a data-modalup href="/epz/order/notice/signview/ep/listModal.html?uid=bad"></a><img alt="Microsoft Word Document"><span class="section__value"><a href="/44fz/filestore/public/1.0/download/priz/file.html?uid=U1" title="spec.docx">ТЗ</a></span></div>`
	a, err := parseAttachmentLinks([]byte(h), "https://zakupki.gov.ru/card", "12345678901")
	if err != nil || len(a) != 1 || a[0].AttachmentID != "U1" || strings.Contains(a[0].SourceURL, "signview") {
		t.Fatalf("attachments=%+v err=%v", a, err)
	}
}

func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, content := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestOfficeExtractionDOCXParagraphsAndTables(t *testing.T) {
	b := zipFixture(t, map[string]string{"word/document.xml": `<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>Объект</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Адрес</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Тольятти</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`})
	f, err := officeFormat(b, ".docx", "application/download")
	if err != nil || f != "docx" {
		t.Fatalf("format=%s err=%v", f, err)
	}
	text, err := extractOfficeText(b, f, 1000)
	if err != nil || !strings.Contains(text, "Объект") || !strings.Contains(text, "Тольятти") {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestOfficeExtractionXLSXSharedStrings(t *testing.T) {
	b := zipFixture(t, map[string]string{"xl/workbook.xml": `<workbook/>`, "xl/sharedStrings.xml": `<sst><si><t>Адрес</t></si><si><t>Тольятти</t></si></sst>`, "xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row></sheetData></worksheet>`})
	f, err := officeFormat(b, ".xlsx", "application/download")
	if err != nil || f != "xlsx" {
		t.Fatalf("format=%s err=%v", f, err)
	}
	text, err := extractOfficeText(b, f, 1000)
	if err != nil || !strings.Contains(text, "A1=Адрес") || !strings.Contains(text, "B1=Тольятти") {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestGetAttachmentTrustedLookupAndSizeLimit(t *testing.T) {
	docx := zipFixture(t, map[string]string{"word/document.xml": `<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>fact</w:t></w:r></w:p></w:body></w:document>`})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			_, _ = w.Write([]byte(`<div class="search-registry-entry-block"><div class="registry-entry__header-mid__number"><a href="/epz/order/notice/ea20/view/common-info.html?regNumber=12345678901">№12345678901</a></div></div>`))
		case "/epz/order/notice/ea20/view/common-info.html":
			_, _ = w.Write([]byte(`<div class="attachment row"><a href="/44fz/filestore/public/1.0/download/priz/file.html?uid=U1" title="x.docx">x</a></div>`))
		case "/44fz/filestore/public/1.0/download/priz/file.html":
			w.Header().Set("Content-Type", "application/download")
			_, _ = w.Write(docx)
		}
	}))
	defer srv.Close()
	a := NewProcurementAccess(srv.Client())
	a.BaseURL = srv.URL
	a.SearchURL = srv.URL + "/search"
	a.MaxDownload = int64(len(docx)) + 1
	v, err := a.GetAttachment(context.Background(), "12345678901", "U1")
	if err != nil || v.Format != "docx" || v.Content != "fact" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	a.MaxDownload = 1
	if _, err = a.GetAttachment(context.Background(), "12345678901", "U1"); err == nil || !strings.Contains(err.Error(), string(ErrDownloadTooLarge)) {
		t.Fatalf("size err=%v", err)
	}
}

func TestOfficeErrors(t *testing.T) {
	if _, err := officeFormat([]byte("bad"), ".docx", "application/download"); err == nil {
		t.Fatal("expected bad zip")
	}
	b := zipFixture(t, map[string]string{"foo.txt": "x"})
	if _, err := officeFormat(b, ".docx", "application/download"); err == nil || !strings.Contains(err.Error(), string(ErrUnsupportedType)) {
		t.Fatalf("err=%v", err)
	}
}
