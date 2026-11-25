package gofasthtml

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// TestReviewJSONDepthBoundNotSyntaxError: valid JSON nested deeper than
// jsonscan's bound (512) is reported as Extra["json.syntax_error"]
// ("nesting deeper than the bound") and read as lines, with no
// limit.exceeded: a fixed bound of the library is a limit, and the file
// is not in error.
func TestReviewJSONDepthBoundNotSyntaxError(t *testing.T) {
	src := strings.Repeat("[", 600) + strings.Repeat("]", 600)
	doc := reviewExtract(t, "d.json", src, nil)
	if len(csvWarnings(doc, gofastcore.WarnLimitExceeded)) == 0 {
		t.Errorf("no limit.exceeded; json.syntax_error = %q", doc.Metadata.Extra[ExtraJSONSyntaxError])
	}
}

// TestReviewDecodeCutNotSyntaxError: when the decode budget cuts a valid
// JSON file, the structure reader sees an unclosed value and the file is
// reported as not JSON (Extra["json.syntax_error"]) and read as lines.
// data.go reads the cut text with jsonStructure without regard to cut.
func TestReviewDecodeCutNotSyntaxError(t *testing.T) {
	var b strings.Builder
	b.WriteString("{\n")
	for i := range 200 {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString(`  "key` + strings.Repeat("k", i%7) + `": "value"`)
	}
	b.WriteString("\n}\n")
	src := b.String()
	doc := reviewExtract(t, "c.json", src, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: int64(len(src) / 2)}})
	if e := doc.Metadata.Extra[ExtraJSONSyntaxError]; e != "" {
		t.Errorf("a file cut by the decode budget is said not to be JSON: %q", e)
	}
}
