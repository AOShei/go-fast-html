package gofasthtml

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

// A 1x1 PNG.
var pngPixel, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

// chromeMHTML is a page as Chrome saves it: quoted-printable HTML, a
// base64 image the page shows, one it does not, a style sheet, a font,
// a frame by Content-ID, and an encoded-word subject.
func chromeMHTML() string {
	img := base64.StdEncoding.EncodeToString(pngPixel)
	return "From: <Saved by Blink>\r\n" +
		"Snapshot-Content-Location: https://example.org/dir/page.html\r\n" +
		"Subject: =?utf-8?Q?Caf=C3=A9_page?=\r\n" +
		"Date: Wed, 25 Nov 2026 16:43:29 GMT\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related;\r\n\ttype=\"text/html\";\r\n\tboundary=\"----B----\"\r\n" +
		"\r\n\r\n" +
		"------B----\r\n" +
		"Content-Type: text/html\r\n" +
		"Content-ID: <frame-1@mhtml.blink>\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"Content-Location: https://example.org/dir/page.html\r\n" +
		"\r\n" +
		"<html><head><meta charset=3D\"utf-8\"><title>Caf=C3=A9</title></head><body>=\r\n" +
		"<h1 id=3D\"top\">Bonjour</h1><p>Un caf=C3=A9 au lait.</p>" +
		"<img alt=3D\"pixel\" src=3D\"img/p.png\"><iframe src=3D\"cid:frame-2@mhtml.blink\"></iframe>" +
		"<a href=3D\"style.css\">css</a></body></html>\r\n" +
		"------B----\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Location: https://example.org/dir/img/p.png\r\n" +
		"\r\n" + img + "\r\n" +
		"------B----\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Location: https://example.org/favicon.png\r\n" +
		"\r\n" + img + "\r\n" +
		"------B----\r\n" +
		"Content-Type: text/css\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"Content-Location: https://example.org/dir/style.css\r\n" +
		"\r\n" +
		"p { color: red }\r\n" +
		"------B----\r\n" +
		"Content-Type: font/woff2\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Location: https://example.org/f.woff2\r\n" +
		"\r\n" +
		"d09GMg==\r\n" +
		"------B----\r\n" +
		"Content-Type: text/html; charset=windows-1252\r\n" +
		"Content-ID: <frame-2@mhtml.blink>\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"<p>In the frame: caf=E9</p>\r\n" +
		"------B------\r\n"
}

