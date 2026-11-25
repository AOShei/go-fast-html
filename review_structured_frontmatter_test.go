package gofasthtml

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/blocks"
)

// TestReviewFrontMatterListOver64K: each item of a list is cut at
// MaxMetaValue, but the items are then joined (", ") into one Extra value
// with no bound: two 40 KB items give an 80 KB value, past the 64 KiB a
// front matter value may be in the metadata, and no limit.exceeded.
func TestReviewFrontMatterListOver64K(t *testing.T) {
	src := "---\nother: [" + strings.Repeat("y", 40000) + ", " + strings.Repeat("z", 40000) + "]\n---\nBody\n"
	doc := reviewExtract(t, "l.md", src, nil)
	if v := doc.Metadata.Extra["front_matter.other"]; len(v) > MaxMetaValue {
		t.Errorf("front_matter.other is %d bytes, over MaxMetaValue (%d)", len(v), MaxMetaValue)
	}
}

// TestReviewFrontMatterKeywordsUnbounded: the items of tags/keywords and
// authors lists are appended to Metadata.Keywords/Authors with no bound
// (maxFrontMatterKeys counts values, and a list is one value), while an
// HTML page's are bounded at blocks.MaxMetaList with limit.exceeded.
func TestReviewFrontMatterKeywordsUnbounded(t *testing.T) {
	src := "---\ntags: [" + strings.Repeat("t, ", 5000) + "t]\n---\nBody\n"
	doc := reviewExtract(t, "k.md", src, nil)
	if n := len(doc.Metadata.Keywords); n > blocks.MaxMetaList {
		t.Errorf("%d keywords, over the bound of %d", n, blocks.MaxMetaList)
	}
	if len(csvWarnings(doc, gofastcore.WarnLimitExceeded)) == 0 {
		t.Errorf("no limit.exceeded for the keywords left out")
	}
}

// TestReviewFrontMatterTOMLArrayOfTables: after a [[table]] header,
// tomlFrontMatter sets table = "" and goes on reading key = value lines,
// so the keys of an array of tables are taken as top-level keys: the
// title of a Hugo resource becomes the page's title, its src a
// front_matter.src value. The comment says they are not read.
func TestReviewFrontMatterTOMLArrayOfTables(t *testing.T) {
	src := "+++\n[[resources]]\nsrc = \"cover.jpg\"\ntitle = \"The cover image\"\ndate = 2001-02-03\n+++\nBody\n"
	doc := reviewExtract(t, "h.md", src, nil)
	md := doc.Metadata
	if md.Title != "" || md.Extra["front_matter.src"] != "" || !md.Created.IsZero() {
		t.Errorf("keys of [[resources]] read as the page's: title %q, src %q, created %v",
			md.Title, md.Extra["front_matter.src"], md.Created)
	}
}

// TestReviewFrontMatterYAMLNull (SUSPECTED): a YAML null (~, null) is
// taken as its spelling: "title: ~" makes the title "~" and "author:
// null" an author "null". yamlFrontMatter skips only an empty plain
// scalar.
func TestReviewFrontMatterYAMLNull(t *testing.T) {
	src := "---\ntitle: ~\nauthor: null\n---\n# Hi\n"
	doc := reviewExtract(t, "n.md", src, nil)
	if doc.Metadata.Title != "" || len(doc.Metadata.Authors) != 0 {
		t.Errorf("title %q, authors %q from YAML nulls", doc.Metadata.Title, doc.Metadata.Authors)
	}
}
