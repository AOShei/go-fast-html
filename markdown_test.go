package gofasthtml

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

func TestMarkdown(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"# Title\n\nSome *emphasis* and **strong** text.", `heading1"Title" paragraph"Some emphasis and strong text."`},
		{"Sub\n---\nline one\nline two", `heading2"Sub" paragraph"line one line two"`},
		{"- a\n- b\n  1. c\n  2. d", "list_item1[\u2022]\"a\" list_item1[\u2022]\"b\" list_item2[1.]\"c\" list_item2[2.]\"d\""},
		{"> quoted\n> > deeper", `quote1"quoted" quote2"deeper"`},
		{"```python\n  x = 1\n```", "code\"  x = 1\""},
		{"    indented\n    code", "code\"indented\\ncode\""},
		{"a\n\n---\n\nb", `paragraph"a" paragraph"b"`},
		{"[link](http://x) and ![alt text](p.png)", `paragraph"link and" image"alt text"`},
		{"`co*de*` \\*lit\\*", "paragraph\"co*de* *lit*\""},
		{"a  \nb", `paragraph"a\nb"`},
		{"| h1 | h2 |\n|----|----|\n| a  | b  |", `table[["h1" "h2"] ["a" "b"]]`},
		{"text <span style=\"display:none\">hidden</span> more", `paragraph{invisible}"text hidden more"`},
		{"<!-- secret note -->\n\npara", `note{invisible}"secret note" paragraph"para"`},
		{"<div hidden>\n\nhidden *para*\n\n</div>", `paragraph{invisible}"hidden para"`},
		{"\uFB01ne", `paragraph"fine"`},
	} {
		if got := show(extractAs(t, "md", c.in, nil)); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestMarkdownLocations(t *testing.T) {
	src := "# Head\n\n> para one\n> line two\n\n- item\n\n<p id=raw>raw html</p>\n"
	doc := extractAs(t, "md", src, nil)
	want := []string{"# Head", "> para one\n> line two", "- item", "<p id=raw>raw html</p>"}
	bs := doc.Parts[0].Blocks
	if len(bs) != len(want) {
		t.Fatalf("blocks %s", show(doc))
	}
	for i, b := range bs {
		got := src[b.Loc.Offset : b.Loc.Offset+b.Loc.Length]
		// A quote's paragraph starts after the "> " that opens it.
		if got != want[i] && !strings.HasSuffix(want[i], got) {
			t.Errorf("block %d (%s) located at %q, want %q", i, b.Kind, got, want[i])
		}
	}
	if bs[3].Loc.Anchor != "raw" {
		t.Errorf("anchor %q", bs[3].Loc.Anchor)
	}
}

func TestMarkdownLinks(t *testing.T) {
	doc := extractAs(t, "md", "See [the docs][d].\n\n[d]: https://example.com/docs \"Docs\"", &gofastcore.Options{Spans: true})
	b := doc.Parts[0].Blocks[0]
	if b.Text != "See the docs." || len(b.Spans) != 1 || b.Spans[0].Href != "https://example.com/docs" {
		t.Errorf("block %+v", b)
	}
	if doc.HiddenChecked != gofastcore.HiddenChars|gofastcore.HiddenInvisible {
		t.Errorf("hidden checked %s", doc.HiddenChecked)
	}
}

func TestPlainText(t *testing.T) {
	src := "First line\r\nsecond line   \n\n\n  Para \uFB01 two\twith tab\x07\n\fPage two\n"
	doc := extractAs(t, "txt", src, nil)
	if got, want := show(doc), `paragraph"First line\nsecond line" paragraph"Para fi two\twith tab" paragraph"Page two"`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	b := doc.Parts[0].Blocks[1]
	if got := src[b.Loc.Offset : b.Loc.Offset+b.Loc.Length]; got != "  Para \uFB01 two\twith tab\x07" {
		t.Errorf("loc %q", got)
	}
	if doc.HiddenChecked != gofastcore.HiddenChars {
		t.Errorf("hidden checked %s", doc.HiddenChecked)
	}
	// windows-1252 text, located in the source bytes.
	src = "caf\xE9\n\nna\xEFve"
	doc = extractAs(t, "txt", src, nil)
	if b := doc.Parts[0].Blocks[1]; b.Text != "na\u00EFve" || src[b.Loc.Offset:b.Loc.Offset+b.Loc.Length] != "na\xEFve" {
		t.Errorf("block %+v", b)
	}
	if doc := extractAs(t, "txt", " \n\n", nil); !doc.Parts[0].Has(gofastcore.WarnPartNoText) {
		t.Error("no part.no_text")
	}
	doc = extractAs(t, "txt", strings.Repeat("para\n\n", 100), &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 1000}})
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Error("no limit.exceeded")
	}
}

func TestDetect(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"a.HTML", "plain", "html"},
		{"a.md", "<p>x</p>", "md"},
		{"notes.txt", "<p>x</p>", "txt"},
		{"", "<!DOCTYPE html><p>", "html"},
		{"", "\xEF\xBB\xBF  <html>", "html"},
		{"", "Some text with a <b>bold</b> word", "html"},
		{"", "# Heading\n\ntext", "txt"},
		{"", "a < b and c > d", "txt"},
		{"", "\xFF\xFE<\x00p\x00>\x00", "html"},
	} {
		if got := Detect(c.name, []byte(c.src)); got != c.want {
			t.Errorf("Detect(%q, %q) = %s, want %s", c.name, c.src, got, c.want)
		}
	}
}
