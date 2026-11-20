package gofasthtml

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// BenchmarkExtract extracts three files of the public dev corpus: an
// HTML page, a Markdown README and an EPUB. It is skipped without the
// corpus (GOFAST_CORPUS, else ../.testdata/corpus).
func BenchmarkExtract(b *testing.B) {
	for _, f := range []struct{ format, name string }{
		{"html", "bbc-news.html"},
		{"md", "tensorflow-tensorflow-README.md"},
		{"epub", "se-austen-pride-and-prejudice.epub"},
	} {
		b.Run(f.format, func(b *testing.B) {
			src, err := os.ReadFile(filepath.Join(corpusDir(), "dev", f.format, f.name))
			if err != nil {
				b.Skip("corpus file absent:", err)
			}
			e := &Extractor{Format: f.format}
			opts := &gofastcore.Options{Name: f.name}
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := e.Extract(context.Background(), bytes.NewReader(src), int64(len(src)), opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
