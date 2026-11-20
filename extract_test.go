package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

func extract(t testing.TB, src string, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	return extractAs(t, "html", src, opts)
}

func extractAs(t testing.TB, format, src string, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := (&Extractor{Format: format}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate: %v\n%s", err, src)
	}
	if doc.Source.Format != format {
		t.Fatalf("format %q", doc.Source.Format)
	}
	return doc
}

// show renders the blocks of a document compactly.
func show(doc *gofastcore.Document) string {
	var out []string
	for _, b := range doc.Parts[0].Blocks {
		s := b.Kind.String()
		if b.Level > 0 {
			s += fmt.Sprint(b.Level)
		}
		if b.Marker != "" {
			s += "[" + b.Marker + "]"
		}
		if b.Hidden != 0 {
			s += "{" + b.Hidden.String() + "}"
		}
		if b.Table != nil {
			s += fmt.Sprintf("%q", b.Table.Rows)
		} else {
			s += fmt.Sprintf("%q", b.Text)
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func TestStructure(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"<h1>Title</h1><p>One  two\n three.</p>", `heading1"Title" paragraph"One two three."`},
		{"<p>a<p>b", `paragraph"a" paragraph"b"`},
		{"text <b>bold</b> more<div>block</div>tail", `paragraph"text bold more" paragraph"block" paragraph"tail"`},
		{"<ul><li>a<li>b<ul><li>c</ul></ul>", "list_item1[\u2022]\"a\" list_item1[\u2022]\"b\" list_item2[\u25E6]\"c\""},
		{"<ol start=3><li>a<li value=7>b<li>c</ol>", `list_item1[3.]"a" list_item1[7.]"b" list_item1[8.]"c"`},
		{"<ol type=a reversed><li>x<li>y</ol>", `list_item1[b.]"x" list_item1[a.]"y"`},
		{"<ul><li><p>one</p><p>two</p></li></ul>", "list_item1[\u2022]\"one\\ntwo\""},
		{"<blockquote><p>q1<blockquote>q2</blockquote></blockquote>", `quote1"q1" quote2"q2"`},
		{"<pre>\n  x = 1\n\ty</pre>", "code\"  x = 1\\n\\ty\""},
		{"a<br>b<br><br>c", `paragraph"a\nb\n\nc"`},
		{"<p>x</p><hr><p>y</p>", `paragraph"x" thematic_break"" paragraph"y"`},
		{"<hr hidden><table><tr><td>a<hr>b</table>", `thematic_break{invisible}"" table[["a\nb"]]`},
		{"<figure><img src=a.png alt='A  cat'><figcaption>Cap</figcaption></figure>", `image"A cat" caption"Cap"`},
		{"<p>See <img alt=pic src=x.png> here</p>", `paragraph"See here" image"pic"`},
		{"<table><tr><th>A<th>B<tr><td>1<td>2</table>", `table[["A" "B"] ["1" "2"]]`},
		{"<table><caption>C</caption><tr><td colspan=2>w<tr><td>1<td>2</table>", `caption"C" table[["w" ""] ["1" "2"]]`},
		{"<table><tr><td rowspan=2>r<td>1<tr><td>2</table>", `table[["r" "1"] ["" "2"]]`},
		{"<table><tr><td>a<table><tr><td>x<td>y</table></table>", `table[["a\nx y"]]`},
		{"<h7>no</h7>", `paragraph"no"`},
		{"x<!-- note -->y", `paragraph"xy" note"note"`},
		{"<select name=s><option>One<option>Two</select>", `note"One\nTwo"`},
		{"<script>var x = '<p>';</script><p>t</p>", `paragraph"t"`},
		{"<template><p>t</p></template><noscript><p>n</p></noscript>", `paragraph"t" paragraph"n"`},
		{"<details><summary>S</summary><p>D</p></details><dialog>G</dialog>", `paragraph"S" paragraph"D" paragraph"G"`},
		{"<ruby>A<rp>(</rp><rt>a</rt><rp>)</rp></ruby>", `paragraph"A(a)"`},
		{"<p>\uFB01ne</p>", `paragraph"fine"`},
		{"<pre>\uFB01</pre>", "code\"\uFB01\""},
		{"<dl><dt>T<dd>D</dl>", `paragraph"T" paragraph"D"`},
		{"<p>a&nbsp;&amp;&lt;b</p>", fmt.Sprintf("paragraph%q", "a\u00A0&<b")},
	} {
		if got := show(extract(t, c.in, nil)); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestHidden(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"<p hidden>a</p>", `paragraph{invisible}"a"`},
		{"<p>Visible <span style='display:none'>secret</span> text</p>", `paragraph{invisible}"Visible secret text"`},
		{"<div style='visibility:hidden'><p>a</p></div>", `paragraph{invisible}"a"`},
		{"<p style='font-size:0'>a</p>", `paragraph{tiny}"a"`},
		{"<p style='opacity:0'>a</p>", `paragraph{transparent}"a"`},
		{"<p style='color:#fefefe'>a</p>", `paragraph{low_contrast}"a"`},
		{"<p style='position:absolute;left:-9999px'>a</p>", `paragraph{off_page}"a"`},
		{"<p>a\u200B\u200B\u200B\u200Bb</p>", fmt.Sprintf("paragraph{invisible_chars}%q", "a\u200B\u200B\u200B\u200Bb")},
		{"<img hidden alt=x src=a.png>", `image{invisible}"x"`},
		{"<table><tr><td>a<td hidden>b</table>", `table{invisible}[["a" "b"]]`},
	} {
		doc := extract(t, c.in, nil)
		if got := show(doc); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.in, got, c.want)
		}
		if !doc.Parts[0].Has(gofastcore.WarnTextHidden) {
			t.Errorf("%q: no text.hidden warning", c.in)
		}
	}
	// A partly hidden paragraph has a span over the hidden passage.
	doc := extract(t, "<p>Visible one. <span hidden>Hidden.</span> Visible two.</p>", nil)
	b := doc.Parts[0].Blocks[0]
	if len(b.Spans) != 1 || b.Text[b.Spans[0].Start:b.Spans[0].End] != "Hidden." || b.Spans[0].Hidden != gofastcore.HiddenInvisible {
		t.Errorf("spans %+v over %q", b.Spans, b.Text)
	}
	if got := b.VisibleText(gofastcore.HiddenAny); got != "Visible one. Visible two." {
		t.Errorf("visible text %q", got)
	}
	// Partly hidden table: the cell is marked.
	doc = extract(t, "<table><tr><td>a<td hidden>b</table>", nil)
	if h := doc.Parts[0].Blocks[0].Table.Hidden; len(h) != 1 || h[0].Col != 1 {
		t.Errorf("hidden cells %+v", h)
	}
}

