package gofasthtml

import "testing"

// The ligature U+FB01, built so that the source holds no literal one.
var reviewFI = string(rune(0xFB01))

// TestReviewStructTableLigature: the cells and header of a table made of
// JSON records are written without expanding ligatures, so the document
// fails Validate (table cells are prose). structure.go table(): header
// via s.clean, cells via s.cleanCell; neither expands U+FB00-U+FB06.
func TestReviewStructTableLigature(t *testing.T) {
	src := `[{"pro` + reviewFI + `le": "` + reviewFI + `ne"}, {"pro` + reviewFI + `le": "b"}]`
	doc := reviewExtract(t, "r.json", src, nil)
	if err := doc.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// TestReviewFrontMatterTitleLigature: front matter values go to Title,
// Authors and Keywords through cleanMeta, which does not expand
// ligatures, so the document fails Validate.
func TestReviewFrontMatterTitleLigature(t *testing.T) {
	src := "---\ntitle: \"" + reviewFI + "le\"\nauthor: Gri" + reviewFI + "n\ntags: [" + reviewFI + "x]\n---\n# Hi\n"
	doc := reviewExtract(t, "p.md", src, nil)
	if err := doc.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if doc.Metadata.Title != "file" {
		t.Errorf("title %q, want %q", doc.Metadata.Title, "file")
	}
}