func extractMHT(t *testing.T, src string, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := (&Extractor{}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return doc
}

// childReader reads HTML children as the command does, recording what it
// was handed.
type childReader struct{ names, types []string }

func (c *childReader) read(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
	c.names = append(c.names, name)
	c.types = append(c.types, mime)
	if !strings.HasPrefix(mime, "text/html") {
		return nil, gofastcore.ErrUnsupported
	}
	cs := ""
	if _, p, ok := strings.Cut(mime, "charset="); ok {
		cs = p
	}
	return (&Extractor{Format: "html", Charset: cs}).Extract(ctx, r, size, o)
}

func TestMHTMLChrome(t *testing.T) {
	c := &childReader{}
	doc := extractMHT(t, chromeMHTML(), &gofastcore.Options{Name: "page.mhtml", Images: gofastcore.ImagesData, Child: c.read})
	if doc.Source.Format != "mht" || doc.Source.MIME != "multipart/related" {
		t.Fatalf("source %+v", doc.Source)
	}
	md := doc.Metadata
	if md.Title != "Café" || md.Application != "Blink" || md.Extra["url"] != "https://example.org/dir/page.html" ||
		md.Extra["subject"] != "Café page" || !md.Created.Equal(time.Date(2026, 11, 25, 16, 43, 29, 0, time.UTC)) {
		t.Fatalf("metadata %+v", md)
	}
	if len(doc.Parts) != 2 || doc.Parts[0].Kind != gofastcore.PartBody || doc.Parts[1].Kind != gofastcore.PartStored {
		t.Fatalf("parts %+v", doc.Parts)
	}
	var texts []string
	var img *gofastcore.Block
	for i, b := range doc.Parts[0].Blocks {
		texts = append(texts, b.Text)
		if b.Kind == gofastcore.KindImage {
			img = &doc.Parts[0].Blocks[i]
		}
	}
	if got := strings.Join(texts, "|"); !strings.Contains(got, "Bonjour|Un café au lait.|pixel") {
		t.Fatalf("blocks %q", got)
	}
	if img == nil || img.Image.ID != "https://example.org/dir/img/p.png" || img.Image.MIME != "image/png" || !bytes.Equal(img.Image.Data, pngPixel) {
		t.Fatalf("image %+v", img)
	}
	// The favicon no page shows is a stored manifest item; the style sheet
	// and the font are not listed.
	st := doc.Parts[1].Blocks
	if len(st) != 1 || st[0].Kind != gofastcore.KindImage || st[0].Attrs[gofastcore.AttrStored] != StoredManifestItem ||
		st[0].Image.ID != "https://example.org/favicon.png" || !bytes.Equal(st[0].Image.Data, pngPixel) {
		t.Fatalf("stored %+v", st)
	}
	// The frame is an embedded document, with its charset.
	if len(doc.Children) != 1 || c.names[0] != "cid:frame-2@mhtml.blink" || c.types[0] != "text/html; charset=windows-1252" {
		t.Fatalf("children %d %q %q", len(doc.Children), c.names, c.types)
	}
	if got := doc.Children[0].Parts[0].Blocks[0].Text; got != "In the frame: café" {
		t.Fatalf("frame %q", got)
	}
	for _, w := range doc.AllWarnings() {
		if w.Code == gofastcore.WarnInputExternalReference {
			t.Fatalf("a resource inside the file counted as external: %+v", w)
		}
	}
}

func TestMHTMLOptions(t *testing.T) {
	// Without images: no image data, no stored images.
	doc := extractMHT(t, chromeMHTML(), &gofastcore.Options{Images: gofastcore.ImagesNone})
	for _, p := range doc.Parts {
		for _, b := range p.Blocks {
			if b.Kind == gofastcore.KindImage {
				t.Fatalf("image block with ImagesNone in part %s", p.Kind)
			}
		}
	}
	// Without a child reader the frame is listed, not read.
	if len(doc.Children) != 1 || !anyWarning(doc.Children[0], gofastcore.WarnChildNotExtracted) {
		t.Fatalf("children %+v", doc.Children)
	}
	// Part 1 only: no stored part.
	doc = extractMHT(t, chromeMHTML(), &gofastcore.Options{Parts: gofastcore.PartRange{First: 1, Last: 1}})
	if len(doc.Parts) != 1 {
		t.Fatalf("%d parts", len(doc.Parts))
	}
	// A decode budget too small for the page.
	doc = extractMHT(t, chromeMHTML(), &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 16}})
	if !anyWarning(doc, gofastcore.WarnLimitExceeded) {
		t.Fatal("no limit.exceeded with a 16-byte decode budget")
	}
}

