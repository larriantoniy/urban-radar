package tgl

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var whitespace = regexp.MustCompile(`\s+`)

// ParseList parses HTML from https://tgl.ru/news/city/. It does no HTTP work.
func ParseList(r io.Reader, baseURL string) ([]News, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	root := firstByClass(doc, "newslist_short")
	if root == nil {
		return nil, fmt.Errorf("tgl list: .newslist_short not found")
	}
	year := yearFromText(textContent(firstElement(root, "h2")))
	if year == 0 {
		return nil, fmt.Errorf("tgl list: year not found")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	var items []News
	for _, item := range descendantsByClass(root, "item") {
		link := firstElement(item, "a")
		date := firstByClass(item, "date")
		dd := firstElement(item, "dd")
		if link == nil || date == nil || dd == nil {
			continue
		}
		href := attr(link, "href")
		itemURL, err := base.Parse(href)
		if err != nil {
			return nil, err
		}
		published, err := parseRussianDate(textContent(date), year)
		if err != nil {
			return nil, fmt.Errorf("tgl list date %q: %w", textContent(date), err)
		}
		items = append(items, News{
			Title: normalize(textContent(link)), PublishedAt: published,
			URL: itemURL.String(), Summary: textWithout(dd, date),
		})
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("tgl list: no news items found")
	}
	return items, nil
}

// ParseArticle parses HTML from a tgl.ru /news/item/ page. It does no HTTP work.
func ParseArticle(r io.Reader, rawURL string) (Article, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return Article{}, err
	}
	root := firstByClass(doc, "single_news")
	if root == nil {
		return Article{}, fmt.Errorf("tgl article: .single_news not found")
	}
	title, date := firstElement(root, "h2"), firstByClass(root, "date")
	if title == nil || date == nil {
		return Article{}, fmt.Errorf("tgl article: title or date not found")
	}
	published, err := parseRussianDate(textContent(date), 0)
	if err != nil {
		return Article{}, fmt.Errorf("tgl article date %q: %w", textContent(date), err)
	}
	var paragraphs []string
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "p" && !hasClass(child, "date") {
			if value := normalize(textContent(child)); value != "" {
				paragraphs = append(paragraphs, value)
			}
		}
	}
	if len(paragraphs) == 0 {
		return Article{}, fmt.Errorf("tgl article: text not found")
	}
	return Article{Title: normalize(textContent(title)), PublishedAt: published, URL: rawURL, Text: strings.Join(paragraphs, "\n\n")}, nil
}

func firstByClass(n *html.Node, class string) *html.Node {
	for _, x := range descendantsByClass(n, class) {
		return x
	}
	return nil
}
func descendantsByClass(n *html.Node, class string) (out []*html.Node) {
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && hasClass(x, class) {
			out = append(out, x)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}
func firstElement(n *html.Node, tag string) *html.Node {
	var walk func(*html.Node) *html.Node
	walk = func(x *html.Node) *html.Node {
		if x.Type == html.ElementNode && x.Data == tag {
			return x
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if found := walk(c); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(n)
}
func hasClass(n *html.Node, want string) bool {
	return strings.Contains(" "+attr(n, "class")+" ", " "+want+" ")
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
func textWithout(n, without *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x == without {
			return
		}
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return normalize(b.String())
}
func normalize(s string) string { return strings.TrimSpace(whitespace.ReplaceAllString(s, " ")) }
func yearFromText(s string) int {
	for _, f := range strings.Fields(s) {
		if y, err := strconv.Atoi(f); err == nil && y >= 2000 && y <= 2100 {
			return y
		}
	}
	return 0
}
func parseRussianDate(value string, defaultYear int) (string, error) {
	fields := strings.Fields(normalize(value))
	if len(fields) < 2 {
		return "", fmt.Errorf("invalid date")
	}
	day, err := strconv.Atoi(fields[0])
	if err != nil {
		return "", err
	}
	months := map[string]int{"января": 1, "февраля": 2, "марта": 3, "апреля": 4, "мая": 5, "июня": 6, "июля": 7, "августа": 8, "сентября": 9, "октября": 10, "ноября": 11, "декабря": 12}
	month := months[strings.ToLower(fields[1])]
	if month == 0 {
		return "", fmt.Errorf("unknown month")
	}
	year := defaultYear
	if len(fields) >= 3 {
		year, err = strconv.Atoi(fields[2])
		if err != nil {
			return "", err
		}
	}
	if year == 0 {
		return "", fmt.Errorf("year not found")
	}
	if day < 1 || day > 31 {
		return "", fmt.Errorf("invalid day")
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day), nil
}
