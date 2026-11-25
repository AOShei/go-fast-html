package gofasthtml

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/charset"
)

// TestSubtitleCorpus extracts every srt and vtt file of the public
// corpus, dev and held-out. Every file must read without an error, a
// panic or a Validate failure. Held-out files are counted in aggregate
// only. On dev files two oracles are checked: (a) the input at each
// block's Loc holds every word of the block's text; (b) the cues, those
// with a block and those counted as having no text, are as many as the
// lines with an arrow.
func TestSubtitleCorpus(t *testing.T) {
	root := corpusDir()
	if _, err := os.Stat(root); err != nil {
		t.Skip("corpus absent:", root)
	}
	opts := &gofastcore.Options{Limits: gofastcore.Limits{
		MaxInputBytes: 64 << 20, MaxDecodedBytes: 128 << 20, MaxTextBytes: 32 << 20}}
	for _, set := range []string{"dev", "heldout"} {
		for _, format := range []string{"srt", "vtt"} {
			dir := filepath.Join(root, set, format)
			ents, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			c := corpusTally{errors: map[string]int{}, warnings: map[string]int{}}
			cues, notes, stored := 0, 0, 0
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
				doc, err, panicked, timedOut := corpusExtract(path, format, opts)
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
					t.Errorf("%s/%s/%s: %v", set, format, name, err)
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
				for _, w := range doc.AllWarnings() {
					c.add(c.warnings, w.Code)
				}
				for _, p := range doc.Parts {
					for _, b := range p.Blocks {
						switch {
						case b.Attrs["start"] != "":
							cues++
						case b.Kind == gofastcore.KindNote:
							notes++
						case p.Kind == gofastcore.PartStored:
							stored++
						}
					}
				}
				if set == "dev" {
					subtitleOracles(t, path, format, doc)
				}
			}
			t.Logf("%-7s %-3s files=%d errors{%s} invalid=%d panics=%d timeouts=%d cues=%d notes=%d stored=%d warnings{%s}",
				set, format, c.files, keys(c.errors), c.invalid, c.panics, c.timeouts, cues, notes, stored, keys(c.warnings))
		}
	}
}

var anyTag = regexp.MustCompile(`<[^>]*>`)

var noTextCues = regexp.MustCompile(`^(\d+) cues? ha(?:s|ve) no text`)

// subtitleOracles checks the two oracles on one dev file, and logs what
// it gives when it has no cue.
func subtitleOracles(t *testing.T, path, format string, doc *gofastcore.Document) {
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	enc := charset.SniffText(src, "")
	if enc.Unknown {
		enc = charset.SniffText(src, "windows-1252")
	}
	// decodeAt decodes the input from offset off, as Extract did.
	decodeAt := func(off, n int64) []byte {
		if be, ok := utf32BOM(src); ok {
			d, _, _, _ := decodeUTF32(append(append([]byte(nil), src[:4]...), src[max(off, 4):off+n]...), be, 1<<30)
			return d
		}
		raw := src[off : off+n]
		r := charset.Result{Enc: enc.Enc}
		if off == 0 {
			r = enc
		}
		d, _, _, _ := charset.Decode(raw, r, 1<<30)
		return d
	}
	// (a) the words of each block's text are in the input at its Loc:
	// in it as it is, or with its tags taken out, since a tag can stand
	// inside a word (Foo<c>bar</c>, 漢<rt>kan</rt>).
	for _, p := range doc.Parts {
		for _, b := range p.Blocks {
			hay := gofastcore.ExpandLigatures(string(dropControlChars(decodeAt(b.Loc.Offset, b.Loc.Length))))
			bare := anyTag.ReplaceAllString(hay, "")
			for _, w := range words(b.Text) {
				if !strings.Contains(hay, w) && !strings.Contains(bare, w) {
					t.Errorf("dev/%s/%s: word %q of %q is not in the input at %d+%d", format, name, w, b.Text, b.Loc.Offset, b.Loc.Length)
					break
				}
			}
		}
	}
	// (b) blocks of cues and cues counted as empty: one per arrow line.
	dec := decodeAt(0, int64(len(src)))
	arrows := 0
	for i, l := range bytes.FieldsFunc(dec, func(r rune) bool { return r == '\n' || r == '\r' }) {
		l = dropControlChars(l)
		if format == "vtt" && i == 0 && bytes.HasPrefix(l, []byte("WEBVTT")) {
			continue
		}
		if bytes.Contains(l, []byte("-->")) {
			arrows++
		}
	}
	cues, empty := 0, 0
	for _, b := range doc.Parts[0].Blocks {
		if b.Attrs["start"] != "" {
			cues++
		}
	}
	for _, w := range doc.Parts[0].Warnings {
		if m := noTextCues.FindStringSubmatch(w.Message); m != nil {
			empty, _ = strconv.Atoi(m[1])
		}
	}
	if cues+empty != arrows {
		t.Errorf("dev/%s/%s: %d cue blocks and %d empty cues, %d lines with an arrow", format, name, cues, empty, arrows)
	}
	if cues == 0 {
		var ws []string
		for _, w := range doc.AllWarnings() {
			ws = append(ws, w.Code+": "+w.Message)
		}
		t.Logf("dev/%s/%s: no cue; %d blocks; %s", format, name, len(doc.Parts[0].Blocks), strings.Join(ws, "; "))
	}
}

// words are the runs of letters and digits of s.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func dropControlChars(b []byte) []byte {
	return bytes.Map(func(r rune) rune {
		if r != '\t' && r != '\n' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, b)
}
