package gofasthtml

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// buildEPUB writes an EPUB archive: the mimetype first, stored, then the
// files in the order given.
func buildEPUB(t testing.TB, files ...string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	io.WriteString(w, "application/epub+zip")
	for i := 0; i+1 < len(files); i += 2 {
		w, err := zw.Create(files[i])
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, files[i+1])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const container = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

const opf3 = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
  <dc:identifier id="uid">urn:uuid:1234</dc:identifier>
  <dc:identifier>isbn:978</dc:identifier>
  <dc:title>The  Book</dc:title>
  <dc:creator>Ann Author</dc:creator><dc:creator>Bob Writer</dc:creator>
  <dc:language>en</dc:language>
  <dc:subject>Fiction</dc:subject>
  <dc:description>A story.</dc:description>
  <dc:date>2020-05-01</dc:date>
  <meta property="dcterms:modified">2021-01-02T03:04:05Z</meta>
</metadata>
<manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  <item id="c1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
  <item id="c2" href="text/ch%202.xhtml" media-type="application/xhtml+xml"/>
  <item id="notes" href="text/notes.xhtml" media-type="application/xhtml+xml"/>
  <item id="extra" href="extra.xhtml" media-type="application/xhtml+xml"/>
  <item id="pic" href="img/pic.png" media-type="image/png"/>
  <item id="cover" href="img/cover.jpg" media-type="image/jpeg" properties="cover-image"/>
  <item id="css" href="style.css" media-type="text/css"/>
  <item id="font" href="f.otf" media-type="font/otf"/>
</manifest>
<spine><itemref idref="c1"/><itemref idref="c2"/><itemref idref="notes" linear="no"/></spine>
</package>`

const nav = `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>Nav</title></head><body>
<nav epub:type="toc"><ol>
  <li><a href="text/ch1.xhtml">One</a><ol><li><a href="text/ch1.xhtml#s">One A</a></li></ol></li>
  <li><a href="text/ch%202.xhtml">Two</a></li>
</ol></nav></body></html>`

func sampleEPUB(t testing.TB) []byte {
	return buildEPUB(t,
		"META-INF/container.xml", container,
		"OEBPS/content.opf", opf3,
		"OEBPS/nav.xhtml", nav,
		"OEBPS/text/ch1.xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Ch 1</title>
<link rel="stylesheet" href="../style.css"/></head><body><h1>Chapter One</h1><a id="top"/><p>First <b>para</b>.</p>
<img src="../img/pic.png" alt="A picture"/><p class="x" hidden="hidden">Hidden note.</p><hr/><p>After.</p></body></html>`,
		"OEBPS/text/ch 2.xhtml", `<html><body><h1>Two</h1><p>Second chapter.</p></body></html>`,
		"OEBPS/text/notes.xhtml", `<html><body><p>A note.</p></body></html>`,
		"OEBPS/extra.xhtml", `<html><body><p>Not in the spine.</p></body></html>`,
		"OEBPS/img/pic.png", "\x89PNG pic",
		"OEBPS/img/cover.jpg", "\xFF\xD8 cover",
		"OEBPS/style.css", "p.x { display: none }",
		"OEBPS/f.otf", "font",
	)
}

