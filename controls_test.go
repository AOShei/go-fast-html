package gofasthtml

import (
	"strings"
	"testing"

	gofastcore "github.com/AOShei/go-fast-core"
)

// controlsWarned returns the message of text.control_characters_removed on
// the first part and on the document.
func controlsWarned(doc *gofastcore.Document) (part, document string) {
	for _, w := range doc.Parts[0].Warnings {
		if w.Code == gofastcore.WarnTextControlCharactersRemoved {
			part = w.Message
		}
	}
	for _, w := range doc.Warnings {
		if w.Code == gofastcore.WarnTextControlCharactersRemoved {
			document = w.Message
		}
	}
	return part, document
}

// TestControlsCounted: control characters with no meaning as text are
// counted where they are removed, in every format; tab, line ends and
// form feed are not, nor what is removed from addresses and names.
func TestControlsCounted(t *testing.T) {
	for _, c := range []struct {
		name, src     string
		part, docWant string
	}{
		// VT in the title, NUL and ESC in the text, SOH in a title
		// attribute's note; the id, href, CR and form feed are not counted.
		{"a.html", "<title>T\x0bitle</title><p id='x\x01y'>a\x00b\x1bc\fd\r\ne</p><p title='n\x01ote'><a href='/p\x02q'>link</a></p>",
			"4 control characters with no meaning as text were removed from the text", ""},
		// CommonMark replaces NUL with U+FFFD: only ESC is removed.
		{"a.md", "# He\x00ad\r\n\r\npara\x1bgraph\f\n", "1 control character with no meaning as text was removed from the text", ""},
		{"a.txt", "a\x00b\tc\r\n\x0bd\fe\u0085f", "3 control characters with no meaning as text were removed from the text", ""},
		{"a.csv", "a,b\x00\r\n\"c\x01\r\nx\",d\n", "2 control characters with no meaning as text were removed from the fields", ""},
		{"a.json", "{\"a\":\r\n \"\x00\x1b\"}\f\n", "3 control characters with no meaning as text were removed from the text", ""},
		{"a.ipynb", `{"nbformat":4,"nbformat_minor":5,"metadata":{"title":"T\u0001"},"cells":[{"cell_type":"markdown","id":"a","metadata":{},"source":"x\u001by"}]}`,
			"1 control character with no meaning as text was removed from the text", "1 control character with no meaning as text was removed from the document's metadata and outline"},
	} {
		doc, err := extractEnc(t, []byte(c.src), c.name, "")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		part, document := controlsWarned(doc)
		if part != c.part || document != c.docWant {
			t.Errorf("%s:\n part %q\nwant %q\n doc %q\nwant %q", c.name, part, c.part, document, c.docWant)
		}
		if strings.ContainsAny(docText(doc), "\x00\x01\x1b\x0b") {
			t.Errorf("%s: a control character is in the text", c.name)
		}
	}
	// None: white space, line ends and tabs only.
	for _, src := range []string{"a\r\nb\tc\fd", "<p>a\r\nb\tc\fd</p>"} {
		doc, err := extractEnc(t, []byte(src), map[bool]string{true: "a.html", false: "a.txt"}[src[0] == '<'], "")
		if err != nil {
			t.Fatal(err)
		}
		if p, d := controlsWarned(doc); p != "" || d != "" {
			t.Errorf("%q: %q %q", src, p, d)
		}
	}
}

// TestControlsNotReadNotCounted: text the budget leaves out is not counted.
func TestControlsNotReadNotCounted(t *testing.T) {
	src := "a\x00\n\n" + strings.Repeat("x", 4000) + "\x00\x00"
	doc, err := (&Extractor{}).Extract(t.Context(), strings.NewReader(src), int64(len(src)),
		&gofastcore.Options{Name: "a.txt", Limits: gofastcore.Limits{MaxTextBytes: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := controlsWarned(doc); p != "1 control character with no meaning as text was removed from the text" {
		t.Errorf("%q", p)
	}
}
