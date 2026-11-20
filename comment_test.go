package gofasthtml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// checkMarkupComment finds the block holding text and checks it is a
// note marked stored=markup_comment with no note type, outside the
// stored part, left out of default text and shown by
// StoredKinds=[markup_comment] and by Stored.
func checkMarkupComment(t *testing.T, doc *gofastcore.Document, text string) {
	t.Helper()
	found := false
	for _, p := range doc.Parts {
		for _, b := range p.Blocks {
			if !strings.Contains(b.Text, text) {
				continue
			}
			found = true
			if p.Kind == gofastcore.PartStored || b.Kind != gofastcore.KindNote ||
				b.Attrs[gofastcore.AttrStored] != gofastcore.StoredMarkupComment || b.Attrs[gofastcore.AttrNoteType] != "" {
				t.Errorf("comment in %s part: %+v", p.Kind, b)
			}
		}
	}
	if !found {
		t.Fatalf("no block holds %q", text)
	}
	if strings.Contains(doc.Text(), text) {
		t.Errorf("comment in default text")
	}
	if !strings.Contains(textWith(doc, &gofastcore.RenderOptions{StoredKinds: []string{gofastcore.StoredMarkupComment}}), text) {
		t.Errorf("comment not shown by StoredKinds")
	}
	if strings.Contains(textWith(doc, &gofastcore.RenderOptions{StoredKinds: []string{gofastcore.StoredMacro}}), text) {
		t.Errorf("comment shown by StoredKinds=macro")
	}
	if !strings.Contains(textWith(doc, &gofastcore.RenderOptions{Stored: true}), text) {
		t.Errorf("comment not shown by Stored")
	}
}

func textWith(doc *gofastcore.Document, opts *gofastcore.RenderOptions) string {
	var buf bytes.Buffer
	doc.WriteText(&buf, opts)
	return buf.String()
}

func TestMarkupCommentStored(t *testing.T) {
	checkMarkupComment(t, extract(t, `<p>Hi</p><!--[if mso]><style>td{x:y}</style><![endif]--><p>Bye</p>`, nil), "[if mso]")
	checkMarkupComment(t, extractAs(t, "md", "Hi\n\n<!-- a raw comment -->\n\nBye\n", nil), "a raw comment")
	b := buildEPUB(t,
		"META-INF/container.xml", container,
		"OEBPS/content.opf", opf3,
		"OEBPS/nav.xhtml", nav,
		"OEBPS/text/ch1.xhtml", `<html><body><h1>One</h1><!-- chapter comment --><p>Text.</p></body></html>`,
		"OEBPS/text/ch 2.xhtml", `<html><body><p>Two.</p></body></html>`,
		"OEBPS/text/notes.xhtml", `<html><body><p>A note.</p></body></html>`,
	)
	checkMarkupComment(t, extractEPUB(t, b, nil), "chapter comment")
}
