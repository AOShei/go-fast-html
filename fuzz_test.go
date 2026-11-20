package gofasthtml

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

// FuzzExtract runs the whole extractor on arbitrary input under small
// limits and checks the document against the contract.
//
// Run the fuzz targets briefly, with a short minimization time:
//
//	go test -run XXX -fuzz FuzzExtract -fuzztime 20s -fuzzminimizetime=2s
//
// By default Go's fuzzer minimizes each new interesting input for up to
// 60 s, during which it reports 0 execs/sec; that is not a slow input.
func FuzzExtract(f *testing.F) {
	for _, s := range []string{
		"<h1>T</h1><p>a<b>b</b><ul><li>x<li>y</ul>",
		"<table><tr><td rowspan=0 colspan=3>a<td>b<tr><td>c</table>",
		"<p hidden>h<span style='display:block;color:#fff'>v</span>",
		"<pre>\n code\xEF\xAC\x81</pre><script>x<!--<script></script>-->y</script>",
		"<meta charset=windows-1252><p>caf\xE9<!-- c --><img alt=a src=data:,x>",
		"\xFF\xFE<\x00p\x00>\x00x\x00",
		"<select><option>a<option>b</select><input type=hidden value=v>",
		"<svg><p>x</svg><math><mi>y</mi></math><table>z<tr><td>w</table>",
		`<table><tr><td>a<iframe hidden id=f srcdoc="<title>t</title><p>b<iframe srcdoc='<h1>c'>">x</iframe></table>`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		start := time.Now()
		defer func() {
			// Inputs are small; a slow one is a cost a file can impose.
			if d := time.Since(start); d > time.Second {
				t.Fatalf("%d bytes took %v", len(b), d)
			}
		}()
		for i, opts := range []*gofastcore.Options{
			{Spans: true, Images: gofastcore.ImagesData},
			{Limits: gofastcore.Limits{MaxTextBytes: 2048, MaxDecodedBytes: 4096}},
			{Spans: true},
		} {
			format := []string{"html", "md", "txt"}[(i+len(b))%3]
			doc, err := (&Extractor{Format: format, MaxDepth: 24}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		}
	})
}
