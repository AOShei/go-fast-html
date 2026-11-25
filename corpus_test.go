package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
)

// corpusDir is the public test corpus: GOFAST_CORPUS, else
// ../.testdata/corpus.
func corpusDir() string {
	if d := os.Getenv("GOFAST_CORPUS"); d != "" {
		return d
	}
	return filepath.Join("..", ".testdata", "corpus")
}

// corpusTally counts what extraction did over one set of files.
type corpusTally struct {
	kinds                            map[string]int // what JSON, YAML and front matter gave
	files, panics, timeouts, invalid int
	errors                           map[string]int
	display                          map[string]int // blocks by reason
	displayFiles                     map[string]int // files with a block of that reason
	warnings                         map[string]int
}

func (c *corpusTally) add(m map[string]int, k string) { m[k]++ }

func keys(m map[string]int) string {
	var ks []string
	for k, v := range m {
		ks = append(ks, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(ks)
	return strings.Join(ks, " ")
}

func errKind(err error) string {
	for _, e := range []error{gofastcore.ErrUnsupported, gofastcore.ErrMalformed, gofastcore.ErrLimitExceeded,
		gofastcore.ErrPasswordRequired, gofastcore.ErrWrongPassword, gofastcore.ErrKeyRequired,
		gofastcore.ErrNoPartsSelected, context.DeadlineExceeded} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "other"
}

// TestCorpus extracts every html, md, epub and mht file of the public corpus,
// dev and held-out, and tallies the outcome. It fails on a panic, a
// Validate failure or a timeout. Held-out files are reported in
// aggregate only; the display reasons of dev files are listed one by one.
func TestCorpus(t *testing.T) {
	root := corpusDir()
	if _, err := os.Stat(root); err != nil {
		t.Skip("corpus absent:", root)
	}
	opts := &gofastcore.Options{Limits: gofastcore.Limits{
		MaxInputBytes: 64 << 20, MaxDecodedBytes: 128 << 20, MaxTextBytes: 32 << 20}}
	for _, set := range []string{"dev", "heldout"} {
		for _, dirName := range []string{"html", "md", "epub", "mht", "json", "jsonl", "yaml", "md-frontmatter"} {
			format := map[string]string{"jsonl": "json", "md-frontmatter": "md"}[dirName]
			if format == "" {
				format = dirName
			}
			dir := filepath.Join(root, set, dirName)
			ents, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			c := corpusTally{errors: map[string]int{}, display: map[string]int{}, displayFiles: map[string]int{}, warnings: map[string]int{}, kinds: map[string]int{}}
			for _, ent := range ents {
				if ent.IsDir() {
					continue
				}
				c.files++
				path := filepath.Join(dir, ent.Name())
				name := ent.Name()
				if set != "dev" {
					name = fmt.Sprintf("held-out file %d", c.files)
				}
				fopts := *opts
				fopts.Name = ent.Name() // .jsonl is JSON Lines
				doc, err, panicked, timedOut := corpusExtract(path, format, &fopts)
				switch {
				case panicked != "":
					c.panics++
					t.Errorf("%s/%s: panic: %s", set, format, panicked)
					continue
				case timedOut:
					c.timeouts++
					t.Errorf("%s/%s: %s timed out", set, format, name)
					continue
				case err != nil:
					c.add(c.errors, errKind(err))
					if set == "dev" {
						t.Logf("dev/%s/%s: %v", format, name, err)
					}
					continue
				}
				if err := doc.Validate(); err != nil {
					c.invalid++
					if set == "dev" {
						t.Errorf("dev/%s/%s: Validate: %v", format, name, err)
					} else {
						t.Errorf("%s/%s: %s fails Validate", set, format, name)
					}
				}
				for p, w := range doc.AllWarnings() {
					c.add(c.warnings, w.Code)
					if set == "dev" && w.Severity != gofastcore.SeverityInfo {
						n := 0
						if p != nil {
							n = p.Number
						}
						t.Logf("warn dev/%s/%s part %d %s: %s", format, name, n, w.Code, w.Message)
					}
				}
				if format == "json" || format == "yaml" || dirName == "md-frontmatter" {
					c.structured(t, set, dirName, name, path, doc)
				}
				seen := map[string]bool{}
				for p, b := range doc.AllBlocks() {
					if b.Display == 0 {
						continue
					}
					c.add(c.display, b.Display.String())
					if !seen[b.Display.String()] {
						seen[b.Display.String()] = true
						c.add(c.displayFiles, b.Display.String())
					}
					if set == "dev" {
						ex := []rune(b.Text)
						if len(ex) > 60 {
							ex = ex[:60]
						}
						t.Logf("display dev/%s/%s part %d %s %s: %q", format, name, p.Number, b.Kind, b.Display, string(ex))
					}
				}
			}
			t.Logf("%-7s %-4s files=%d errors{%s} invalid=%d panics=%d timeouts=%d blocks{%s} files{%s} warnings{%s} kinds{%s}",
				set, dirName, c.files, keys(c.errors), c.invalid, c.panics, c.timeouts, keys(c.display), keys(c.displayFiles), keys(c.warnings), keys(c.kinds))
		}
	}
}

// corpusExtract extracts one file within 10 s, recovering a panic.
func corpusExtract(path, format string, opts *gofastcore.Options) (doc *gofastcore.Document, err error, panicked string, timedOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		doc   *gofastcore.Document
		err   error
		panic string
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if p := recover(); p != nil {
				r.panic = fmt.Sprintf("%v\n%s", p, debug.Stack())
			}
			done <- r
		}()
		r.doc, r.err = (&Extractor{Format: format}).ExtractFile(ctx, path, opts)
	}()
	select {
	case r := <-done:
		if errors.Is(r.err, context.DeadlineExceeded) {
			return nil, nil, "", true
		}
		return r.doc, r.err, r.panic, false
	case <-time.After(12 * time.Second):
		return nil, nil, "", true
	}
}

