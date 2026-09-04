package tgl

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseList(t *testing.T) {
	body, err := os.ReadFile("testdata/list.html")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseList(strings.NewReader(string(body)), "https://tgl.ru/news/city/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	if got[0].Title != "Разговор о важном!" || got[0].PublishedAt != "2026-09-02" || got[0].URL != "https://tgl.ru/news/item/25860-razgovor-o-vazhnom/" || got[0].Summary != "В День знаний ученики лицея «Созвездие» встретились с ветераном СВО." {
		t.Fatalf("unexpected first item: %#v", got[0])
	}
}

func TestNewsSourceItem(t *testing.T) {
	n := News{Title: "x", PublishedAt: "2026-09-03", URL: "https://tgl.ru/news/item/25864-example/"}
	item := n.SourceItem(time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	if item.Source != "tgl" || item.SourceItemID != "25864" || item.PublishedAt.IsZero() {
		t.Fatalf("unexpected source item: %#v", item)
	}
}

func TestParseArticle(t *testing.T) {
	body, err := os.ReadFile("testdata/article.html")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseArticle(strings.NewReader(string(body)), "https://tgl.ru/news/item/25860-razgovor-o-vazhnom/")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Разговор о важном!" || got.PublishedAt != "2026-09-02" {
		t.Fatalf("unexpected metadata: %#v", got)
	}
	want := "В День знаний ученики лицея «Созвездие» встретились с ветераном СВО Владимиром Харьковым.\n\nНа фронт Владимир ушёл добровольцем. Участвовал в боях на Херсонском направлении."
	if got.Text != want {
		t.Fatalf("text = %q, want %q", got.Text, want)
	}
}

func TestParseListRejectsMissingStructure(t *testing.T) {
	if _, err := ParseList(strings.NewReader("<html></html>"), "https://tgl.ru/"); err == nil {
		t.Fatal("expected error")
	}
}
