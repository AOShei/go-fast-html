package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
)

// dataDoc extracts src named name and checks the document.
func dataDoc(t testing.TB, name, src string, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	if opts == nil {
		opts = &gofastcore.Options{}
	}
	opts.Name = name
	doc, err := (&Extractor{}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return doc
}

func TestDataDetect(t *testing.T) {
	for name, want := range map[string]string{"a.json": "json", "B.JSONL": "json", "x.ndjson": "json", "m.geojson": "json",
		"c.yaml": "yaml", "d.YML": "yaml"} {
		if got := Detect(name, nil); got != want {
			t.Errorf("Detect(%q) = %q", name, got)
		}
	}
	if got := Detect("", []byte(`{"a": 1}`)); got != "txt" {
		t.Errorf("JSON by content: %q", got)
	}
	doc := dataDoc(t, "x.yml", "a: 1\n", nil)
	if doc.Source.Format != "yaml" || doc.Source.MIME != "application/yaml" {
		t.Errorf("source: %+v", doc.Source)
	}
}

func TestDataJSON(t *testing.T) {
	src := "\n{\r\n  \"name\": \"café\",\r\n\t\"list\": [1, 2],  \r\n  \"ctl\": \"a\x01b\"\r\n}\r\n\n"
	doc := dataDoc(t, "a.json", src, nil)
	b := doc.Parts[0].Blocks
	if len(b) != 1 || b[0].Kind != gofastcore.KindCode || b[0].Attrs[gofastcore.AttrLanguage] != "json" {
		t.Fatalf("blocks: %+v", b)
	}
	want := "{\n  \"name\": \"café\",\n\t\"list\": [1, 2],  \n  \"ctl\": \"ab\"\n}"
	if b[0].Text != want {
		t.Errorf("text:\n%q\nwant\n%q", b[0].Text, want)
	}
	if b[0].Loc.Offset != 1 || b[0].Loc.Length != int64(strings.LastIndex(src, "}")) {
		t.Errorf("loc: %+v (input %d bytes)", b[0].Loc, len(src))
	}
	// Invalid JSON is text like any other.
	doc = dataDoc(t, "b.json", "{not json", nil)
	if doc.Parts[0].Blocks[0].Text != "{not json" {
		t.Errorf("invalid: %+v", doc.Parts[0].Blocks)
	}
}

func TestDataYAMLDocuments(t *testing.T) {
	src := "# config\na: 1\n---\nb:\n  - x\n--- !tag\nc: 3\n...\n"
	doc := dataDoc(t, "c.yaml", src, nil)
	var got []string
	for _, b := range doc.Parts[0].Blocks {
		if b.Continued {
			t.Errorf("continued: %q", b.Text)
		}
		got = append(got, b.Text)
		if src[b.Loc.Offset:b.Loc.Offset+b.Loc.Length] != b.Text {
			t.Errorf("loc of %q: %q", b.Text, src[b.Loc.Offset:b.Loc.Offset+b.Loc.Length])
		}
	}
	want := []string{"# config\na: 1", "b:\n  - x", "!tag\nc: 3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("documents: %q", got)
	}
	// A JSON file is not split at "---".
	doc = dataDoc(t, "d.json", "[\n---\n]", nil)
	if len(doc.Parts[0].Blocks) != 1 {
		t.Errorf("json split: %d", len(doc.Parts[0].Blocks))
	}
}

func TestDataSplit(t *testing.T) {
	// JSON Lines of records: a table, in pieces of csvRowsPerBlock rows,
	// each with the header row.
	var b strings.Builder
	for i := range 2500 {
		fmt.Fprintf(&b, "{\"n\": %d}\n", i)
	}
	doc := dataDoc(t, "x.jsonl", b.String(), nil)
	blocks := doc.Parts[0].Blocks
	if len(blocks) != 3 || !blocks[1].Continued || !blocks[2].Continued || blocks[0].Continued {
		t.Fatalf("%d blocks", len(blocks))
	}
	rows := 0
	for _, bl := range blocks {
		tb := bl.Table
		if bl.Kind != gofastcore.KindTable || tb.HeaderRows != 1 || tb.Rows[0][0] != "n" || len(tb.RowLocs) != len(tb.Rows) {
			t.Fatalf("block: %+v", bl)
		}
		for i, r := range tb.Rows[1:] {
			loc := tb.RowLocs[i+1]
			if r[0] != fmt.Sprint(rows) || loc.Anchor != fmt.Sprintf("[%d]", rows) || b.String()[loc.Offset:loc.Offset+loc.Length] != fmt.Sprintf("{\"n\": %d}", rows) {
				t.Fatalf("row %d: %q %+v", rows, r, loc)
			}
			rows++
		}
	}
	if rows != 2500 {
		t.Errorf("%d rows", rows)
	}
	// A value is never cut: one longer than a block is a block of its own.
	long := strings.Repeat("\u00e9", dataBlockBytes)
	doc = dataDoc(t, "l.json", "[\n1,\n\""+long+"\",\n2\n]", nil)
	if bl := doc.Parts[0].Blocks; len(bl) != 3 || bl[0].Text != "[\n1" || bl[1].Text != "\""+long+"\"" || bl[2].Text != "2" ||
		bl[1].Loc.Anchor != "[1]" {
		t.Errorf("long value: %d blocks", len(bl))
	}
}

func TestDataEmptyAndEncoding(t *testing.T) {
	for _, src := range []string{"", "  \n\t\n"} {
		doc := dataDoc(t, "e.json", src, nil)
		if len(doc.Parts[0].Blocks) != 0 || len(csvWarnings(doc, gofastcore.WarnPartNoText)) != 1 {
			t.Errorf("%q: %+v", src, doc.Parts[0])
		}
	}
	utf16 := []byte{0xFF, 0xFE}
	for _, r := range "k: v\n" {
		utf16 = append(utf16, byte(r), 0)
	}
	doc := dataDoc(t, "u.yaml", string(utf16), nil)
	if doc.Parts[0].Blocks[0].Text != "k: v" {
		t.Errorf("utf-16: %q", doc.Parts[0].Blocks[0].Text)
	}
	doc = dataDoc(t, "w.yaml", "k: caf\xe9\n", nil)
	if doc.Parts[0].Blocks[0].Text != "k: café" {
		t.Errorf("windows-1252: %q", doc.Parts[0].Blocks[0].Text)
	}
}

func TestDataBudget(t *testing.T) {
	var b strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	doc := dataDoc(t, "x.yaml", b.String(), &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 20000}})
	w := csvWarnings(doc, gofastcore.WarnLimitExceeded)
	// The file is one value, a plain scalar of 3,000 lines: the budget
	// cuts its block at a line end.
	bl := doc.Parts[0].Blocks
	if len(bl) != 1 || !strings.HasPrefix(b.String(), bl[0].Text+"\n") {
		t.Fatalf("%d blocks", len(bl))
	}
	read := strings.Count(bl[0].Text, "\n") + 1
	if len(w) != 1 || w[0] != fmt.Sprintf("the limit on the amount of text was reached; the body is cut off there (%d lines not read)", 3000-read) {
		t.Errorf("%q (%d lines read)", w, read)
	}
}