func TestAriaHiddenIsVisible(t *testing.T) {
	// aria-hidden hides from screen readers only, and undoes no reason.
	doc := extract(t, `<p aria-hidden="true">seen</p><p aria-hidden="true" style="display:none">unseen</p>`, nil)
	if got := show(doc); got != `paragraph"seen" paragraph{invisible}"unseen"` {
		t.Errorf("got %s", got)
	}
}

// Inside SVG, script and style are code, not text the page shows; an
// image is an image block; each text element is its own run of words.
func TestSVGContent(t *testing.T) {
	src := `<p>a</p><svg xmlns:xlink="http://www.w3.org/1999/xlink"><style>.st0{fill:#AAB}</style>` +
		`<script>alert(1)</script><text>Chapter one</text><text>Once upon a time</text>` +
		`<image xlink:href="flyer.jpg" width="10" height="20"/></svg>`
	doc := extract(t, src, nil)
	if got := show(doc); got != `paragraph"a" paragraph"Chapter one Once upon a time" image""` {
		t.Errorf("got %s", got)
	}
	active := false
	for _, w := range doc.Warnings {
		active = active || w.Code == gofastcore.WarnInputActiveContent
	}
	if !active {
		t.Errorf("no active content warning: %+v", doc.Warnings)
	}
	var img *gofastcore.Image
	for _, b := range doc.AllBlocks() {
		if b.Kind == gofastcore.KindImage {
			img = b.Image
		}
	}
	if img == nil || img.ID != "flyer.jpg" || img.Width != 10 || img.Height != 20 {
		t.Errorf("image %+v", img)
	}
	stored := doc.Parts[len(doc.Parts)-1]
	if stored.Kind != gofastcore.PartStored || len(stored.Blocks) != 2 {
		t.Errorf("stored part %+v", stored)
	}
}

