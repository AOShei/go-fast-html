package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
)

func TestResolve(t *testing.T) {
	for _, c := range []struct {
		base, ref, want string
		ok              bool
	}{
		{"OEBPS/text/a.xhtml", "../img/p%20q.png#x", "OEBPS/img/p q.png", true},
		{"OEBPS/a.xhtml", "b.xhtml?y", "OEBPS/b.xhtml", true},
		{"OEBPS/a.xhtml", "#frag", "OEBPS/a.xhtml", true},
		{"OEBPS/a.xhtml", "/root.xhtml", "root.xhtml", true},
		{"OEBPS/a.xhtml", "../../etc/passwd", "", false},
		{"OEBPS/a.xhtml", "http://x/y", "", false},
		{"", "META-INF/x.xml", "META-INF/x.xml", true},
	} {
		got, ok := Resolve(c.base, c.ref)
		if got != c.want || ok != c.ok {
			t.Errorf("Resolve(%q, %q) = %q, %v", c.base, c.ref, got, ok)
		}
	}
}

// A navPoint that closes inside its text element must not leave the
// text element pointing at no entry.
func TestNCXUnbalanced(t *testing.T) {
	for _, src := range []string{
		`<ncx><navMap><navPoint><text>A</navPoint></text></navMap></ncx>`,
		`<ncx><navPoint><text></navPoint>x</text><text>y</text></ncx>`,
	} {
		NCX("toc.ncx", []byte(src))
	}
}

func TestNCXAndNav(t *testing.T) {
	ncx, _ := NCX("OPS/toc.ncx", []byte(`<ncx><navMap><navPoint><navLabel><text>A</text></navLabel><content src="a.html"/>
<navPoint><navLabel><text>B</text></navLabel><content src="sub/b.html#f"/></navPoint></navPoint>
<navPoint><navLabel><text>C</text></navLabel></navPoint></navMap></ncx>`))
	want := []Entry{{"A", 1, "OPS/a.html"}, {"B", 2, "OPS/sub/b.html"}, {"C", 1, ""}}
	if len(ncx) != 3 || ncx[0] != want[0] || ncx[1] != want[1] || ncx[2] != want[2] {
		t.Errorf("NCX %+v", ncx)
	}
	nav, _ := Nav(context.Background(), "OPS/nav.xhtml", []byte(`<html><body><nav epub:type="landmarks"><ol><li><a href="x.xhtml">L</a></li></ol></nav>
<nav epub:type="toc"><h1>Contents</h1><ol><li><a href="a.xhtml">One <em>1</em></a><ol><li><span>Part</span></li></ol></li></ol></nav></body></html>`))
	if len(nav) != 2 || nav[0].Title != "One 1" || nav[0].Path != "OPS/a.xhtml" || nav[1].Level != 2 || nav[1].Path != "" {
		t.Errorf("Nav %+v", nav)
	}
}

func zipOf(t *testing.T, files ...string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i+1 < len(files); i += 2 {
		w, _ := zw.Create(files[i])
		w.Write([]byte(files[i+1]))
	}
	zw.Close()
	return buf.Bytes()
}

func TestArchive(t *testing.T) {
	b := zipOf(t, "a", "hello", "b", "world", "a", "again")
	a, err := Open(b, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Members) != 2 || len(a.Duplicates) != 1 {
		t.Errorf("members %d duplicates %v", len(a.Members), a.Duplicates)
	}
	_, budget := gofastcore.WithDecodeBudget(context.Background(), gofastcore.Limits{MaxDecodedBytes: 8}, 0)
	if d, err := a.Read("a", budget, -1); err != nil || string(d) != "hello" {
		t.Errorf("read a: %q %v", d, err)
	}
	if _, err := a.Read("b", budget, -1); !errors.Is(err, ErrBudget) {
		t.Errorf("budget: %v", err)
	}
	if _, err := a.Read("zz", budget, -1); !errors.Is(err, ErrMissing) {
		t.Errorf("missing: %v", err)
	}
	if _, err := Open(b, 1); !errors.Is(err, ErrTooManyMembers) {
		t.Errorf("limit: %v", err)
	}
	// Without the central directory.
	cut := b[:bytes.Index(b, []byte("PK\x01\x02"))]
	a, err = Open(cut, -1)
	if err != nil || !a.Repaired || len(a.Members) != 2 {
		t.Fatalf("repaired %v %+v", err, a)
	}
	_, budget = gofastcore.WithDecodeBudget(context.Background(), gofastcore.Limits{}, 1<<20)
	if d, err := a.Read("b", budget, -1); err != nil || string(d) != "world" {
		t.Errorf("read b repaired: %q %v", d, err)
	}
	if _, err := Open([]byte("not a zip"), -1); !errors.Is(err, ErrNotZip) {
		t.Errorf("not zip: %v", err)
	}
}