// structured checks a JSON, YAML or Markdown file with front matter and
// counts what it gave: in a file that is UTF-8, every code block's text
// is the input at its location; blocks are in file order; every table
// row's location is inside its table; a front matter block comes first.
func (c *corpusTally) structured(t *testing.T, set, dir, name, path string, doc *gofastcore.Document) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	exact := utf8.Valid(src) && !bytes.HasPrefix(src, []byte("\xEF\xBB\xBF"))
	fail := func(format string, args ...any) {
		if set == "dev" {
			t.Errorf("dev/%s/%s: "+format, append([]any{dir, name}, args...)...)
		} else {
			t.Errorf("%s/%s: %s: a structure check fails", set, dir, name)
		}
	}
	if doc.Metadata.Extra["json.syntax_error"] != "" || doc.Metadata.Extra["yaml.syntax_error"] != "" {
		c.add(c.kinds, "read as lines")
	}
	end := int64(0)
	for i, b := range doc.Parts[0].Blocks {
		if b.Attrs[AttrFrontMatter] != "" {
			c.add(c.kinds, "front matter")
			if i != 0 {
				fail("front matter is block %d", i)
			}
		}
		if dir == "md-frontmatter" {
			continue
		}
		c.add(c.kinds, b.Kind.String())
		l := b.Loc
		if l.Offset < end || l.Offset+l.Length > int64(len(src)) {
			fail("block %d at %d+%d after %d", i, l.Offset, l.Length, end)
			return
		}
		end = l.Offset + l.Length
		switch b.Kind {
		case gofastcore.KindCode:
			n := 0
			if got := cleanCode(src[l.Offset:end], &n); exact && got != b.Text {
				fail("block %d: its text is not the input at its location", i)
			}
		case gofastcore.KindTable:
			for r, rl := range b.Table.RowLocs[1:] {
				if rl.Offset < l.Offset || rl.Offset+rl.Length > end {
					fail("block %d row %d outside the table", i, r+1)
				}
			}
		}
	}
}
