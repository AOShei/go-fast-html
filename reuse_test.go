package gofasthtml

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	gofastcore "github.com/AOShei/go-fast-core"
)

// TestInputReused: the memory of one extraction (its input buffer, its
// tree's nodes and attributes) is reused by the next. A document must keep
// nothing of it: each document is extracted, then many others that reuse
// the memory, and its JSON must be as it was.
func TestInputReused(t *testing.T) {
	defer poisonReleased.Store(poisonReleased.Load())
	page := func(i int) string {
		return fmt.Sprintf(`<html lang="en-%d"><head><title>T%d</title><meta name="author" content="Author %d">
<meta name="keywords" content="k%d"><style>p { color: red }</style><script>var x%d = 1;</script></head>
<body><h1 id="top%d">Heading %d</h1><p title="tip %d" aria-label="label %d">Text %d <a href="/link/%d" name="n%d">link %d</a>
<img src="/img/%d.png" alt="alt %d" width="10" height="20"> <img src="data:image/gif;base64,R0lGODlhAQABAAAAACw="> <b>bold %d</b></p><!-- comment %d -->
<pre class="language-go%d"><code>code %d</code></pre><ul><li>one %d<li><input type="checkbox" checked> two %d</ul>
<table><tr><th>h%d<td><a href="/c%d">c%d</a></table><form><input type="hidden" name="f%d" value="v%d">
<select name="s%d"><option>o%d<option label="l%d"></select><textarea name="t%d">ta%d</textarea></form></body></html>`,
			i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i)
	}
	inputs := map[string]func(i int) string{
		"html": page,
		"md": func(i int) string {
			return fmt.Sprintf("# Title %d\n\nText [link %d](/l/%d) ![alt %d](/i%d.png)\n\n```go\ncode %d\n```\n", i, i, i, i, i, i)
		},
		"txt":  func(i int) string { return fmt.Sprintf("Plain text %d\n\nsecond paragraph %d\n", i, i) },
		"csv":  func(i int) string { return fmt.Sprintf("name,value\nrow%d,%d\nother%d,x%d\n", i, i, i, i) },
		"json": func(i int) string { return fmt.Sprintf("{\"key%d\": \"value %d\"}\n", i, i) },
		"yaml": func(i int) string { return fmt.Sprintf("key%d: value %d\n---\nother: %d\n", i, i, i) },
		"ipynb": func(i int) string {
			return fmt.Sprintf(`{"nbformat":4,"nbformat_minor":5,"metadata":{"title":"N%d"},"cells":[{"cell_type":"markdown","id":"c%d","metadata":{},"source":"# Cell %d [l](/x%d)"},{"cell_type":"code","id":"d%d","metadata":{},"source":"print(%d)","outputs":[],"execution_count":1}]}`, i, i, i, i, i, i)
		},
	}
	extract := func(format, src string, spans bool) string {
		doc, err := (&Extractor{Format: format}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), &gofastcore.Options{Spans: spans})
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		b, _ := json.Marshal(doc)
		return string(b)
	}
	for format, gen := range inputs {
		for _, spans := range []bool{false, true} {
			src := gen(1)
			// Read before anything reuses the memory, the document is as it
			// should be; with the buffer overwritten as it is given back,
			// it must not change.
			poisonReleased.Store(false)
			want := extract(format, src, spans)
			poisonReleased.Store(true)
			if got := extract(format, src, spans); got != want {
				t.Errorf("%s (spans %v): the document shares the memory given back:\n%s\n%s", format, spans, want, got)
			}
			doc, err := (&Extractor{Format: format}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), &gofastcore.Options{Spans: spans})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(doc)
			// Others of every format and of many sizes reuse the memory.
			for i := 2; i < 200; i++ {
				for f, g := range inputs {
					s := g(i)
					if i%7 == 0 && f != "ipynb" {
						s = strings.Repeat(s, 50) // larger inputs, other buffer sizes
					}
					extract(f, s, spans)
				}
			}
			after, _ := json.Marshal(doc)
			if !bytes.Equal(before, after) {
				t.Errorf("%s (spans %v): the document changed when its memory was reused:\n%s\n%s", format, spans, before, after)
			}
			if again := extract(format, src, spans); again != string(before) {
				t.Errorf("%s: a second extraction differs", format)
			}
		}
	}
}

// TestInputReusedConcurrently: extractions on many goroutines share the
// pools; each gives what it gives alone.
func TestInputReusedConcurrently(t *testing.T) {
	gen := func(i int) string {
		return fmt.Sprintf(`<p title="t%d">Text %d <a href="/l%d">l</a></p><script>var v%d</script><ul><li>%d</ul>`, i, i, i, i, i) +
			strings.Repeat(fmt.Sprintf("<p>more %d</p>", i), i%50)
	}
	want := make([]string, 64)
	for i := range want {
		s := gen(i)
		doc, err := Extract(context.Background(), strings.NewReader(s), int64(len(s)), &gofastcore.Options{Name: "a.html", Spans: true})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(doc)
		want[i] = string(b)
	}
	errs := make(chan string, 8)
	for g := range 8 {
		go func() {
			defer func() { errs <- "" }()
			for k := range 400 {
				i := (g*31 + k) % len(want)
				s := gen(i)
				doc, err := Extract(context.Background(), strings.NewReader(s), int64(len(s)), &gofastcore.Options{Name: "a.html", Spans: true})
				if err != nil {
					errs <- err.Error()
					return
				}
				if b, _ := json.Marshal(doc); string(b) != want[i] {
					errs <- fmt.Sprintf("page %d differs", i)
					return
				}
			}
		}()
	}
	for range 8 {
		if e := <-errs; e != "" {
			t.Error(e)
		}
	}
}