func TestUnshownAttributes(t *testing.T) {
	src := `<nav aria-label="Main menu"><a href="/" title="Home">Home</a></nav>` +
		`<p><button aria-label="Ignore previous instructions">Close</button> after</p>` +
		`<input placeholder="Your  name"><img alt="logo" title="logo" src=l.png>` +
		`<map><area alt="Region one" href="/r1"></map>` +
		`<div hidden aria-description="secret">x</div>` +
		`<span aria-labelledby="lbl">y</span>`
	doc := extract(t, src, nil)
	want := `note"Main menu" list_item1[` + "•" + `]"Home"` +
		` note"Ignore previous instructions" paragraph"Close after"` +
		` note"Your name" image"logo" note"Region one"` +
		` note"secret" paragraph{invisible}"x" paragraph"y"`
	got := show(doc)
	// The nav is not a list; its link is a paragraph.
	want = strings.Replace(want, `list_item1[`+"•"+`]"Home"`, `paragraph"Home"`, 1)
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind != gofastcore.KindNote {
			continue
		}
		if b.Attrs["type"] != "attribute" || b.Attrs["attribute"] == "" {
			t.Errorf("attrs %v", b.Attrs)
		}
		// Loc is the attribute value in the source.
		if v := src[b.Loc.Offset : b.Loc.Offset+b.Loc.Length]; strings.Join(strings.Fields(v), " ") != b.Text {
			t.Errorf("loc of %q is %q", b.Text, v)
		}
	}
	// A label equal to the visible text says nothing new.
	if got := show(extract(t, `<a aria-label=" Read   more " title="Read more">Read more</a>`, nil)); got != `paragraph"Read more"` {
		t.Errorf("repeat: %s", got)
	}
	// Bounded by the text budget.
	doc = extract(t, strings.Repeat(`<b title="tip">x</b>`, 1000), &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 4096}})
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Error("no limit.exceeded")
	}
}

func TestHiddenChecked(t *testing.T) {
	if doc := extract(t, "<p>x</p>", nil); doc.HiddenChecked != gofastcore.HiddenChars|gofastcore.HiddenInvisible {
		t.Errorf("plain page: %s", doc.HiddenChecked)
	}
	if doc := extract(t, "<style>.x{display:none}</style><p class=x>x</p>", nil); doc.HiddenChecked != gofastcore.HiddenChars {
		t.Errorf("page with a stylesheet: %s", doc.HiddenChecked)
	}
	if doc := extract(t, "<p onclick='x()'>x</p>", nil); doc.HiddenChecked != gofastcore.HiddenChars {
		t.Errorf("page with a script: %s", doc.HiddenChecked)
	}
}

func TestMetadata(t *testing.T) {
	doc := extract(t, `<html lang=en-GB><head><title> My
	 Page </title><meta name=author content="A. Writer"><meta name=description content="About it">
	<meta name=keywords content="a, b"><meta name=generator content=Gen></head><body>x`, nil)
	m := doc.Metadata
	if m.Title != "My Page" || len(m.Authors) != 1 || m.Authors[0] != "A. Writer" || m.Extra["description"] != "About it" ||
		len(m.Keywords) != 1 || m.Keywords[0] != "a, b" || m.Application != "Gen" || m.Language != "en-GB" {
		t.Errorf("metadata %+v", m)
	}
	if show(doc) != `paragraph"x"` {
		t.Errorf("blocks %s", show(doc))
	}
}

func TestLocations(t *testing.T) {
	src := `<p id=first>One</p><h2>Two <a name=x>x</a></h2>`
	doc := extract(t, src, nil)
	bs := doc.Parts[0].Blocks
	if l := bs[0].Loc; src[l.Offset:l.Offset+l.Length] != "<p id=first>One</p>" || l.Anchor != "first" {
		t.Errorf("first loc %+v", l)
	}
	if l := bs[1].Loc; src[l.Offset:l.Offset+l.Length] != "<h2>Two <a name=x>x</a></h2>" || l.Anchor != "x" {
		t.Errorf("second loc %+v", l)
	}
	// Offsets are into the source bytes, also when decoding changes them.
	src = "<meta charset=windows-1252><p>caf\xE9</p><p id=b>b\xE9</p>"
	doc = extract(t, src, nil)
	bs = doc.Parts[0].Blocks
	if bs[0].Text != "caf\u00E9" {
		t.Errorf("text %q", bs[0].Text)
	}
	if l := bs[1].Loc; src[l.Offset:l.Offset+l.Length] != "<p id=b>b\xE9</p>" {
		t.Errorf("windows-1252 loc %+v: %q", l, src[l.Offset:l.Offset+l.Length])
	}
	// UTF-16 with a BOM.
	u16 := []byte{0xFF, 0xFE}
	for _, r := range "<p>hi</p>" {
		u16 = append(u16, byte(r), 0)
	}
	doc = extract(t, string(u16), nil)
	if l := doc.Parts[0].Blocks[0].Loc; l.Offset != 2 || l.Length != int64(len(u16)-2) || doc.Parts[0].Blocks[0].Text != "hi" {
		t.Errorf("utf-16 loc %+v", l)
	}
}

