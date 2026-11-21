package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/epub"
)

// warnings lists the warnings of a code in the document and its parts,
// as "part: message", part 0 for the document.
func warnings(doc *gofastcore.Document, code string) []string {
	var out []string
	for p, w := range doc.AllWarnings() {
		if w.Code != code {
			continue
		}
		n := 0
		if p != nil {
			n = p.Number
		}
		out = append(out, fmt.Sprintf("%d: %s", n, w.Message))
	}
	return out
}

func extractBytes(b []byte, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
}

// chapterEPUB builds an EPUB 3 whose spine has the chapters given as
// (href, content) pairs, with nav as its navigation document when it is
// not empty, metadata added to the package's, and further files.
func chapterEPUB(t testing.TB, chapters []string, nav, metadata string, files ...string) []byte {
	return pub{chapters: chapters, nav: nav, metadata: metadata, files: files}.build(t)
}

// pub is an EPUB 3 to build: chapters as (href, content) pairs, a
// navigation document, and what to add to the package's metadata, its
// manifest and the start of its spine, and further files.
type pub struct {
	chapters                  []string
	nav, metadata             string
	manifestExtra, spineStart string
	files                     []string
}

func (p pub) build(t testing.TB) []byte {
	chapters, nav, metadata, files := p.chapters, p.nav, p.metadata, p.files
	var manifest, spine strings.Builder
	manifest.WriteString(p.manifestExtra)
	spine.WriteString(p.spineStart)
	var members []string
	for i := 0; i+1 < len(chapters); i += 2 {
		fmt.Fprintf(&manifest, `<item id="c%d" href="%s" media-type="application/xhtml+xml"/>`, i, chapters[i])
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, i)
		members = append(members, "OEBPS/"+chapters[i], chapters[i+1])
	}
	if nav != "" {
		manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
		members = append(members, "OEBPS/nav.xhtml", nav)
	}
	opf := `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">` +
		`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">x</dc:identifier><dc:title>Book</dc:title>` + metadata + `</metadata>` +
		`<manifest>` + manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`
	all := append([]string{"META-INF/container.xml", container, "OEBPS/content.opf", opf}, members...)
	return buildEPUB(t, append(all, files...)...)
}

func xhtml(title, body string) string {
	return `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + title + `</title></head><body>` + body + `</body></html>`
}

func navOf(ol string) string {
	return `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>n</title></head><body><nav epub:type="toc">` + ol + `</nav></body></html>`
}

// An encryption.xml that cannot be read in full leaves no chapter read
// as text: which members are encrypted is not known.
func TestEPUBEncryptionUnread(t *testing.T) {
	entry := `<EncryptedData xmlns="http://www.w3.org/2001/04/xmlenc#"><EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes128-cbc"/>` +
		`<CipherData><CipherReference URI="OEBPS/c2.xhtml"/></CipherData></EncryptedData>`
	big := `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container">` + entry + "<!--" + strings.Repeat("p", epub.MaxXML) + "--></encryption>"
	broken := `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container">` + entry + "<EncryptedData"
	chapters := []string{"c1.xhtml", xhtml("T", "<p>chapter one</p>"), "c2.xhtml", "CIPHERTEXT\x8f\x01"}
	nav := navOf(`<ol><li><a href="c1.xhtml">One</a></li></ol>`)
	for _, c := range []struct {
		name, enc string
		want      error
	}{
		{"larger than MaxXML", big, gofastcore.ErrLimitExceeded},
		{"cut short", broken, gofastcore.ErrMalformed},
	} {
		b := chapterEPUB(t, chapters, nav, "", "META-INF/encryption.xml", c.enc)
		doc, err := extractBytes(b, nil)
		if !errors.Is(err, c.want) || doc != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}

	// With a spine entry that is not a content document, there is a
	// document, and its chapter is reported as possibly encrypted.
	b := pub{chapters: chapters[2:], nav: nav, spineStart: `<itemref idref="missing"/>`,
		files: []string{"META-INF/encryption.xml", broken}}.build(t)
	doc, err := extractBytes(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Parts) != 2 || !doc.Parts[1].Has(gofastcore.WarnPartFailed) || !doc.Parts[1].Has(WarnEncrypted) || len(doc.Parts[1].Blocks) != 0 {
		t.Errorf("chapter 2: %+v", doc.Parts)
	}
	if len(doc.Outline) != 0 {
		t.Errorf("the navigation document was read: %+v", doc.Outline)
	}
	if w := warnings(doc, WarnEncrypted); len(w) != 2 || !strings.Contains(w[0], "encryption.xml") {
		t.Errorf("epub.encrypted: %q", w)
	}
	for _, p := range doc.Parts {
		for _, bl := range p.Blocks {
			if strings.Contains(bl.Text, "CIPHER") {
				t.Errorf("ciphertext read as text: %q", bl.Text)
			}
		}
	}
}

