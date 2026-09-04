package zakupki

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// ProcurementAttachment is a trusted file link discovered on the EIS card.
// SourceUID is supplied by EIS and is also the stable attachment ID in V0.
type ProcurementAttachment struct {
	AttachmentID string `json:"attachment_id"`
	SourceUID    string `json:"source_uid"`
	RegistryID   string `json:"registry_id"`
	Name         string `json:"name"`
	DeclaredType string `json:"declared_type,omitempty"`
	Extension    string `json:"extension,omitempty"`
	SourceURL    string `json:"source_url"`
}

type AttachmentsResult struct {
	RegistryID  string                  `json:"registry_id"`
	SourceURL   string                  `json:"source_url"`
	Attachments []ProcurementAttachment `json:"attachments"`
	Provenance  ProcurementProvenance   `json:"provenance"`
}

type AttachmentResult struct {
	RegistryID   string                `json:"registry_id"`
	AttachmentID string                `json:"attachment_id"`
	Name         string                `json:"name"`
	SourceURL    string                `json:"source_url"`
	Format       string                `json:"format"`
	ContentType  string                `json:"content_type"`
	SizeBytes    int64                 `json:"size_bytes"`
	Content      string                `json:"content"`
	Provenance   ProcurementProvenance `json:"provenance"`
}

// findDocumentsURL resolves the documents tab from the canonical card DOM;
// notice type is never guessed from the registry number.
func findDocumentsURL(data []byte, cardURL string) string {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	base, err := url.Parse(cardURL)
	if err != nil {
		return ""
	}
	var result string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if result != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" && strings.Contains(a.Val, "/view/documents.html") {
					u, e := base.Parse(a.Val)
					if e == nil && u.Host == base.Host && u.Query().Get("regNumber") != "" {
						result = u.String()
					}
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return result
}

func parseAttachmentLinks(data []byte, cardURL, registryID string) ([]ProcurementAttachment, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(cardURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []ProcurementAttachment
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "attachment") {
			var download, name, declared string
			var uid string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				collectAttachmentNode(c, &download, &name, &declared)
			}
			if download != "" {
				u, e := base.Parse(download)
				if e == nil && u.Host == base.Host && strings.HasPrefix(u.Path, "/44fz/filestore/public/1.0/download/") {
					uid = u.Query().Get("uid")
					if uid != "" && !seen[uid] {
						seen[uid] = true
						ext := strings.ToLower(filepath.Ext(name))
						out = append(out, ProcurementAttachment{AttachmentID: uid, SourceUID: uid, RegistryID: registryID, Name: strings.TrimSpace(name), DeclaredType: declared, Extension: ext, SourceURL: u.String()})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

func collectAttachmentNode(n *html.Node, download, name, declared *string) {
	if n.Type == html.ElementNode {
		if n.Data == "img" {
			for _, a := range n.Attr {
				if a.Key == "alt" {
					*declared = strings.TrimSpace(a.Val)
				}
			}
		}
		if n.Data == "a" {
			var href, title string
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
				if a.Key == "title" {
					title = a.Val
				}
			}
			if strings.Contains(href, "/filestore/public/1.0/download/") {
				*download = href
				*name = title
			}
		}
	}
	if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" && *name == "" {
		*name = strings.TrimSpace(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectAttachmentNode(c, download, name, declared)
	}
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, v := range strings.Fields(a.Val) {
				if v == class {
					return true
				}
			}
		}
	}
	return false
}

func officeFormat(data []byte, declared, contentType string) (string, error) {
	if len(data) < 4 || !bytes.Equal(data[:4], []byte("PK\x03\x04")) {
		return "", &MCPError{Code: ErrParse, Message: "attachment is not a ZIP Office document"}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", &MCPError{Code: ErrParse, Message: "corrupt Office ZIP: " + err.Error()}
	}
	hasDoc, hasSheet := false, false
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			hasDoc = true
		}
		if f.Name == "xl/workbook.xml" {
			hasSheet = true
		}
	}
	format := ""
	if hasDoc {
		format = "docx"
	}
	if hasSheet {
		if format != "" {
			return "", &MCPError{Code: ErrParse, Message: "ambiguous Office document structure"}
		}
		format = "xlsx"
	}
	if format == "" {
		return "", &MCPError{Code: ErrUnsupportedType, Message: "unsupported Office document structure"}
	}
	_ = declared
	_ = contentType
	return format, nil
}