func TestPackageAndEncryption(t *testing.T) {
	b := zipOf(t,
		"META-INF/container.xml", `<container><rootfiles><rootfile full-path="pkg/p.opf"/></rootfiles></container>`,
		"pkg/p.opf", `<package version="3.0" unique-identifier="u"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:title id="t">T</dc:title><dc:identifier id="u">X</dc:identifier><meta property="dcterms:modified">2020</meta><meta name="cover" content="c"/></metadata>
<manifest><item id="a" href="a.xhtml" media-type="Application/XHTML+xml"/><item id="a" href="dup.xhtml"/></manifest>
<spine toc="n"><itemref idref="a" linear="no"/></spine></package>`,
		"META-INF/encryption.xml", `<encryption><EncryptedData><EncryptionMethod Algorithm="aes"/><CipherData><CipherReference URI="pkg/a.xhtml"/></CipherData></EncryptedData>
<EncryptedData><EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/><CipherData><CipherReference URI="pkg/f.otf"/></CipherData></EncryptedData></encryption>`)
	a, _ := Open(b, -1)
	_, budget := gofastcore.WithDecodeBudget(context.Background(), gofastcore.Limits{}, 1<<20)
	p, err := a.ReadPackage(budget)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "3.0" || p.UniqueID != "u" || len(p.DC) != 2 || p.DC[1].ID != "u" || p.Meta["dcterms:modified"] != "2020" || p.Meta["cover"] != "c" {
		t.Errorf("package %+v", p)
	}
	if it := p.ByID["a"]; it == nil || it.Path != "pkg/a.xhtml" || it.MediaType != "application/xhtml+xml" {
		t.Errorf("manifest %+v", p.ByID["a"])
	}
	if len(p.Spine) != 1 || p.Spine[0].Linear || p.NCX != "n" {
		t.Errorf("spine %+v %q", p.Spine, p.NCX)
	}
	enc, err := a.Encryption(budget)
	if err != nil || len(enc) != 1 || enc["pkg/a.xhtml"] != "aes" {
		t.Errorf("encryption %v %v", enc, err)
	}
}

// An encryption.xml that cannot be read in full is an error, never an
// empty list.
func TestEncryptionUnread(t *testing.T) {
	_, budget := gofastcore.WithDecodeBudget(context.Background(), gofastcore.Limits{}, 1<<30)
	entry := `<EncryptedData><EncryptionMethod Algorithm="aes"/><CipherData><CipherReference URI="a.xhtml"/></CipherData></EncryptedData>`
	for _, c := range []struct {
		name, xml string
		want      error
	}{
		{"none", "", nil},
		{"big", "<encryption>" + entry + "<!--" + strings.Repeat("p", MaxXML) + "--></encryption>", ErrTooLarge},
		{"cut short", "<encryption>" + entry + "<EncryptedData><Encryption", ErrMalformedXML},
		{"not xml", "<encryption>\x00" + entry + "</encryption>", ErrMalformedXML},
	} {
		if c.want == ErrTooLarge && testing.Short() {
			continue // a 16 MiB member
		}
		files := []string{"mimetype", "application/epub+zip"}
		if c.xml != "" {
			files = append(files, "META-INF/encryption.xml", c.xml)
		}
		a, _ := Open(zipOf(t, files...), -1)
		enc, err := a.Encryption(budget)
		if !errors.Is(err, c.want) || c.want == nil && err != nil || c.want != nil && enc != nil {
			t.Errorf("%s: %v %v", c.name, enc, err)
		}
	}
	// Past the limit on decoded bytes.
	a, _ := Open(zipOf(t, "META-INF/encryption.xml", "<encryption>"+entry+"</encryption>"), -1)
	_, small := gofastcore.WithDecodeBudget(context.Background(), gofastcore.Limits{MaxDecodedBytes: 10}, 0)
	if enc, err := a.Encryption(small); !errors.Is(err, ErrBudget) || enc != nil {
		t.Errorf("budget: %v %v", enc, err)
	}
}

// A container.xml or package document larger than MaxXML is an error
// that says so, not a missing package.
func TestPackageTooLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("16 MiB members")
	}
	_, budget := gofastcore.WithDecodeBudget(context.Background(), gofastcore.Limits{}, 1<<30)
	container := `<container><rootfiles><rootfile full-path="p.opf"/></rootfiles></container>`
	big := "<!--" + strings.Repeat("p", MaxXML) + "-->"
	for _, files := range [][]string{
		{"META-INF/container.xml", container + big, "p.opf", "<package/>"},
		{"META-INF/container.xml", container, "p.opf", "<package/>" + big},
	} {
		a, _ := Open(zipOf(t, files...), -1)
		if _, err := a.ReadPackage(budget); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %v", files[1][:20], err)
		}
	}
}