// TestDataWorstCase reads inputs built to cost much for their size.
func TestDataWorstCase(t *testing.T) {
	// It is quadratic work these catch, which at a mebibyte is minutes:
	// under the race detector, whose run on a small CI machine is slower
	// by far more than raceSlowdown for a million blocks, that is enough.
	n := 1 << 22
	if raceSlowdown > 1 {
		n = 1 << 20
	}
	if testing.Short() {
		n = 1 << 18
	}
	for name, src := range map[string]string{
		"newlines":     strings.Repeat("\n", n),
		"one line":     strings.Repeat("a", n),
		"short lines":  strings.Repeat("a\n", n/2),
		"yaml markers": strings.Repeat("---\n", n/4),
		"controls":     strings.Repeat("\x01\r", n/2),
	} {
		start := time.Now()
		dataDoc(t, "w.yaml", src, nil)
		if d := time.Since(start); d > 5*raceSlowdown*time.Second {
			t.Errorf("%s: %d bytes took %v", name, len(src), d)
		}
	}
}

// FuzzData reads arbitrary input as JSON and YAML, under small limits and
// without, and checks the document against the contract.
//
//	go test -run XXX -fuzz FuzzData -fuzztime 30s -fuzzminimizetime 0s
func FuzzData(f *testing.F) {
	for _, s := range []string{"{\"a\": [1, 2]}\n", "a: 1\n---\nb: 2\n...\n", "\xFF\xFEa\x00", "\r\n\r\n---", "caf\xe9\x01\x85",
		"[{\"a\": 1}, {\"a\": 2, \"b\": \"x\"}]", "- {a: 1}\n- {a: 2}\n", "// c\n{\"a\": 1, // d\n}\n# e", "? [a]\n: b\n&x c: *x\n"} {
		f.Add([]byte(s), false)
	}
	f.Fuzz(func(t *testing.T, b []byte, small bool) {
		opts := &gofastcore.Options{}
		if small {
			opts.Limits = gofastcore.Limits{MaxTextBytes: 600, MaxDecodedBytes: 2000}
		}
		for _, format := range []string{"json", "yaml"} {
			e := &Extractor{Format: format}
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
			// Blocks are in file order and do not overlap; in UTF-8 input,
			// a code block's text is the input at its location.
			exact := utf8.Valid(b) && !bytes.HasPrefix(b, []byte("\xEF\xBB\xBF")) && !hasWarning(doc, gofastcore.WarnTextEncodingDetected)
			end := int64(0)
			for i, bl := range doc.Parts[0].Blocks {
				l := bl.Loc
				if l.Offset < end || l.Offset+l.Length > int64(len(b)) {
					t.Fatalf("block %d at %d+%d after %d", i, l.Offset, l.Length, end)
				}
				end = l.Offset + l.Length
				switch bl.Kind {
				case gofastcore.KindCode:
					if bl.Attrs[gofastcore.AttrLanguage] != format {
						t.Fatalf("block %d: %v", i, bl.Attrs)
					}
					n := 0
					if got := cleanCode(b[l.Offset:end], &n); exact && got != bl.Text {
						t.Fatalf("block %d: %q at its location, text %q", i, got, bl.Text)
					}
				case gofastcore.KindTable:
					for r, rl := range bl.Table.RowLocs[1:] {
						if rl.Offset < l.Offset || rl.Offset+rl.Length > end || rl.Anchor == "" {
							t.Fatalf("block %d row %d at %+v", i, r+1, rl)
						}
					}
				default:
					t.Fatalf("block %d: kind %v", i, bl.Kind)
				}
			}
		}
	})
}

