package gofasthtml

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// A character reference that names no character (&#0;, a surrogate, a
// number past U+10FFFF) is read as U+FFFD and counted in one
// text.unmapped_glyphs warning, in every format that reads references.
// A reference to U+FFFD itself is a character and is not counted.
func TestCharRefsReplacedAreWarned(t *testing.T) {
	cases := []struct {
		name, format, src string
		n                 int // references read as U+FFFD; 0: no warning
	}{
		{"html text and attribute", "html", `<p title="&#0;">a&#0;b&#xD800;c&#x110000;d&#xFFFD;e</p>`, 4},
		{"html, none", "html", `<p>a&#xFFFD;b&#65;c&amp;</p>`, 0},
		{"html, the reference in a srcdoc document", "html", `<iframe srcdoc="&lt;p&gt;x&amp;#0;y"></iframe>`, 1},
		{"html, srcdoc and page", "html", `<p>&#0;</p><iframe srcdoc="&lt;p&gt;&amp;#0;&#0;"></iframe>`, 3},
		{"markdown text, link, title, definition, fence info, HTML", "md",
			"a &#0; b [l](/x&#0; \"t&#0;\") [r]\n\n[r]: /y&#0;\n\n```&#0;\ncode\n```\n\n<span title=\"&#0;\">s</span>\n", 6},
		{"markdown, a link that is not one is read as text once", "md", "[x](/a&#0; z\n", 1},
		{"markdown, a definition that is not stored", "md", "[r]: /a\n[r]: /b&#0;\n\n[r]\n", 0},
		{"webvtt text and voice", "vtt", "WEBVTT\n\n00:00.000 --> 00:01.000\n<v A&#0;>x&#0;y</v>\n", 2},
		{"notebook markdown cell", "ipynb", nb(mdCell("a", "x &#0; y")), 1},
	}
	for _, c := range cases {
		doc := extractAs(t, c.format, c.src, nil)
		var got []string
		for _, w := range warnings(doc, gofastcore.WarnTextUnmappedGlyphs) {
			if strings.Contains(w, "character reference") {
				got = append(got, w)
			}
		}
		switch {
		case c.n == 0 && len(got) != 0:
			t.Errorf("%s: %q", c.name, got)
		case c.n > 0 && len(got) != 1:
			t.Errorf("%s: %q, want 1 warning for %d", c.name, got, c.n)
		case c.n > 0 && !strings.HasSuffix(got[0], ": "+replacedMessage(c.n)):
			t.Errorf("%s: %q, want %q", c.name, got[0], replacedMessage(c.n))
		}
	}
	if m := replacedMessage(1); m != "1 character reference names no character (&#0;, a surrogate or a number past U+10FFFF) and is read as U+FFFD" {
		t.Errorf("one: %q", m)
	}
	if m := replacedMessage(2); m != "2 character references name no character (&#0;, a surrogate or a number past U+10FFFF) and are read as U+FFFD" {
		t.Errorf("two: %q", m)
	}
}

// The warning for bytes not valid in the encoding says they are read as
// U+FFFD (not "appear": some are in no text given, a script's), in the
// singular for one.
func TestInvalidBytesMessage(t *testing.T) {
	doc := extract(t, "<meta charset=\"utf-8\"><p>caf\xff au lait \xff</p>", nil)
	if got := warnings(doc, gofastcore.WarnTextUnmappedGlyphs); len(got) != 1 || !strings.HasSuffix(got[0], " byte sequences are not valid utf-8 and are read as U+FFFD") {
		t.Errorf("%q", got)
	}
	if m := invalidMessage(1, "UTF-8"); m != "1 byte sequence is not valid UTF-8 and is read as U+FFFD" {
		t.Errorf("one: %q", m)
	}
}
