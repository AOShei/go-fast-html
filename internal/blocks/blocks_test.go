package blocks

import (
	"context"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/dom"
)

func walk(t *testing.T, src string, cfg Config) *Result {
	t.Helper()
	d, err := dom.Parse(context.Background(), []byte(src), dom.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source == nil {
		cfg.Source = func(i int) int64 { return int64(i) }
	}
	r, err := Walk(context.Background(), d.Root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBufCollapse(t *testing.T) {
	b := newBuf()
	b.add([]byte("  a \n\t b  "), dom.Normal, 0, false)
	b.add([]byte(" c"), dom.Normal, gofastcore.HiddenTiny, false)
	b.lineBreak()
	b.add([]byte("  d\r\ne\x01"), dom.Pre, 0, false)
	b.add([]byte("x\ny  z"), dom.PreLine, 0, false)
	text, h, spans := b.finish()
	if text != "a b c\n  d\nex\ny z" {
		t.Errorf("text %q", text)
	}
	if h != gofastcore.HiddenTiny || len(spans) != 1 || text[spans[0].Start:spans[0].End] != "c" {
		t.Errorf("hidden %s spans %+v", h, spans)
	}
}

func TestBufWholeHidden(t *testing.T) {
	b := newBuf()
	b.add([]byte("a "), dom.Normal, gofastcore.HiddenInvisible, false)
	b.add([]byte(" "), dom.Normal, 0, false)
	b.add([]byte("b"), dom.Normal, gofastcore.HiddenInvisible, false)
	text, h, spans := b.finish()
	if text != "a b" || h != gofastcore.HiddenInvisible || len(spans) != 0 {
		t.Errorf("%q %s %+v", text, h, spans)
	}
}

func TestVerbatim(t *testing.T) {
	b := newBuf()
	b.add([]byte(" \xEF\xAC\x81\t\x00x \n"), dom.Pre, 0, true)
	if got, _, _ := b.verbatimText(); got != " \xEF\xAC\x81\tx \n" {
		t.Errorf("%q", got)
	}
}

func TestMarkers(t *testing.T) {
	for _, c := range []struct {
		lc   listCtx
		want string
	}{
		{listCtx{ordered: true, n: 4}, "4."},
		{listCtx{ordered: true, n: 28, typ: "a"}, "ab."},
		{listCtx{ordered: true, n: 1994, typ: "I"}, "MCMXCIV."},
		{listCtx{ordered: true, n: 4, typ: "i"}, "iv."},
		{listCtx{ordered: true, n: 0, typ: "A"}, "0."},
		{listCtx{typ: "square"}, "▪"},
		{listCtx{typ: "none"}, ""},
	} {
		if got := marker(&c.lc, 1); got != c.want {
			t.Errorf("%+v: %q, want %q", c.lc, got, c.want)
		}
	}
	if marker(&listCtx{}, 2) != "◦" || marker(&listCtx{}, 3) != "▪" || marker(&listCtx{}, 4) != "•" {
		t.Error("bullets by depth")
	}
}

func TestTable(t *testing.T) {
	r := walk(t, `<table id=t><thead><tr><th colspan=2>H</thead>
		<tr><td rowspan=3>a<td><a href="/x">b</a><tr><td style="display:none">c</table>`, Config{Spans: true})
	if len(r.Blocks) != 1 {
		t.Fatalf("%d blocks", len(r.Blocks))
	}
	b := r.Blocks[0]
	tb := b.Table
	if got := len(tb.Rows); got != 3 || tb.HeaderRows != 1 || b.Loc.Anchor != "t" {
		t.Fatalf("table %+v", tb)
	}
	if tb.Rows[0][0] != "H" || tb.Rows[1][0] != "a" || tb.Rows[1][1] != "b" || tb.Rows[2][1] != "c" {
		t.Errorf("rows %q", tb.Rows)
	}
	want := []gofastcore.Merge{{Row: 0, Col: 0, RowSpan: 1, ColSpan: 2}, {Row: 1, Col: 0, RowSpan: 2, ColSpan: 1}}
	if len(tb.Merges) != 2 || tb.Merges[0] != want[0] || tb.Merges[1] != want[1] {
		t.Errorf("merges %+v", tb.Merges)
	}
	if len(tb.Cells) != 1 || tb.Cells[0].Href != "/x" {
		t.Errorf("cells %+v", tb.Cells)
	}
	if len(tb.Hidden) != 1 || tb.Hidden[0].Row != 2 || b.Hidden != gofastcore.HiddenInvisible {
		t.Errorf("hidden %+v %s", tb.Hidden, b.Hidden)
	}
}

func TestOrder(t *testing.T) {
	// Notes and images follow the block they stand in.
	r := walk(t, `<p>a<!--c--><img alt=i src=x.png>b</p><p>d</p>`, Config{})
	var kinds []string
	for _, b := range r.Blocks {
		kinds = append(kinds, b.Kind.String()+":"+b.Text)
	}
	if got := strings.Join(kinds, " "); got != "paragraph:ab note:c image:i paragraph:d" {
		t.Errorf("order %s", got)
	}
	if r.External != 1 {
		t.Errorf("external %d", r.External)
	}
}

func TestBudget(t *testing.T) {
	_, budget := gofastcore.WithTextBudget(context.Background(), gofastcore.Limits{MaxTextBytes: 2 * (gofastcore.BlockCost + 1)})
	r := walk(t, "<p>a<p>b<p>c<p>d", Config{Budget: budget})
	if !r.Cut || len(r.Blocks) != 2 {
		t.Errorf("cut %v, %d blocks", r.Cut, len(r.Blocks))
	}
}

func TestActiveAndMeta(t *testing.T) {
	r := walk(t, `<html lang=fr><head><title>T</title><meta name=keywords content="k">
		<meta http-equiv=refresh content="0; url=http://x"><link rel=stylesheet href=s.css></head>
		<body onload="x()"><a href="javascript:void(0)">j</a><a href=" java&#9;script:x">k</a>
		<script type="application/ld+json">{"a":1}</script></body>`, Config{})
	if r.Active != 3 {
		t.Errorf("active %d", r.Active)
	}
	if r.External != 2 || r.Stylesheets != 1 {
		t.Errorf("external %d stylesheets %d", r.External, r.Stylesheets)
	}
	if r.Meta.Title != "T" || r.Meta.Language != "fr" || len(r.Meta.Keywords) != 1 {
		t.Errorf("meta %+v", r.Meta)
	}
	last := r.Blocks[len(r.Blocks)-1]
	if last.Kind != gofastcore.KindCode || last.Attrs[gofastcore.AttrLanguage] != "json" || last.Hidden == 0 {
		t.Errorf("json script %+v", last)
	}
	long := strings.Repeat("x", MaxMeta+10)
	if r := walk(t, "<title>"+long+"</title>", Config{}); !r.MetaCut || len(r.Meta.Title) != MaxMeta {
		t.Errorf("meta cut %v %d", r.MetaCut, len(r.Meta.Title))
	}
}

func TestDataURI(t *testing.T) {
	_, dec := gofastcore.WithDecodeBudget(context.Background(), gofastcore.Limits{MaxDecodedBytes: 15}, 0)
	r := walk(t, `<img src="data:text/plain,hello%20there"><img src="data:image/png;base64,AAAA">`,
		Config{Images: gofastcore.ImagesData, Decode: dec})
	if img := r.Blocks[0].Image; string(img.Data) != "hello there" || img.MIME != "text/plain" {
		t.Errorf("first %+v", img)
	}
	// The second is past the decode budget.
	if img := r.Blocks[1].Image; img.Data != nil || r.ImageData != 1 {
		t.Errorf("second %+v, %d", img, r.ImageData)
	}
}

func TestHelpers(t *testing.T) {
	if !isScriptURL(" JavaScript:x") || !isScriptURL("java\nscript:x") || isScriptURL("http://x") {
		t.Error("isScriptURL")
	}
	if cleanRef(" a\x00b\n") != "ab" {
		t.Error("cleanRef")
	}
	if cleanProse(" a\n  b\xEF\xAC\x81 ", true) != "a bfi" {
		t.Error("cleanProse")
	}
	d, _ := dom.Parse(context.Background(), []byte(`<pre class="x lang-Go"><code class="language-rust">`), dom.Options{})
	pre := d.Root.FirstChild
	if got := codeLanguage(pre); got != "go" {
		t.Errorf("codeLanguage %q", got)
	}
	if v, ok := parseInt(" 12abc"); !ok || v != 12 {
		t.Error("parseInt")
	}
}