func extractOfficeText(data []byte, format string, max int) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", &MCPError{Code: ErrParse, Message: "corrupt Office ZIP: " + err.Error()}
	}
	var text string
	if format == "docx" {
		var b []byte
		for _, f := range zr.File {
			if f.Name == "word/document.xml" {
				r, e := f.Open()
				if e != nil {
					return "", e
				}
				b, e = io.ReadAll(r)
				r.Close()
				if e != nil {
					return "", e
				}
				break
			}
		}
		text, err = extractDOCXXML(b)
	}
	if format == "xlsx" {
		text, err = extractXLSXXML(zr)
	}
	if err != nil {
		return "", &MCPError{Code: ErrParse, Message: err.Error()}
	}
	if max > 0 && len(text) > max {
		return "", &MCPError{Code: ErrDownloadTooLarge, Message: "extracted text exceeds configured limit"}
	}
	return strings.TrimSpace(text), nil
}

func extractDOCXXML(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
			case "tc":
				if b.Len() > 0 {
					b.WriteByte('\t')
				}
			case "tr":
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
			}
		case xml.CharData: /* handled below by element context is unnecessary: w:t is emitted as CharData */
		case xml.EndElement:
		}
	}
	// Reparse with a small state machine to retain only text in w:t.
	dec = xml.NewDecoder(bytes.NewReader(data))
	b.Reset()
	inText := false
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			}
			if t.Name.Local == "p" && b.Len() > 0 {
				b.WriteByte('\n')
			}
			if t.Name.Local == "tc" && b.Len() > 0 {
				b.WriteByte('\t')
			}
		case xml.CharData:
			if inText {
				b.WriteString(string(t))
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		}
	}
	return b.String(), nil
}

func extractXLSXXML(zr *zip.Reader) (string, error) {
	shared := []string{}
	for _, f := range zr.File {
		if f.Name == "xl/sharedStrings.xml" {
			r, e := f.Open()
			if e != nil {
				return "", e
			}
			d, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				return "", e
			}
			shared, e = parseSharedStrings(d)
			if e != nil {
				return "", e
			}
		}
	}
	type sheet struct {
		name string
		data []byte
	}
	var sheets []sheet
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/") && strings.HasSuffix(f.Name, ".xml") {
			r, e := f.Open()
			if e != nil {
				return "", e
			}
			d, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				return "", e
			}
			sheets = append(sheets, sheet{name: strings.TrimSuffix(strings.TrimPrefix(f.Name, "xl/worksheets/"), ".xml"), data: d})
		}
	}
	sort.Slice(sheets, func(i, j int) bool { return sheets[i].name < sheets[j].name })
	var b strings.Builder
	for _, s := range sheets {
		b.WriteString("Sheet ")
		b.WriteString(s.name)
		b.WriteByte('\n')
		lines, e := parseWorksheet(s.data, shared)
		if e != nil {
			return "", e
		}
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

func parseSharedStrings(data []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	inT := false
	var b strings.Builder
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "si" {
				b.Reset()
			}
			if t.Name.Local == "t" {
				inT = true
			}
		case xml.CharData:
			if inT {
				b.WriteString(string(t))
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
			}
			if t.Name.Local == "si" {
				out = append(out, b.String())
			}
		}
	}
	return out, nil
}
func parseWorksheet(data []byte, shared []string) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var lines []string
	var cellRef, cellType, val string
	inV, inT := false, false
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				cellRef = ""
				cellType = ""
				val = ""
				for _, a := range t.Attr {
					if a.Name.Local == "r" {
						cellRef = a.Value
					}
					if a.Name.Local == "t" {
						cellType = a.Value
					}
				}
			case "v":
				inV = true
			case "t":
				inT = true
			}
		case xml.CharData:
			if inV || inT {
				val += string(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "c":
				out := val
				if cellType == "s" {
					if i := atoi(val); i >= 0 && i < len(shared) {
						out = shared[i]
					}
				}
				if strings.TrimSpace(out) != "" {
					lines = append(lines, cellRef+"="+strings.TrimSpace(out))
				}
			}
		}
	}
	return lines, nil
}
func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}