// The metadata of the package document: a value cut at MaxMeta wherever
// its text is split, and elements past MaxDC counted.
func TestPackageMetadataBounds(t *testing.T) {
	long := strings.Repeat("w", MaxMeta+100)
	for _, desc := range []string{long, long[:MaxMeta/2] + "<!--c-->" + long[MaxMeta/2:]} {
		p := parsePackage("p.opf", []byte(`<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:description>`+desc+`</dc:description></metadata></package>`))
		if len(p.DC) != 1 || len(p.DC[0].Value) != MaxMeta || !p.MetaCut {
			t.Errorf("description of %d bytes, cut %v", len(p.DC[0].Value), p.MetaCut)
		}
	}
	p := parsePackage("p.opf", []byte(`<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>`+long[:MaxMeta]+`</dc:title></metadata></package>`))
	if p.MetaCut || len(p.DC[0].Value) != MaxMeta {
		t.Errorf("a value of exactly MaxMeta bytes: cut %v", p.MetaCut)
	}
	var b strings.Builder
	b.WriteString(`<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">`)
	for range MaxDC + 7 {
		b.WriteString("<dc:creator>C</dc:creator>")
	}
	b.WriteString("</metadata></package>")
	p = parsePackage("p.opf", []byte(b.String()))
	if len(p.DC) != MaxDC || p.DCLeftOut != 7 || p.MetaCut {
		t.Errorf("%d elements, %d left out", len(p.DC), p.DCLeftOut)
	}
}

// The bounds of a table of contents are reported, and a title is cut at
// MaxTitle however its text is split.
func TestTOCBounds(t *testing.T) {
	ctx := context.Background()
	navDoc := func(ol string) []byte {
		return []byte(`<html xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc">` + ol + `</nav></body></html>`)
	}
	long := strings.Repeat("t", MaxTitle+50)
	for _, a := range []string{long, "<span>" + long[:100] + "</span><span>" + long[100:] + "</span>", long[:MaxTitle-1] + "\u00e9" + long} {
		nav, cut := Nav(ctx, "nav.xhtml", navDoc(`<ol><li><a href="a.xhtml">`+a+`</a></li></ol>`))
		if len(nav) != 1 || len(nav[0].Title) > MaxTitle || len(nav[0].Title) < MaxTitle-1 || cut.Titles != 1 || !utf8.ValidString(nav[0].Title) {
			t.Errorf("nav title of %d bytes, cut %+v", len(nav[0].Title), cut)
		}
	}
	for _, x := range []string{long, long[:100] + "<!--c-->" + long[100:]} {
		ncx, cut := NCX("toc.ncx", []byte(`<ncx><navMap><navPoint><navLabel><text>`+x+`</text></navLabel></navPoint></navMap></ncx>`))
		if len(ncx) != 1 || len(ncx[0].Title) != MaxTitle || cut.Titles != 1 {
			t.Errorf("ncx title of %d bytes, cut %+v", len(ncx[0].Title), cut)
		}
	}
	if nav, cut := Nav(ctx, "nav.xhtml", navDoc(`<ol><li><a href="a.xhtml">`+long[:MaxTitle]+`</a></li></ol>`)); cut != (Cut{}) || len(nav[0].Title) != MaxTitle {
		t.Errorf("a title of exactly MaxTitle bytes: %+v", cut)
	}

	deep := ""
	for i := MaxLevel + 3; i > 0; i-- {
		deep = `<ol><li><a href="a.xhtml">L</a>` + deep + `</li></ol>`
	}
	if nav, cut := Nav(ctx, "nav.xhtml", navDoc(deep)); len(nav) != MaxLevel || !cut.Deep || cut.Entries {
		t.Errorf("deep: %d entries, %+v", len(nav), cut)
	}

	if testing.Short() {
		return // 100,000 entries
	}
	var ol, ncx strings.Builder
	ol.WriteString("<ol>")
	ncx.WriteString("<ncx><navMap>")
	for range MaxEntries + 2 {
		ol.WriteString(`<li><a href="a.xhtml">E</a></li>`)
		ncx.WriteString(`<navPoint><navLabel><text>E</text></navLabel></navPoint>`)
	}
	ol.WriteString("</ol>")
	ncx.WriteString("</navMap></ncx>")
	if nav, cut := Nav(ctx, "nav.xhtml", navDoc(ol.String())); len(nav) != MaxEntries || !cut.Entries {
		t.Errorf("nav: %d entries, %+v", len(nav), cut)
	}
	if got, cut := NCX("toc.ncx", []byte(ncx.String())); len(got) != MaxEntries || !cut.Entries || got[MaxEntries-1].Title != "E" {
		t.Errorf("ncx: %d entries, %+v", len(got), cut)
	}
	// Exactly MaxEntries is not cut.
	if got, cut := NCX("toc.ncx", []byte(strings.Replace(ncx.String(), `<navPoint><navLabel><text>E</text></navLabel></navPoint>`, "", 2))); len(got) != MaxEntries || cut.Entries {
		t.Errorf("ncx exact: %d entries, %+v", len(got), cut)
	}
}
