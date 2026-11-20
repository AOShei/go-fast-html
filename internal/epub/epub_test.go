package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"testing"

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
	ncx := NCX("OPS/toc.ncx", []byte(`<ncx><navMap><navPoint><navLabel><text>A</text></navLabel><content src="a.html"/>
<navPoint><navLabel><text>B</text></navLabel><content src="sub/b.html#f"/></navPoint></navPoint>
<navPoint><navLabel><text>C</text></navLabel></navPoint></navMap></ncx>`))
	want := []Entry{{"A", 1, "OPS/a.html"}, {"B", 2, "OPS/sub/b.html"}, {"C", 1, ""}}
	if len(ncx) != 3 || ncx[0] != want[0] || ncx[1] != want[1] || ncx[2] != want[2] {
		t.Errorf("NCX %+v", ncx)
	}
	nav := Nav(context.Background(), "OPS/nav.xhtml", []byte(`<html><body><nav epub:type="landmarks"><ol><li><a href="x.xhtml">L</a></li></ol></nav>
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
	enc := a.Encryption(budget)
	if len(enc) != 1 || enc["pkg/a.xhtml"] != "aes" {
		t.Errorf("encryption %v", enc)
	}
}
