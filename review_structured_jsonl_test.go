package gofasthtml

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// TestReviewJSONLinesBadLineSwallowsNext: in a .jsonl file a line that
// is not JSON ends only where the scanner finds an error, so an unclosed
// line takes the valid lines after it into its raw block: they get no
// path of their own and are not records. Here line 1 is cut off; lines 2
// and 3 are records [1] and [2], but come out inside one raw block [0].
// jsondata.go planJSON: the region runs from the value's start to the
// line of the error, not to the end of the value's first line.
func TestReviewJSONLinesBadLineSwallowsNext(t *testing.T) {
	src := "{\"a\": [\n{\"b\": 2}\n{\"b\": 3}\n"
	doc := reviewExtract(t, "l.jsonl", src, nil)
	var found bool
	for _, b := range doc.Parts[0].Blocks {
		if b.Table != nil {
			for _, l := range b.Table.RowLocs {
				if l.Anchor == "[1]" && src[l.Offset:l.Offset+l.Length] == `{"b": 2}` {
					found = true
				}
			}
		}
		if b.Kind == gofastcore.KindCode && b.Loc.Anchor == "[1]" && strings.HasPrefix(b.Text, `{"b": 2}`) {
			found = true
		}
	}
	if !found {
		for _, b := range doc.Parts[0].Blocks {
			t.Logf("%s %q %q %v", b.Kind, b.Loc.Anchor, b.Text, b.Attrs)
		}
		t.Errorf("line 2 is not the value [1]")
	}
}
