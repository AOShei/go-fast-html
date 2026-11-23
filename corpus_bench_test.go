package gofasthtml

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	gofastcore "github.com/AOShei/go-fast-core"
)

// The pages of a web crawl, for measuring speed and checking that a change
// meant to be faster leaves the output as it was. GOFASTHTML_PAGES names a
// file listing the pages, one path a line (bench/README.md makes one from
// Common Crawl); without it these are skipped.
//
//	GOFASTHTML_PAGES=all.list GOMAXPROCS=1 go test -run xxx -bench CorpusPages -benchtime 3x .
//	GOFASTHTML_PAGES=all.list GOFASTHTML_IDENT=before.txt go test -run TestCorpusIdentity .
//	(change, then the same with after.txt, and cmp the two)

func corpusPages(tb testing.TB, max int) []string {
	list := os.Getenv("GOFASTHTML_PAGES")
	if list == "" {
		tb.Skip("GOFASTHTML_PAGES is not set")
	}
	f, err := os.Open(list)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() && len(out) < max {
		if sc.Text() != "" {
			out = append(out, sc.Text())
		}
	}
	return out
}

// BenchmarkCorpusPages extracts the first 3,000 pages and renders their
// text, as a pipeline does.
func BenchmarkCorpusPages(b *testing.B) {
	var pages [][]byte
	for _, p := range corpusPages(b, 3000) {
		d, err := os.ReadFile(p)
		if err != nil {
			b.Fatal(err)
		}
		pages = append(pages, d)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, p := range pages {
			doc, err := (&Extractor{Format: "html"}).Extract(context.Background(), bytes.NewReader(p), int64(len(p)), &gofastcore.Options{Workers: 1})
			if err == nil {
				_ = doc.Text()
			}
		}
	}
}

// TestCorpusIdentity writes to $GOFASTHTML_IDENT, for every page, a hash of
// the whole document as JSON and its text, with spans and without.
func TestCorpusIdentity(t *testing.T) {
	out := os.Getenv("GOFASTHTML_IDENT")
	if out == "" {
		t.Skip("GOFASTHTML_IDENT is not set")
	}
	w, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, path := range corpusPages(t, 1<<30) {
		p, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, spans := range []bool{false, true} {
			doc, err := (&Extractor{Format: "html"}).Extract(context.Background(), bytes.NewReader(p), int64(len(p)), &gofastcore.Options{Workers: 1, Spans: spans})
			h := sha256.New()
			if err != nil {
				fmt.Fprint(h, "error: ", err)
			} else {
				json.NewEncoder(h).Encode(doc)
				h.Write([]byte(doc.Text()))
			}
			fmt.Fprintf(w, "%s %v %x\n", path, spans, h.Sum(nil))
		}
	}
}