// Whenever the text budget stops the reading of chapters, the document
// says so.
func TestEPUBTextBudgetWarns(t *testing.T) {
	var chapters []string
	for i := range 5 {
		chapters = append(chapters, fmt.Sprintf("c%d.xhtml", i), xhtml(fmt.Sprintf("LongChapterTitle%d", i), fmt.Sprintf("<p>body%d</p>", i)))
	}
	b := chapterEPUB(t, chapters, "", "")
	full, err := extractBytes(b, nil)
	if err != nil || len(full.Parts) != 5 {
		t.Fatalf("%v %d parts", err, len(full.Parts))
	}
	nameCut := false
	for limit := int64(1); limit < 1500; limit++ {
		doc, err := extractBytes(b, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: limit}})
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		lost := len(doc.Parts) < len(full.Parts)
		for i := range doc.Parts {
			if doc.Parts[i].Name != full.Parts[i].Name || len(doc.Parts[i].Blocks) != len(full.Parts[i].Blocks) {
				lost = true
			}
			if doc.Parts[i].Name == "" && len(doc.Parts[i].Blocks) == len(full.Parts[i].Blocks) {
				nameCut = true
			}
		}
		if lost && len(warnings(doc, gofastcore.WarnLimitExceeded)) == 0 {
			t.Fatalf("limit %d: %d parts and no limit.exceeded", limit, len(doc.Parts))
		}
		if !lost {
			break
		}
	}
	if !nameCut {
		t.Error("no limit cut a chapter's name alone")
	}
}

// The bounds on the table of contents and the package's metadata are
// reported.
func TestEPUBOutlineAndMetadataBounds(t *testing.T) {
	chapters := []string{"c1.xhtml", xhtml("T", "<p>one</p>")}
	long := strings.Repeat("t", epub.MaxTitle+10)

	// A navigation document larger than MaxXML.
	bigNav := strings.Replace(navOf(`<ol><li><a href="c1.xhtml">One</a></li></ol>`), "<body>", "<body><!--"+strings.Repeat("p", epub.MaxXML)+"-->", 1)
	doc := extractEPUB(t, chapterEPUB(t, chapters, bigNav, ""), nil)
	if w := warnings(doc, gofastcore.WarnLimitExceeded); len(w) != 1 || !strings.Contains(w[0], "navigation document") {
		t.Errorf("big nav: %q", w)
	}

	// A title cut, the same whether its text is one node or several.
	for _, a := range []string{long, "<span>" + long[:10] + "</span><span>" + long[10:] + "</span>"} {
		doc = extractEPUB(t, chapterEPUB(t, chapters, navOf(`<ol><li><a href="c1.xhtml">`+a+`</a></li></ol>`), ""), nil)
		if w := warnings(doc, gofastcore.WarnLimitExceeded); len(w) != 1 || len(doc.Outline) != 1 || len(doc.Outline[0].Title) != epub.MaxTitle {
			t.Errorf("long title: %q, %d bytes", w, len(doc.Outline[0].Title))
		}
	}

	// Lists nested deeper than MaxLevel.
	deep := ""
	for range epub.MaxLevel + 2 {
		deep = `<ol><li><a href="c1.xhtml">L</a>` + deep + `</li></ol>`
	}
	doc = extractEPUB(t, chapterEPUB(t, chapters, navOf(deep), ""), nil)
	if w := warnings(doc, gofastcore.WarnLimitExceeded); len(w) != 1 || len(doc.Outline) != epub.MaxLevel {
		t.Errorf("deep: %q, %d entries", w, len(doc.Outline))
	}

	// More entries than MaxEntries.
	var ol strings.Builder
	ol.WriteString("<ol>")
	for range epub.MaxEntries + 3 {
		ol.WriteString(`<li><a href="c1.xhtml">E</a></li>`)
	}
	ol.WriteString("</ol>")
	doc = extractEPUB(t, chapterEPUB(t, chapters, navOf(ol.String()), ""), nil)
	if w := warnings(doc, gofastcore.WarnLimitExceeded); len(w) != 1 || len(doc.Outline) != epub.MaxEntries {
		t.Errorf("many entries: %q, %d entries", w, len(doc.Outline))
	}

	// Metadata: a description cut at 64 KiB wherever its text is split,
	// and Dublin Core elements past MaxDC.
	desc := strings.Repeat("word ", 20000)
	for _, d := range []string{desc, desc[:30000] + "<!--c-->" + desc[30000:]} {
		doc = extractEPUB(t, chapterEPUB(t, chapters, "", "<dc:description>"+d+"</dc:description>"), nil)
		if w := warnings(doc, WarnMetadataTruncated); len(w) != 1 || len(doc.Metadata.Extra["description"]) > epub.MaxMeta || len(doc.Metadata.Extra["description"]) < epub.MaxMeta-8 {
			t.Errorf("description: %q, %d bytes", w, len(doc.Metadata.Extra["description"]))
		}
	}
	var creators strings.Builder
	for i := range epub.MaxDC + 5 {
		fmt.Fprintf(&creators, "<dc:creator>C%d</dc:creator>", i)
	}
	doc = extractEPUB(t, chapterEPUB(t, chapters, "", creators.String()), nil)
	if w := warnings(doc, WarnMetadataTruncated); len(w) != 1 || !strings.Contains(w[0], "7 were left out") {
		t.Errorf("creators: %q, %d authors", w, len(doc.Metadata.Authors))
	}

	// None of it for a publication within the bounds.
	doc = extractEPUB(t, chapterEPUB(t, chapters, navOf(`<ol><li><a href="c1.xhtml">`+long[:epub.MaxTitle]+`</a></li></ol>`), "<dc:creator>A</dc:creator>"), nil)
	if len(doc.Warnings) != 0 {
		t.Errorf("within the bounds: %+v", doc.Warnings)
	}
}

