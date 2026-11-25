package gofasthtml

import (
	"bytes"
	"context"
	"errors"
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
		"---\ntitle: T\ntags: [a, b]\nauthors:\n  - name: A\n---\n# H\n",
		"+++\ntitle = \"T\"\n[p]\na = [1, '2']\n+++\nx",
		"---\nglobs: **/*.ts\n  more\n---\n",
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
			e := &Extractor{Format: format, MaxDepth: 24}
			doc, err := e.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
			if errors.Is(err, ErrEncodingUnknown) {
				// As a user would: give the encoding.
				e.Charset = "windows-1252"
				doc, err = e.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		}
	})
}

// FuzzCSV reads arbitrary input as csv and tsv, with the delimiter
// sniffed, given and declared, with quoting and without, under small
// limits, and checks the
// document against the contract: among others that every table is a
// rectangle of at least one row and one column, and that a piece of a
// split table has the columns of the piece before it.
//
//	go test -run XXX -fuzz FuzzCSV -fuzztime 30s -fuzzminimizetime 0s
func FuzzCSV(f *testing.F) {
	for _, s := range []string{
		"a,b,c\n1,\"2,3\",4\r\n\"x\"\"y\",\"line\nbreak\"\n",
		"sep=;\na;b\n\"unclosed;c\n",
		"Name;Price\nTea;1,50\n\n\n",
		"a\tb\n\"q\"x\tz\"\n\r\r",
		"\xEF\xBB\xBFa|b\n\x00\x1b\xC2\x85|\xEF\xAC\x81\n",
		"\xFF\xFEa\x00,\x00b\x00\n\x00",
		"caf\xE9,\x80\n",
		",,,,\n,\n",
	} {
		f.Add([]byte(s), byte(0))
	}
	f.Fuzz(func(t *testing.T, b []byte, mode byte) {
		start := time.Now()
		defer func() {
			if d := time.Since(start); d > time.Second {
				t.Fatalf("%d bytes took %v", len(b), d)
			}
		}()
		e := &Extractor{Format: "csv", Header: mode&1 != 0, NoQuotes: mode&64 != 0}
		if mode&2 != 0 {
			e.Format = "tsv"
		}
		if mode&4 != 0 {
			e.Delimiter = []rune{';', '|', ' ', '§'}[mode>>3&3]
		}
		opts := &gofastcore.Options{}
		if mode&32 != 0 {
			opts.Limits = gofastcore.Limits{MaxTextBytes: 2048, MaxDecodedBytes: 4096}
		}
		doc, err := e.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
		if errors.Is(err, ErrEncodingUnknown) {
			e.Charset = "windows-1252"
			doc, err = e.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := doc.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(doc.Parts) != 1 {
			t.Fatalf("%d parts", len(doc.Parts))
		}
		width := 0
		for i, bl := range doc.Parts[0].Blocks {
			if bl.Kind != gofastcore.KindTable || bl.Continued != (i > 0) {
				t.Fatalf("block %d: %v, continued %v", i, bl.Kind, bl.Continued)
			}
			for _, row := range bl.Table.Rows {
				if i == 0 && width == 0 {
					width = len(row)
				}
				if len(row) != width {
					t.Fatalf("block %d: a row of %d cells in a table of %d columns", i, len(row), width)
				}
			}
		}
	})
}

// FuzzNotebook reads arbitrary input as a Jupyter notebook, under small
// limits and without, and checks the document against the contract.
//
//	go test -run XXX -fuzz FuzzNotebook -fuzztime 30s -fuzzminimizetime 0s
func FuzzNotebook(f *testing.F) {
	for _, s := range []string{
		`{"nbformat":4,"nbformat_minor":5,"metadata":{"kernelspec":{"name":"python3"},"authors":[{"name":"a"},"b"]},"cells":[` +
			`{"cell_type":"markdown","id":"m","source":["# T\n","![x](attachment:a.png)"],"attachments":{"a.png":{"image/png":"iVBORw0K"}},"metadata":{"jupyter":{"source_hidden":true}}},` +
			`{"cell_type":"code","source":"x","metadata":{"collapsed":true},"outputs":[{"output_type":"stream","name":"stdout","text":["a\r","b\u001b[31m"]},` +
			`{"output_type":"execute_result","data":{"text/html":"<table><tr><td>1","text/plain":"1"}},` +
			`{"output_type":"display_data","data":{"image/svg+xml":"<svg/>","text/latex":"$x$"}},` +
			`{"output_type":"error","ename":"E","evalue":"v","traceback":["\u001b[0;31mE"]}]},` +
			`{"cell_type":"raw","source":"r"}]}`,
		`{"nbformat":3,"worksheets":[{"cells":[{"cell_type":"heading","level":9,"source":"h"},` +
			`{"cell_type":"code","input":"i","language":"r","outputs":[{"output_type":"pyout","html":"<b>x</b>","png":"AAAA"},{"output_type":"pyerr"}]}]}]}`,
		`{"cells":[1,"x",null,{"cell_type":7,"source":{}}],"metadata":[]}`,
		`{"nbformat":2}`,
		`[`,
	} {
		f.Add([]byte(s), false)
	}
	f.Fuzz(func(t *testing.T, b []byte, small bool) {
		start := time.Now()
		defer func() {
			if d := time.Since(start); d > time.Second {
				t.Fatalf("%d bytes took %v", len(b), d)
			}
		}()
		opts := &gofastcore.Options{Images: gofastcore.ImagesData, Spans: true}
		if small {
			opts.Limits = gofastcore.Limits{MaxTextBytes: 2048, MaxDecodedBytes: 4096}
		}
		doc, err := (&Extractor{Format: "ipynb"}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
		if err != nil {
			if !errors.Is(err, gofastcore.ErrMalformed) && !errors.Is(err, gofastcore.ErrUnsupported) {
				t.Fatal(err)
			}
			return
		}
		if err := doc.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})
}
