package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	gofastcore "github.com/AOShei/go-fast-core"
)

// detectFile reads a file of internal/charset/testdata/detect.
func detectFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("internal/charset/testdata/detect/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func extractEnc(t *testing.T, b []byte, name, charsetLabel string) (*gofastcore.Document, error) {
	t.Helper()
	doc, err := (&Extractor{Charset: charsetLabel}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)), &gofastcore.Options{Name: name})
	if err == nil {
		if verr := doc.Validate(); verr != nil {
			t.Fatalf("Validate: %v", verr)
		}
	}
	return doc, err
}

func hasWarning(doc *gofastcore.Document, code string) bool {
	for _, w := range doc.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func docText(doc *gofastcore.Document) string {
	var b strings.Builder
	for _, p := range doc.Parts {
		for _, bl := range p.Blocks {
			b.WriteString(bl.Text)
			if bl.Table != nil {
				for _, r := range bl.Table.Rows {
					b.WriteString(strings.Join(r, " "))
				}
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// TestEncodingDetected: text that declares no encoding and is not UTF-8
// is read in the encoding recognised, with text.encoding_detected naming
// it, in every format that reads text.
func TestEncodingDetected(t *testing.T) {
	for _, c := range []struct {
		file, name, enc, want string
	}{
		{"ja.cp932", "a.txt", "shift_jis", "水は地球の表面"},
		{"zh.gbk", "a.md", "gbk", "水是地球上最常见的物质之一"},
		{"zh-tw.cp950", "a.html", "big5", "水是地球上最常見的物質之一"},
		{"ko.cp949", "a.txt", "euc-kr", "물은 지구 표면의"},
		{"ru.cp1251", "a.csv", "windows-1251", "Вода покрывает"},
		{"fr.cp1252", "a.json", "windows-1252", "L'eau recouvre environ soixante-dix pour cent"},
		{"ru.utf_16_le", "a.txt", "utf-16le", "Вода покрывает"},
		{"fr.utf8bad", "a.yaml", "utf-8", "indispensable"},
	} {
		doc, err := extractEnc(t, detectFile(t, c.file), c.name, "")
		if err != nil {
			t.Errorf("%s as %s: %v", c.file, c.name, err)
			continue
		}
		if !strings.Contains(docText(doc), c.want) {
			t.Errorf("%s as %s: text %.80q", c.file, c.name, docText(doc))
		}
		var msg string
		for _, w := range doc.Warnings {
			if w.Code == gofastcore.WarnTextEncodingDetected {
				msg = w.Message
			}
		}
		if !strings.Contains(msg, "read as "+c.enc+",") || !strings.Contains(msg, "-charset") {
			t.Errorf("%s as %s: warning %q", c.file, c.name, msg)
		}
	}
}

// TestEncodingUnknown: text whose encoding is not certain is refused
// with ErrEncodingUnknown, and read once Extractor.Charset gives it.
func TestEncodingUnknown(t *testing.T) {
	for _, c := range []struct{ file, name, label, want string }{
		{"cs.cp1250", "a.txt", "windows-1250", "Českou republikou protékají řeky"},
		{"tr.cp1254", "a.csv", "windows-1254", "Türkiye'de Fırat"},
		{"el.cp1253", "a.html", "windows-1253", "Το νερό καλύπτει"},
		{"ru.koi8_r", "a.md", "", ""},
	} {
		b := detectFile(t, c.file)
		_, err := extractEnc(t, b, c.name, "")
		if !errors.Is(err, ErrEncodingUnknown) || !errors.Is(err, gofastcore.ErrUnsupported) || !strings.Contains(err.Error(), "-charset") {
			t.Errorf("%s: %v", c.file, err)
		}
		if c.label == "" {
			continue // KOI8-R is not decoded
		}
		doc, err := extractEnc(t, b, c.name, c.label)
		if err != nil {
			t.Fatalf("%s with %s: %v", c.file, c.label, err)
		}
		if !strings.Contains(docText(doc), c.want) || hasWarning(doc, gofastcore.WarnTextEncodingDetected) {
			t.Errorf("%s with %s: %.80q %v", c.file, c.label, docText(doc), doc.Warnings)
		}
	}
}

// TestEncodingDeclaredNotJudged: UTF-8 and declared input have no
// warning.
func TestEncodingDeclaredNotJudged(t *testing.T) {
	for _, src := range []string{"caf\xC3\xA9 au lait", "<meta charset=windows-1250><p>Pra\xBEsk\xFD hrad", "\xEF\xBB\xBFcaf\xC3\xA9"} {
		doc, err := extractEnc(t, []byte(src), "a.html", "")
		if err != nil || hasWarning(doc, gofastcore.WarnTextEncodingDetected) {
			t.Errorf("%q: %v %v", src, err, doc.Warnings)
		}
	}
}

// TestEncodingEPUBChapter: a chapter of an EPUB whose encoding is not
// certain is read as windows-1252, with the warning, rather than the book
// refused.
func TestEncodingEPUBChapter(t *testing.T) {
	ch := func(body string) string {
		return `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>C</title></head><body>` + body + `</body></html>`
	}
	cs := detectFile(t, "cs.cp1250")
	b := buildEPUB(t, "META-INF/container.xml", container, "OEBPS/content.opf", opf3, "OEBPS/nav.xhtml", nav,
		"OEBPS/text/ch1.xhtml", ch("<p>"+string(cs[:400])+"</p>"),
		"OEBPS/text/ch 2.xhtml", ch("<p>"+string(detectFile(t, "ja.cp932")[:600])+"</p>"),
		"OEBPS/text/notes.xhtml", ch("<p>notes</p>"))
	doc := extractEPUB(t, b, nil)
	var msg string
	for _, w := range doc.Warnings {
		if w.Code == gofastcore.WarnTextEncodingDetected {
			msg = w.Message
		}
	}
	if !strings.Contains(msg, "2 chapters") || !strings.Contains(msg, "windows-1252 (not determined) in 1") || !strings.Contains(msg, "shift_jis in 1") {
		t.Errorf("warning %q", msg)
	}
	if !strings.Contains(docText(doc), "水は地球") {
		t.Errorf("text %.200q", docText(doc))
	}
}