// TestDataReview covers the findings of the review of the first version.
func TestDataReview(t *testing.T) {
	texts := func(doc *gofastcore.Document) []string {
		var out []string
		for _, b := range doc.Parts[0].Blocks {
			s := b.Text
			if b.Continued {
				s = "+" + s
			}
			out = append(out, s)
		}
		return out
	}
	check := func(name, src string, want ...string) {
		t.Helper()
		if got := texts(dataDoc(t, name, src, nil)); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%q:\n got %q\nwant %q", src, got, want)
		}
	}
	// A lone CR ends a line.
	check("a.yaml", "a: 1\rb: 2\r---\rc: 3\r", "a: 1\nb: 2", "c: 3")
	// Document markers and directives are not in the blocks: tab after
	// ---; after ..., the next document, its directives first.
	// (A mapping on the "---" line is not YAML: the file is read as lines.)
	check("b.yaml", "a: 1\n---\tb: 2\n", "a: 1", "---\tb: 2")
	check("c.yaml", "a: 1\n...\nb: 2\n", "a: 1", "b: 2")
	check("d.yaml", "a: 1\n...\n%YAML 1.2\n---\nb: 2\n", "a: 1", "b: 2")
	// Lines of control characters only are blank.
	check("e.json", "\x01\na: 1\n\x01", "a: 1")
	doc := dataDoc(t, "f.json", "\x01\n\x00\n", nil)
	if len(doc.Parts[0].Blocks) != 0 || len(csvWarnings(doc, gofastcore.WarnPartNoText)) != 1 {
		t.Errorf("controls only: %+v", doc.Parts[0])
	}
	// Code is verbatim: ligatures are kept.
	check("g.json", "{\"k\": \"\ufb01le\"}", "{\"k\": \"\ufb01le\"}")

	// Pieces joined with line feeds give back the document: blank lines
	// and white space at a split are kept.
	var b strings.Builder
	b.WriteString("text: |\n")
	for range 998 {
		b.WriteString("  x\n")
	}
	b.WriteString("\n\n  y\n")
	doc = dataDoc(t, "h.yaml", b.String(), nil)
	var joined []string
	for _, bl := range doc.Parts[0].Blocks {
		joined = append(joined, bl.Text)
	}
	if strings.Join(joined, "\n")+"\n" != b.String() {
		t.Errorf("blank lines at a split lost: %d blocks", len(joined))
	}
	ws := "a\n" + strings.Repeat(" ", dataBlockBytes+100) + "b\n"
	doc = dataDoc(t, "i.json", ws, nil)
	joined = joined[:0]
	for _, bl := range doc.Parts[0].Blocks {
		joined = append(joined, bl.Text)
	}
	if strings.Join(joined, "\n")+"\n" != ws {
		t.Errorf("white space at a split lost: %d blocks", len(joined))
	}

	// A document after a split is not Continued.
	b.Reset()
	for range 1000 {
		b.WriteString("a: 1\n")
	}
	b.WriteString("\n---\nb: 2\n")
	doc = dataDoc(t, "j.yaml", b.String(), nil)
	last := doc.Parts[0].Blocks[len(doc.Parts[0].Blocks)-1]
	if last.Text != "b: 2" || last.Continued || last.Attrs[AttrDocument] != "2" {
		t.Errorf("document after a split: %q continued %v", last.Text, last.Continued)
	}

	// Lines not read: every line after the last block made.
	for src, want := range map[string]string{
		"a: 1\n---\nb: 2\n\n\n\n\n": "(6 lines not read)",
		"a: 1\n\n\n\n---\nb: 2\n":   "(5 lines not read)",
	} {
		doc := dataDoc(t, "k.yaml", src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: gofastcore.BlockCost + 10}})
		if w := csvWarnings(doc, gofastcore.WarnLimitExceeded); len(w) != 1 || !strings.HasSuffix(w[0], want) {
			t.Errorf("%q: %q", src, w)
		}
	}
	// The decode cut gives the bytes not read.
	doc = dataDoc(t, "l.json", strings.Repeat("x\n", 1000), &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 100}})
	if w := csvWarnings(doc, gofastcore.WarnLimitExceeded); len(w) != 1 || !strings.HasSuffix(w[0], "(1900 bytes)") {
		t.Errorf("decode: %q", w)
	}
}