func extractEPUB(t testing.TB, b []byte, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := (&Extractor{}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestEPUB(t *testing.T) {
	b := sampleEPUB(t)
	var children []string
	opts := &gofastcore.Options{Images: gofastcore.ImagesData,
		Child: func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
			children = append(children, name)
			if mime == "application/xhtml+xml" {
				return (&Extractor{Format: "html"}).Extract(ctx, r, size, o)
			}
			return nil, gofastcore.ErrUnsupported
		}}
	doc := extractEPUB(t, b, opts)
	if doc.Source.Format != "epub" {
		t.Fatalf("format %q", doc.Source.Format)
	}
	m := doc.Metadata
	if m.Title != "The Book" || len(m.Authors) != 2 || m.Language != "en" || len(m.Keywords) != 1 ||
		m.Extra["description"] != "A story." || m.Extra["identifier"] != "urn:uuid:1234\nisbn:978" ||
		m.Created.Year() != 2020 || m.Modified.Year() != 2021 {
		t.Errorf("metadata %+v", m)
	}
	want := []struct {
		title string
		level int
		part  int
	}{{"One", 1, 1}, {"One A", 2, 1}, {"Two", 1, 2}}
	if len(doc.Outline) != len(want) {
		t.Fatalf("outline %+v", doc.Outline)
	}
	for i, w := range want {
		if o := doc.Outline[i]; o.Title != w.title || o.Level != w.level || o.Part != w.part {
			t.Errorf("outline[%d] = %+v", i, o)
		}
	}
	if len(doc.Parts) != 4 {
		t.Fatalf("%d parts", len(doc.Parts))
	}
	p1 := doc.Parts[0]
	if p1.Kind != gofastcore.PartChapter || p1.Number != 1 || p1.Name != "Ch 1" {
		t.Errorf("part 1 %+v", p1)
	}
	got := show(&gofastcore.Document{Parts: []gofastcore.Part{p1}})
	if !strings.Contains(got, `heading1"Chapter One" paragraph"First para." image"A picture" paragraph{hidden}"Hidden note."`) {
		t.Errorf("chapter 1: %s", got)
	}
	for _, blk := range p1.Blocks {
		if blk.Kind == gofastcore.KindImage && (blk.Image.ID != "OEBPS/img/pic.png" || blk.Image.MIME != "image/png" || string(blk.Image.Data) != "\x89PNG pic") {
			t.Errorf("image %+v", blk.Image)
		}
	}
	// Offsets are into the chapter's own source.
	if l := p1.Blocks[0].Loc; l.Offset == 0 || l.Length != int64(len("<h1>Chapter One</h1>")) {
		t.Errorf("loc %+v", l)
	}
	if doc.Parts[1].Name != "Two" {
		t.Errorf("part 2 name %q", doc.Parts[1].Name)
	}
	if !doc.Parts[2].Has(WarnNonLinear) {
		t.Error("non-linear chapter not noted")
	}
	stored := doc.Parts[3]
	if stored.Kind != gofastcore.PartStored || len(stored.Blocks) != 1 || stored.Blocks[0].Image.ID != "OEBPS/img/cover.jpg" ||
		stored.Blocks[0].Text != "cover" || stored.Display != 0 || stored.Blocks[0].Display != 0 {
		t.Errorf("stored %+v", stored)
	}
	if strings.Join(children, ",") != "extra.xhtml,f.otf" || len(doc.Children) != 2 {
		t.Errorf("children %v, %d", children, len(doc.Children))
	}
	if doc.Children[0].Parts[0].Blocks[0].Text != "Not in the spine." {
		t.Errorf("child %+v", doc.Children[0])
	}
	if c := doc.Children[1]; c.Source.Name != "f.otf" || len(c.Warnings) != 1 || c.Warnings[0].Code != gofastcore.WarnChildUnsupported {
		t.Errorf("font child %+v", c)
	}
	for _, w := range doc.Warnings {
		if w.Code == gofastcore.WarnInputExternalReference {
			t.Errorf("archive members counted as external: %+v", w)
		}
	}

	// A range of chapters, without the stored part.
	doc = extractEPUB(t, b, &gofastcore.Options{Parts: gofastcore.PartRange{First: 2, Last: 2}})
	if len(doc.Parts) != 1 || doc.Parts[0].Number != 2 {
		t.Errorf("range: %+v", doc.Parts)
	}
	if _, err := Extract(context.Background(), bytes.NewReader(b), int64(len(b)), &gofastcore.Options{Parts: gofastcore.PartRange{First: 9}}); !errors.Is(err, gofastcore.ErrNoPartsSelected) {
		t.Errorf("range beyond: %v", err)
	}
	doc = extractEPUB(t, b, &gofastcore.Options{Limits: gofastcore.Limits{MaxParts: 1}})
	if len(doc.Parts) != 1 {
		t.Errorf("max parts: %d", len(doc.Parts))
	}
}

// The navigation document in the spine declares its landmarks and page
// list hidden with the hidden attribute, as EPUB 3 provides: that is a
// declaration like any other, and the blocks get the reason.
func TestEPUBNavInSpine(t *testing.T) {
	opf := strings.Replace(opf3, `<spine>`, `<spine><itemref idref="nav"/>`, 1)
	navDoc := strings.Replace(nav, `</body>`, `<nav epub:type="landmarks" hidden=""><ol><li><a epub:type="bodymatter" href="text/ch1.xhtml">Start</a></li></ol></nav>`+
		`<nav epub:type="page-list" hidden="hidden"><ol><li><a href="text/ch1.xhtml#p1">1</a></li></ol></nav><p hidden="">Secret.</p></body>`, 1)
	b := buildEPUB(t, "META-INF/container.xml", container, "OEBPS/content.opf", opf, "OEBPS/nav.xhtml", navDoc,
		"OEBPS/text/ch1.xhtml", `<html><body><p>One.</p></body></html>`)
	doc := extractEPUB(t, b, nil)
	hidden := map[string]bool{}
	for _, blk := range doc.Parts[0].Blocks {
		if blk.Display != 0 {
			if blk.Display != gofastcore.DisplayHidden {
				t.Errorf("%q has %s", blk.Text, blk.Display)
			}
			hidden[blk.Text] = true
		}
	}
	if !hidden["Start"] || !hidden["1"] || !hidden["Secret."] || len(hidden) != 3 {
		t.Errorf("hidden blocks %v", hidden)
	}
}

// Invisible and bidirectional characters in a chapter survive unchanged.
func TestEPUBCharactersKept(t *testing.T) {
	b := buildEPUB(t, "META-INF/container.xml", container, "OEBPS/content.opf", opf3, "OEBPS/nav.xhtml", nav,
		"OEBPS/text/ch1.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>`+r6Text+`</p></body></html>`)
	doc := extractEPUB(t, b, nil)
	found := false
	for _, blk := range doc.AllBlocks() {
		if blk.Text == r6Text && blk.Display == 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("characters not kept: %s", show(doc))
	}
	noRemovedWarnings(t, "epub", doc)
}

func TestEPUB2(t *testing.T) {
	opf := `<package version="2.0" unique-identifier="id" xmlns="http://www.idpf.org/2007/opf">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Old</dc:title><meta name="cover" content="c"/></metadata>
<manifest><item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
<item id="a" href="a.html" media-type="application/xhtml+xml"/></manifest>
<spine toc="ncx"><itemref idref="a"/></spine></package>`
	ncx := `<ncx><navMap><navPoint id="p1"><navLabel><text>Start</text></navLabel><content src="a.html#x"/>
<navPoint id="p2"><navLabel><text>Inner</text></navLabel><content src="a.html"/></navPoint></navPoint></navMap></ncx>`
	b := buildEPUB(t, "META-INF/container.xml", container, "OEBPS/content.opf", opf, "OEBPS/toc.ncx", ncx,
		"OEBPS/a.html", "<p>Text</p>")
	doc := extractEPUB(t, b, nil)
	if len(doc.Outline) != 2 || doc.Outline[0].Title != "Start" || doc.Outline[1].Level != 2 || doc.Outline[1].Part != 1 {
		t.Errorf("outline %+v", doc.Outline)
	}
	if doc.Parts[0].Name != "Start" || doc.Metadata.Title != "Old" {
		t.Errorf("name %q title %q", doc.Parts[0].Name, doc.Metadata.Title)
	}
}

func TestEPUBEncrypted(t *testing.T) {
	enc := `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:enc="http://www.w3.org/2001/04/xmlenc#">
<enc:EncryptedData><enc:EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes128-cbc"/>
<enc:CipherData><enc:CipherReference URI="OEBPS/text/ch1.xhtml"/></enc:CipherData></enc:EncryptedData>
<enc:EncryptedData><enc:EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/>
<enc:CipherData><enc:CipherReference URI="OEBPS/f.otf"/></enc:CipherData></enc:EncryptedData></encryption>`
	files := []string{"META-INF/container.xml", container, "META-INF/encryption.xml", enc, "OEBPS/content.opf", opf3,
		"OEBPS/nav.xhtml", nav, "OEBPS/text/ch1.xhtml", "\x01\x02 ciphertext", "OEBPS/text/ch 2.xhtml", "<p>Plain</p>",
		"OEBPS/text/notes.xhtml", "<p>n</p>"}
	doc := extractEPUB(t, buildEPUB(t, files...), nil)
	if !doc.Parts[0].Has(gofastcore.WarnPartFailed) || !doc.Parts[0].Has(WarnEncrypted) || len(doc.Parts[0].Blocks) != 0 {
		t.Errorf("part 1 %+v", doc.Parts[0])
	}
	if doc.Parts[1].Blocks[0].Text != "Plain" {
		t.Errorf("part 2 %+v", doc.Parts[1])
	}
	// Every chapter encrypted: nothing usable.
	b := buildEPUB(t, "META-INF/container.xml", container, "META-INF/encryption.xml", enc,
		"OEBPS/content.opf", strings.Replace(opf3, `<itemref idref="c2"/><itemref idref="notes" linear="no"/>`, "", 1),
		"OEBPS/text/ch1.xhtml", "x")
	if _, err := Extract(context.Background(), bytes.NewReader(b), int64(len(b)), nil); !errors.Is(err, gofastcore.ErrUnsupported) {
		t.Errorf("all encrypted: %v", err)
	}
}

func TestEPUBDamaged(t *testing.T) {
	b := sampleEPUB(t)
	// Cut off the central directory: the members are found by their
	// local headers.
	cut := b[:bytes.LastIndex(b, []byte("PK\x01\x02"))-1]
	cut = cut[:bytes.Index(cut, []byte("PK\x01\x02"))]
	doc := extractEPUB(t, cut, nil)
	found := false
	for _, w := range doc.Warnings {
		found = found || w.Code == gofastcore.WarnInputRepaired
	}
	if !found || len(doc.Parts) < 2 {
		t.Errorf("repaired: %v, %d parts", found, len(doc.Parts))
	}
	// Not an archive at all.
	if _, err := (&Extractor{Format: "epub"}).Extract(context.Background(), strings.NewReader("nope"), 4, nil); !errors.Is(err, gofastcore.ErrMalformed) {
		t.Errorf("not zip: %v", err)
	}
	// Too many members.
	if _, err := (&Extractor{MaxArchiveMembers: 3}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)), nil); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("members: %v", err)
	}
	// A decompression budget smaller than a chapter.
	doc = extractEPUB(t, b, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 2500}})
	limited := false
	for p, w := range doc.AllWarnings() {
		_ = p
		limited = limited || w.Code == gofastcore.WarnLimitExceeded
	}
	if !limited {
		t.Error("no limit.exceeded with a small decode budget")
	}
}

// Run with -fuzzminimizetime=2s: see FuzzExtract.
func FuzzEPUB(f *testing.F) {
	f.Add(sampleEPUB(f))
	f.Fuzz(func(t *testing.T, b []byte) {
		doc, err := (&Extractor{Format: "epub", MaxArchiveMembers: 64}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)),
			&gofastcore.Options{Images: gofastcore.ImagesData, Limits: gofastcore.Limits{MaxDecodedBytes: 1 << 20, MaxTextBytes: 1 << 16}})
		if err != nil {
			return
		}
		if err := doc.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})
}