func anyWarning(doc *gofastcore.Document, code string) bool {
	for _, w := range doc.AllWarnings() {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestMHTMLDetect(t *testing.T) {
	src := []byte(chromeMHTML())
	for _, c := range []struct {
		name string
		src  []byte
		want string
	}{
		{"a.mht", nil, "mht"},
		{"a.MHTML", nil, "mht"},
		{"", src, "mht"},
		{"saved", []byte("From: <Saved by Windows Internet Explorer 8>\r\nContent-Type: multipart/related; boundary=x\r\n\r\n--x\r\n"), "mht"},
		// A message with a multipart/related body is not a saved page.
		{"", []byte("From: a@example.org\r\nTo: b@example.org\r\nContent-Type: multipart/related; boundary=x\r\n\r\n--x\r\n"), "txt"},
		{"", []byte("Snapshot-Content-Location: x\r\nContent-Type: text/plain\r\n\r\nhi"), "txt"},
	} {
		if got := Detect(c.name, c.src); got != c.want {
			t.Errorf("Detect(%q, %.40q) = %q, want %q", c.name, c.src, got, c.want)
		}
	}
}

func TestMHTMLInternetExplorer(t *testing.T) {
	// Internet Explorer puts the page in a multipart/alternative, with
	// relative locations.
	src := "From: <Saved by Windows Internet Explorer 8>\r\n" +
		"Subject: IE page\r\n" +
		"Content-Type: multipart/related;\r\n\ttype=\"multipart/alternative\";\r\n\tboundary=\"----=_NextPart_000\"\r\n" +
		"\r\n" +
		"------=_NextPart_000\r\n" +
		"Content-Type: multipart/alternative;\r\n\tboundary=\"----=_NextPart_001\"\r\n" +
		"\r\n" +
		"------=_NextPart_001\r\n" +
		"Content-Type: text/html;\r\n\tcharset=\"windows-1252\"\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"Content-Location: file://C:\\pages\\p.htm\r\n" +
		"\r\n" +
		"<P>Caf=E9 <IMG src=3D\"p.png\"></P>\r\n" +
		"------=_NextPart_001--\r\n" +
		"\r\n" +
		"------=_NextPart_000\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Location: p.png\r\n" +
		"\r\n" + base64.StdEncoding.EncodeToString(pngPixel) + "\r\n" +
		"------=_NextPart_000--\r\n"
	doc := extractMHT(t, src, &gofastcore.Options{Images: gofastcore.ImagesData})
	b := doc.Parts[0].Blocks
	if len(b) != 2 || b[0].Text != "Café" || b[1].Kind != gofastcore.KindImage || !bytes.Equal(b[1].Image.Data, pngPixel) {
		t.Fatalf("blocks %+v", b)
	}
	if doc.Metadata.Application != "Windows Internet Explorer 8" || len(doc.Parts) != 1 {
		t.Fatalf("metadata %+v, %d parts", doc.Metadata, len(doc.Parts))
	}
}

func TestMHTMLDamaged(t *testing.T) {
	// A binary part whose end has no line break: the boundary follows the
	// last byte, as Chromium's test files have it.
	bin := "From: <Saved by Blink>\r\nContent-Type: multipart/related; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\nContent-Transfer-Encoding: binary\r\n\r\n<p>one</p>--B\r\n" +
		"Content-Type: text/html\r\nContent-Location: f.html\r\n\r\n<p>two</p>\r\n--B--\r\n"
	doc := extractMHT(t, bin, &gofastcore.Options{})
	if len(doc.Parts[0].Blocks) != 1 || doc.Parts[0].Blocks[0].Text != "one" || len(doc.Children) != 1 || !anyWarning(doc, gofastcore.WarnInputRepaired) {
		t.Fatalf("binary: %+v, %d children", doc.Parts[0].Blocks, len(doc.Children))
	}
	// No closing boundary; a header with no fields at all; an image saved
	// on its own.
	for _, src := range []string{
		"From: <Saved by Blink>\r\nContent-Type: multipart/related; boundary=B\r\n\r\n--B\r\nContent-Type: text/html\r\n\r\n<p>cut",
		"<p>no header</p>",
		"From: <Saved by Blink>\r\nContent-Type: multipart/related; boundary=B\r\n\r\n--B\r\nContent-Type: image/png\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
			base64.StdEncoding.EncodeToString(pngPixel) + "\r\n--B--\r\n",
		"From: <Saved by Blink>\r\nContent-Type: multipart/related; boundary=B\r\n\r\nno boundary here",
		"From: <Saved by Blink>\r\nContent-Type: multipart/related; boundary=B\r\n\r\n--B\r\nContent-Type: application/pdf\r\n\r\n%PDF\r\n--B--\r\n",
		strings.Repeat("--B\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n", 20),
	} {
		doc, err := (&Extractor{Format: "mht"}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), nil)
		if err != nil {
			t.Fatalf("%.40q: %v", src, err)
		}
		if err := doc.Validate(); err != nil {
			t.Fatalf("%.40q: Validate: %v", src, err)
		}
	}
}

func TestMHTMLCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := chromeMHTML()
	if _, err := (&Extractor{}).Extract(ctx, strings.NewReader(src), int64(len(src)), &gofastcore.Options{Name: "a.mht"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

// FuzzMHTML reads arbitrary input as MHTML, with images and a child
// reader, under small limits, and checks the document against the
// contract.
//
//	go test -run XXX -fuzz FuzzMHTML -fuzztime 30s -fuzzminimizetime 0s
func FuzzMHTML(f *testing.F) {
	f.Add([]byte(chromeMHTML()))
	f.Add([]byte("Content-Type: multipart/related; boundary=b\n\n--b\nContent-Type: multipart/alternative; boundary=c\n\n--c\nContent-Type: text/html\n\n<p>x\n--c--\n--b\nContent-Location: x.png\nContent-Type: image/png\n\nzz--b--"))
	f.Fuzz(func(t *testing.T, b []byte) {
		start := time.Now()
		c := &childReader{}
		for _, opts := range []*gofastcore.Options{
			{Images: gofastcore.ImagesData, Child: c.read},
			{Limits: gofastcore.Limits{MaxTextBytes: 2048, MaxDecodedBytes: 4096}, Spans: true},
		} {
			e := &Extractor{Format: "mht", MaxDepth: 24, MaxArchiveMembers: 64, Charset: "windows-1252"}
			doc, err := e.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("%d bytes took %v", len(b), d)
		}
	})
}
