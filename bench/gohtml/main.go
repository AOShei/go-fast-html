// Command gohtml extracts the text of every file in a list with
// go-fast-html, in the same way as run_html.py does with a Python tool:
// one process, WORKERS goroutines reading the files in turn, the time of
// each call (Extract and the text rendering, after the bytes are read),
// whether it failed, and the length of the text; the text goes to
// OUTDIR/text/NAME.txt, one line a file to OUTDIR/results.jsonl.
//
//	gohtml LIST OUTDIR WORKERS
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	gofastcore "github.com/AOShei/go-fast-core"
	gofasthtml "github.com/AOShei/go-fast-html"
)

type result struct {
	File  string  `json:"file"`
	S     float64 `json:"s"`
	Err   *string `json:"err"`
	Chars int     `json:"chars"`
}

func main() {
	list, out := os.Args[1], os.Args[2]
	workers, _ := strconv.Atoi(os.Args[3])
	f, err := os.Open(list)
	if err != nil {
		panic(err)
	}
	var files []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sc.Text() != "" {
			files = append(files, sc.Text())
		}
	}
	os.MkdirAll(filepath.Join(out, "text"), 0o755)
	// GOHTML_CHARSETS names a manifest (file, size, charset, url, tab
	// separated): each page is read with the charset of its HTTP header
	// as Extractor.Charset, as a crawler passes it on.
	charsets := map[string]string{}
	if m := os.Getenv("GOHTML_CHARSETS"); m != "" {
		mf, err := os.ReadFile(m)
		if err != nil {
			panic(err)
		}
		for _, l := range strings.Split(string(mf), "\n") {
			if f := strings.Split(l, "\t"); len(f) >= 3 && f[2] != "-" && f[2] != "charset" {
				charsets[f[0]] = f[2]
			}
		}
	}
	res := make([]result, len(files))
	var wg sync.WaitGroup
	for k := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e0 := gofasthtml.Extractor{Format: "html"}
			// One worker reads one document at a time (Workers: 1), as
			// the command does with -j above 1.
			opts := &gofastcore.Options{Workers: 1}
			for i := k; i < len(files); i += workers {
				b, err := os.ReadFile(files[i])
				if err != nil {
					panic(err)
				}
				name := filepath.Base(files[i])
				t0 := time.Now()
				o := *opts
				o.Name = name
				e := e0
				e.Charset = charsets[name]
				doc, err := e.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), &o)
				var text string
				if err == nil {
					if os.Getenv("GOHTML_VISIBLE") != "" {
						// What a browser shows: the body's blocks, not
						// stored data, declared-hidden blocks, the
						// attribute notes (title, aria-label) or images'
						// alternative text.
						text = visible(doc)
					} else if os.Getenv("GOHTML_EXCLUDE_HIDDEN") != "" {
						// The text without what the page declares hidden.
						var sb strings.Builder
						doc.WriteText(&sb, &gofastcore.RenderOptions{ExcludeDisplay: gofastcore.DisplayHidden})
						text = sb.String()
					} else {
						text = doc.Text()
					}
				}
				dt := time.Since(t0).Seconds()
				r := result{File: name, S: dt, Chars: len([]rune(text))}
				if err != nil {
					s := fmt.Sprint(err)
					r.Err = &s
				} else {
					os.WriteFile(filepath.Join(out, "text", name+".txt"), []byte(text), 0o644)
				}
				res[i] = r
			}
		}()
	}
	wg.Wait()
	w, _ := os.Create(filepath.Join(out, "results.jsonl"))
	defer w.Close()
	enc := json.NewEncoder(w)
	for _, r := range res {
		enc.Encode(r)
	}
}

func visible(doc *gofastcore.Document) string {
	var sb strings.Builder
	for _, p := range doc.Parts {
		if p.Kind == gofastcore.PartStored {
			continue
		}
		for _, b := range p.Blocks {
			if b.Attrs[gofastcore.AttrStored] != "" || b.Display&gofastcore.DisplayHidden != 0 ||
				b.Kind == gofastcore.KindImage || b.Kind == gofastcore.KindNote && b.Attrs[gofastcore.AttrNoteType] == gofastcore.NoteAttribute {
				continue
			}
			if b.Table != nil {
				for _, r := range b.Table.Rows {
					sb.WriteString(strings.Join(r, " "))
					sb.WriteByte('\n')
				}
				continue
			}
			sb.WriteString(b.Text)
			sb.WriteString("\n\n")
		}
	}
	return sb.String()
}
