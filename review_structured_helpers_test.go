package gofasthtml

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// reviewExtract extracts src as a file named name without failing on
// Validate, so that a test can report what Validate says.
func reviewExtract(t *testing.T, name, src string, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	if opts == nil {
		opts = &gofastcore.Options{}
	}
	opts.Name = name
	doc, err := (&Extractor{}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), opts)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// reviewAllText returns the text of every block and every table cell of
// the document, one per line.
func reviewAllText(doc *gofastcore.Document) string {
	var b strings.Builder
	for _, bl := range doc.AllBlocks() {
		b.WriteString(bl.Text)
		b.WriteByte('\n')
		if bl.Table != nil {
			for _, row := range bl.Table.Rows {
				b.WriteString(strings.Join(row, "\t"))
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// reviewAlloc returns the bytes allocated while extracting src.
func reviewAlloc(t *testing.T, name, src string) uint64 {
	t.Helper()
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	reviewExtract(t, name, src, nil)
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}