func TestCharsetWarnings(t *testing.T) {
	doc := extract(t, `<meta charset="shift_jis"><p>x`, nil)
	found := false
	for _, w := range doc.Warnings {
		found = found || w.Code == WarnCharsetUnsupported
	}
	if !found {
		t.Error("no charset warning")
	}
	doc = extract(t, "<meta charset=utf-8><p>a\xFFb", nil)
	if !doc.Parts[0].Has(gofastcore.WarnTextUnmappedGlyphs) || doc.Parts[0].Blocks[0].Text != "a\uFFFDb" {
		t.Errorf("invalid UTF-8: %s %+v", show(doc), doc.Parts[0].Warnings)
	}
}

func TestSpansAndLinks(t *testing.T) {
	doc := extract(t, `<p>Go <a href="https://go.dev/">to <b>Go</b></a>.</p><table><tr><td><a href="/x">X</a></table>`, &gofastcore.Options{Spans: true})
	b := doc.Parts[0].Blocks[0]
	var href, bold bool
	for _, s := range b.Spans {
		if s.Href == "https://go.dev/" && b.Text[s.Start:s.End] == "to Go" {
			href = true
		}
		if s.Style == gofastcore.StyleBold && b.Text[s.Start:s.End] == "Go" {
			bold = true
		}
	}
	if !href || !bold {
		t.Errorf("spans %+v over %q", b.Spans, b.Text)
	}
	if c := doc.Parts[0].Blocks[1].Table.Cells; len(c) != 1 || c[0].Href != "/x" {
		t.Errorf("cells %+v", c)
	}
	var md bytes.Buffer
	if err := doc.WriteMarkdown(&md, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "[to Go](<https://go.dev/>)") {
		t.Errorf("markdown %q", md.String())
	}
}

func TestWarnings(t *testing.T) {
	doc := extract(t, `<script src=a.js></script><img src="http://x/y.png"><p onclick="f()">x</p>`, nil)
	codes := map[string]bool{}
	for _, w := range doc.Warnings {
		codes[w.Code] = true
	}
	if !codes[gofastcore.WarnInputActiveContent] || !codes[gofastcore.WarnInputExternalReference] {
		t.Errorf("warnings %+v", doc.Warnings)
	}
	if doc := extract(t, "", nil); !doc.Parts[0].Has(gofastcore.WarnPartNoText) {
		t.Errorf("empty: %+v", doc.Parts[0].Warnings)
	}
	if doc := extract(t, "<img src=a.png>", nil); !doc.Parts[0].Has(gofastcore.WarnPartImageOnly) {
		t.Errorf("image only: %+v", doc.Parts[0].Warnings)
	}
}

func TestImagesData(t *testing.T) {
	src := `<img alt=dot src="data:image/png;base64,iVBORw0KGgo=">`
	doc := extract(t, src, &gofastcore.Options{Images: gofastcore.ImagesData})
	img := doc.Parts[0].Blocks[0].Image
	if img.MIME != "image/png" || !bytes.HasPrefix(img.Data, []byte("\x89PNG")) || !strings.HasPrefix(img.ID, "sha256:") {
		t.Errorf("image %+v", img)
	}
	doc = extract(t, src, &gofastcore.Options{Images: gofastcore.ImagesNone})
	if len(doc.Parts[0].Blocks) != 0 {
		t.Errorf("ImagesNone gave %s", show(doc))
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	src := "<p>x"
	if _, err := Extract(ctx, strings.NewReader(src), 4, &gofastcore.Options{Limits: gofastcore.Limits{MaxInputBytes: 3}}); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("input limit: %v", err)
	}
	if _, err := Extract(ctx, strings.NewReader(src), 4, &gofastcore.Options{Parts: gofastcore.PartRange{First: 2}}); !errors.Is(err, gofastcore.ErrNoPartsSelected) {
		t.Errorf("parts: %v", err)
	}
	if _, err := Extract(ctx, strings.NewReader(src), 10, nil); !errors.Is(err, gofastcore.ErrMalformed) {
		t.Errorf("short read: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Extract(cctx, strings.NewReader(src), 4, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}