// container.xml or a package document larger than MaxXML is a limit,
// not a malformed file.
func TestEPUBPackageTooLarge(t *testing.T) {
	pad := "<!--" + strings.Repeat("p", epub.MaxXML) + "-->"
	b := chapterEPUB(t, []string{"c1.xhtml", xhtml("T", "<p>one</p>")}, "", pad)
	if _, err := extractBytes(b, nil); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("package: %v", err)
	}
	b = buildEPUB(t, "META-INF/container.xml", container+pad, "OEBPS/content.opf", opf3)
	if _, err := extractBytes(b, nil); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("container: %v", err)
	}
}

// An image no chapter shows whose data passes the limit on decoded bytes
// is reported.
func TestEPUBStoredImageBudget(t *testing.T) {
	b := pub{chapters: []string{"c1.xhtml", xhtml("T", "<p>one</p>")},
		manifestExtra: `<item id="pic" href="pic.png" media-type="image/png"/>`,
		files:         []string{"OEBPS/pic.png", "\x89PNG" + strings.Repeat("\x00", 200000)}}.build(t)
	doc := extractEPUB(t, b, &gofastcore.Options{Images: gofastcore.ImagesData, Limits: gofastcore.Limits{MaxDecodedBytes: 100000}})
	stored := doc.Parts[len(doc.Parts)-1]
	if stored.Kind != gofastcore.PartStored || stored.Blocks[0].Image.Data != nil || !stored.Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("stored %+v", stored)
	}
	doc = extractEPUB(t, b, &gofastcore.Options{Images: gofastcore.ImagesData})
	if stored := doc.Parts[len(doc.Parts)-1]; len(stored.Blocks[0].Image.Data) == 0 || len(stored.Warnings) != 0 {
		t.Errorf("within the limit %+v", stored.Warnings)
	}
}

// Encodings that are not decoded are reported once for the publication,
// however many chapters declare them.
func TestEPUBCharsetsAggregated(t *testing.T) {
	var chapters []string
	for i := range 40 {
		label := "x-foo"
		if i%10 == 0 {
			label = "x-bar"
		}
		chapters = append(chapters, fmt.Sprintf("c%d.xhtml", i), `<html><head><meta charset="`+label+`"/></head><body><p>ch</p></body></html>`)
	}
	doc := extractEPUB(t, chapterEPUB(t, chapters, "", ""), nil)
	w := warnings(doc, WarnCharsetUnsupported)
	if len(w) != 1 || !strings.Contains(w[0], `40 chapters`) || !strings.Contains(w[0], `"x-foo" in 36`) || !strings.Contains(w[0], `"x-bar" in 4`) {
		t.Errorf("%q", w)
	}
	// Past eight labels, the rest are counted.
	chapters = nil
	for i := range 12 {
		chapters = append(chapters, fmt.Sprintf("c%d.xhtml", i), fmt.Sprintf(`<html><head><meta charset="x-l%d"/></head><body><p>ch</p></body></html>`, i))
	}
	doc = extractEPUB(t, chapterEPUB(t, chapters, "", ""), nil)
	if w := warnings(doc, WarnCharsetUnsupported); len(w) != 1 || !strings.Contains(w[0], "and 4 more") {
		t.Errorf("%q", w)
	}
}

