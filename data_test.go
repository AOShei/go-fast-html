package gofasthtml

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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
	if b[0].Loc.Offset != 1 || b[0].Loc.Length != int64(len(src)-2) {
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
		if src[b.Loc.Offset:b.Loc.Offset+b.Loc.Length] != b.Text+"\n" {
			t.Errorf("loc of %q: %q", b.Text, src[b.Loc.Offset:b.Loc.Offset+b.Loc.Length])
		}
	}
	want := []string{"# config\na: 1", "---\nb:\n  - x", "--- !tag\nc: 3\n..."}
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
	var b strings.Builder
	for i := range 2500 {
		fmt.Fprintf(&b, "{\"n\": %d}\n", i)
	}
	doc := dataDoc(t, "x.jsonl", b.String(), nil)
	blocks := doc.Parts[0].Blocks
	if len(blocks) != 3 || !blocks[1].Continued || !blocks[2].Continued || blocks[0].Continued {
		t.Fatalf("%d blocks", len(blocks))
	}
	joined := blocks[0].Text + "\n" + blocks[1].Text + "\n" + blocks[2].Text + "\n"
	if joined != b.String() || strings.Count(blocks[0].Text, "\n") != dataBlockLines-1 {
		t.Error("pieces do not join to the input")
	}
	// One line longer than a piece is cut at a character boundary.
	long := strings.Repeat("é", dataBlockBytes)
	doc = dataDoc(t, "l.json", "\""+long+"\"", nil)
	var all string
	for i, bl := range doc.Parts[0].Blocks {
		if bl.Continued != (i > 0) {
			t.Errorf("block %d continued %v", i, bl.Continued)
		}
		all += bl.Text
	}
	if all != "\""+long+"\"" || len(doc.Parts[0].Blocks) != 3 {
		t.Errorf("long line: %d blocks, %d bytes", len(doc.Parts[0].Blocks), len(all))
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
	// Two blocks of 1,000 lines fit in 20,000 bytes; the third does not.
	if len(w) != 1 || w[0] != "the limit on the amount of text was reached; the body is cut off there (1000 lines not read)" {
		t.Errorf("%q", w)
	}
	if len(doc.Parts[0].Blocks) != 2 {
		t.Errorf("%d blocks", len(doc.Parts[0].Blocks))
	}
}

// TestDataWorstCase reads inputs built to cost much for their size.
func TestDataWorstCase(t *testing.T) {
	n := 1 << 22
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
	for _, s := range []string{"{\"a\": [1, 2]}\n", "a: 1\n---\nb: 2\n...\n", "\xFF\xFEa\x00", "\r\n\r\n---", "caf\xe9\x01\x85"} {
		f.Add([]byte(s), false)
	}
	f.Fuzz(func(t *testing.T, b []byte, small bool) {
		opts := &gofastcore.Options{}
		if small {
			opts.Limits = gofastcore.Limits{MaxTextBytes: 600, MaxDecodedBytes: 2000}
		}
		for _, format := range []string{"json", "yaml"} {
			doc, err := (&Extractor{Format: format}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			for i, bl := range doc.Parts[0].Blocks {
				if bl.Kind != gofastcore.KindCode || bl.Attrs[gofastcore.AttrLanguage] != format {
					t.Fatalf("block %d: %v %v", i, bl.Kind, bl.Attrs)
				}
			}
		}
	})
}