// The bounds of the tree builder that lose spans or declared display
// are reported once in the part.
func TestBuilderBounds(t *testing.T) {
	limit := func(doc *gofastcore.Document) []string { return warnings(doc, gofastcore.WarnLimitExceeded) }
	open := func(n int) string {
		var b strings.Builder
		b.WriteString(`<p><a href="http://link/">`)
		for i := range n {
			fmt.Fprintf(&b, "<b id=b%d>", i)
		}
		return b.String() + "start</p><p>after</p><p>more</p>"
	}
	if w := limit(extract(t, open(70), nil)); len(w) != 1 || !strings.Contains(w[0], "formatting elements") {
		t.Errorf("formatting: %q", w)
	}
	if w := limit(extract(t, open(10), nil)); len(w) != 0 {
		t.Errorf("few formatting elements: %q", w)
	}

	style := `<p style="--pad:` + strings.Repeat("x", 16<<10) + `;display:none">a</p><p style="--pad:` + strings.Repeat("x", 16<<10) + `">b</p><p style="display:none">c</p>`
	if w := limit(extract(t, style, nil)); len(w) != 1 || !strings.HasPrefix(w[0], "1: 2 style attributes") {
		t.Errorf("style: %q", w)
	}
	// In an iframe srcdoc document too.
	srcdoc := `<iframe srcdoc="&lt;p style='--pad:` + strings.Repeat("x", 16<<10) + `'&gt;f"></iframe><p>x</p>`
	if w := limit(extract(t, srcdoc, nil)); len(w) != 1 || !strings.Contains(w[0], "style attributes") {
		t.Errorf("srcdoc style: %q", w)
	}

	var clones strings.Builder
	clones.WriteString(`<p><font style="display:none">`)
	for i := range 30 {
		fmt.Fprintf(&clones, "<i id=i%d>", i)
	}
	clones.WriteString("x")
	for range 400 {
		clones.WriteString("<p>y")
	}
	doc := extract(t, clones.String(), nil)
	if w := limit(doc); len(w) != 1 || !strings.Contains(w[0], "reopened after blocks") {
		t.Errorf("clones: %q", w)
	}
	last := doc.Parts[0].Blocks[len(doc.Parts[0].Blocks)-1]
	if last.Text != "y" || last.Display != gofastcore.DisplayHidden {
		t.Errorf("the declared display past the bound: %+v", last)
	}
}

// An image a chapter shows that encryption.xml lists has no data, and the
// chapter says so.
func TestEPUBEncryptedImage(t *testing.T) {
	enc := `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><EncryptedData xmlns="http://www.w3.org/2001/04/xmlenc#">` +
		`<EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes128-cbc"/><CipherData><CipherReference URI="OEBPS/pic.png"/></CipherData></EncryptedData></encryption>`
	b := pub{chapters: []string{"c1.xhtml", xhtml("T", `<p>one</p><img src="pic.png" alt="p"/><img src="pic.png" alt="q"/>`)},
		manifestExtra: `<item id="pic" href="pic.png" media-type="image/png"/>`,
		files:         []string{"OEBPS/pic.png", "CIPHERTEXT", "META-INF/encryption.xml", enc}}.build(t)
	doc := extractEPUB(t, b, &gofastcore.Options{Images: gofastcore.ImagesData})
	p := doc.Parts[0]
	n := 0
	for _, bl := range p.Blocks {
		if bl.Image != nil {
			n++
			if bl.Image.Data != nil {
				t.Errorf("encrypted image data read: %q", bl.Image.Data)
			}
		}
	}
	if w := warnings(doc, WarnEncrypted); n != 2 || len(w) != 2 || w[1] != "1: 2 images the chapter shows are encrypted; their data was not read" {
		t.Errorf("%d images, %q", n, w)
	}
}
