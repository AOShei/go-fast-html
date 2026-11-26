package gofasthtml

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// structBlock is a block as these tests compare it.
type structBlock struct {
	kind   gofastcore.Kind
	text   string // code: the text; table: the rows, cells joined by |, rows by /
	anchor string
	end    string // Attrs["path_end"]
}

func structBlocks(t *testing.T, doc *gofastcore.Document, src string) []structBlock {
	t.Helper()
	var out []structBlock
	for _, b := range doc.Parts[0].Blocks {
		sb := structBlock{kind: b.Kind, anchor: b.Loc.Anchor, end: b.Attrs[AttrPathEnd]}
		loc := src[b.Loc.Offset : b.Loc.Offset+b.Loc.Length]
		switch b.Kind {
		case gofastcore.KindCode:
			sb.text = b.Text
			// The text is the input at the block's location.
			n := 0
			if got := cleanCode([]byte(loc), &n); got != b.Text {
				t.Errorf("block %q: its location holds %q", b.Text, loc)
			}
		case gofastcore.KindTable:
			var rows []string
			for i, r := range b.Table.Rows {
				rows = append(rows, strings.Join(r, "|"))
				rl := b.Table.RowLocs[i]
				if i == 0 {
					if rl != (gofastcore.Location{}) {
						t.Errorf("header row location %+v", rl)
					}
					continue
				}
				if rl.Offset < b.Loc.Offset || rl.Offset+rl.Length > b.Loc.Offset+b.Loc.Length || rl.Anchor == "" {
					t.Errorf("row %d location %+v outside the table's %+v", i, rl, b.Loc)
				}
			}
			sb.text = strings.Join(rows, "/")
		}
		out = append(out, sb)
	}
	return out
}

func checkStruct(t *testing.T, name, src string, want ...structBlock) *gofastcore.Document {
	t.Helper()
	doc := dataDoc(t, name, src, nil)
	got := structBlocks(t, doc, src)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s %q:\n got %+v\nwant %+v", name, src, got, want)
	}
	return doc
}

func code(text, anchor, end string) structBlock {
	return structBlock{kind: gofastcore.KindCode, text: text, anchor: anchor, end: end}
}

func table(rows, anchor, end string) structBlock {
	return structBlock{kind: gofastcore.KindTable, text: rows, anchor: anchor, end: end}
}

// big returns a JSON string member value of n bytes.
func big(n int) string { return strings.Repeat("x", n) }

func TestStructJSONSmall(t *testing.T) {
	// A top-level value that fits is one block, as written.
	checkStruct(t, "a.json", "{\"a\": 1, \"b\": [1, 2]}\n", code(`{"a": 1, "b": [1, 2]}`, "", ""))
	checkStruct(t, "b.json", "\"s\"", code(`"s"`, "", ""))
	// A top-level array of records is a table whatever its length: the
	// header row is the keys in the order first seen, a cell the value as
	// written (a string in quotes, its escapes decoded), absent keys
	// empty.
	checkStruct(t, "c.json", `[{"id": 1, "n": "caf\u00e9 \"x\""}, {"id": 2, "ok": true, "z": null}]`,
		table(`id|n|ok|z/1|"café "x""||/2||true|null`, "[0]", "[1]"))
	// One record, or records with nested values, are not a table.
	checkStruct(t, "d.json", `[{"id": 1}]`, code(`[{"id": 1}]`, "", ""))
	checkStruct(t, "e.json", `[{"id": 1}, {"id": [2]}]`, code(`[{"id": 1}, {"id": [2]}]`, "", ""))
}

func TestStructJSONSplit(t *testing.T) {
	x := big(dataUnitBytes)
	src := "{\n  \"a\": 1,\n  \"b\": {\"c\": \"" + x + "\", \"d\": 2},\n  \"odd key\": [3]\n}\n"
	// The top level is split; "b" is longer than a block and is split in
	// turn, its long member a block of its own. The key lines of a split
	// container go with its first block.
	checkStruct(t, "a.json", src,
		code("{\n  \"a\": 1", "a", ""),
		code("  \"b\": {\"c\": \""+x+"\"", "b.c", ""),
		code(`"d": 2`, "b.d", ""),
		code(`  "odd key": [3]`, `["odd key"]`, ""))
	// Members are packed into blocks of at most dataUnitBytes.
	var b strings.Builder
	b.WriteString("[\n")
	for i := range 400 {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "  [%d, \"%s\"]", i, big(20))
	}
	b.WriteString("\n]\n")
	doc := dataDoc(t, "p.json", b.String(), nil)
	blocks := structBlocks(t, doc, b.String())
	if len(blocks) < 2 || blocks[0].anchor != "[0]" || blocks[1].anchor == "" {
		t.Fatalf("%d blocks: %+v", len(blocks), blocks[0])
	}
	for _, bl := range doc.Parts[0].Blocks {
		if len(bl.Text) > dataUnitBytes+16 {
			t.Errorf("block of %d bytes", len(bl.Text))
		}
	}
	last := blocks[len(blocks)-1]
	if last.end != "[399]" {
		t.Errorf("last block ends at %q", last.end)
	}
}

func TestStructJSONRecords(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"meta": {"n": 3}, "rows": [`)
	for i := range 300 {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, `{"id": %d, "name": "row %d"}`, i, i)
	}
	b.WriteString(`, {"nested": {"a": 1}}, {"id": 300}, "x"]}`)
	doc := dataDoc(t, "r.json", b.String(), nil)
	got := structBlocks(t, doc, b.String())
	// The run of 300 records is a table; the nested object ends it; a
	// lone record after it is a member like any other. The key line
	// before the table is a block of its own, at the array's path.
	if len(got) != 4 || got[0].kind != gofastcore.KindCode || got[0].anchor != "meta" ||
		got[1].kind != gofastcore.KindCode || got[1].text != `"rows": [` || got[1].anchor != "rows" || got[1].end != "" ||
		got[2].kind != gofastcore.KindTable || got[2].anchor != "rows[0]" || got[2].end != "rows[299]" ||
		got[3].kind != gofastcore.KindCode || got[3].anchor != "rows[300]" || got[3].end != "rows[302]" {
		t.Fatalf("%+v", got)
	}
	tb := doc.Parts[0].Blocks[2].Table
	if len(tb.Rows) != 301 || tb.Rows[0][1] != "name" || tb.Rows[300][1] != `"row 299"` || tb.RowLocs[300].Anchor != "rows[299]" {
		t.Errorf("table: %d rows, %q", len(tb.Rows), tb.Rows[0])
	}
	if l := tb.RowLocs[1]; b.String()[l.Offset:l.Offset+l.Length] != `{"id": 0, "name": "row 0"}` {
		t.Errorf("row location %+v", l)
	}
}

func TestStructJSONComments(t *testing.T) {
	x := big(dataUnitBytes)
	// JSON with comments and a trailing comma (tsconfig.json): comment
	// lines above a member, and a comment after it on its line, go with it;
	// comments after the last block are a block.
	src := "{\n  // the first\n  \"a\": 1, // one\n\n  /* long */\n  \"b\": \"" + x + "\",\n}\n// end\n"
	checkStruct(t, "t.json", src,
		code("{\n  // the first\n  \"a\": 1, // one", "a", ""),
		code("  /* long */\n  \"b\": \""+x+"\"", "b", ""),
		code("// end", "", ""))
}

func TestStructJSONLines(t *testing.T) {
	src := "{\"a\": 1}\n{\"a\": 2, \"b\": \"x\"}\n\nnot json\n[1, 2]\n"
	checkStruct(t, "l.jsonl", src,
		table(`a|b/1|/2|"x"`, "[0]", "[1]"),
		code("not json\n[1, 2]", "[2]", "[3]"))
	// A .json with several values is read as JSON Lines.
	checkStruct(t, "m.json", "1\n2\n", code("1\n2", "[0]", "[1]"))
}

func TestStructJSONNotJSON(t *testing.T) {
	doc := checkStruct(t, "n.json", "{\"a\": 1,\n \"b\" 2}\n", code("{\"a\": 1,\n \"b\" 2}", "", ""))
	if got := doc.Metadata.Extra[ExtraJSONSyntaxError]; got != `line 2, column 6: '2' where ':' is expected after a key` {
		t.Errorf("syntax error: %q", got)
	}
	// Nesting past the bound is read as lines, a limit and not an error.
	deep := strings.Repeat("[", 600) + strings.Repeat("]", 600)
	doc = dataDoc(t, "d.json", deep, nil)
	if doc.Metadata.Extra[ExtraJSONSyntaxError] != "" || len(doc.Parts[0].Blocks) != 1 ||
		len(csvWarnings(doc, gofastcore.WarnLimitExceeded)) != 1 {
		t.Errorf("deep: %+v %+v", doc.Metadata.Extra, doc.Parts[0].Warnings)
	}
	ydeep := strings.Repeat("[", 600) + strings.Repeat("]", 600)
	doc = dataDoc(t, "d.yaml", ydeep, nil)
	if doc.Metadata.Extra[ExtraYAMLSyntaxError] != "" || len(csvWarnings(doc, gofastcore.WarnLimitExceeded)) != 1 {
		t.Errorf("deep yaml: %+v %+v", doc.Metadata.Extra, doc.Parts[0].Warnings)
	}
	// Bytes that are not UTF-8 are U+FFFD: JSON is UTF-8.
	doc = dataDoc(t, "u.json", "[\"\xff\x01\"]", nil)
	if doc.Parts[0].Blocks[0].Text != "[\"\uFFFD\"]" || len(csvWarnings(doc, gofastcore.WarnTextUnmappedGlyphs)) != 1 {
		t.Errorf("not UTF-8: %+q %+v", doc.Parts[0].Blocks[0].Text, doc.Parts[0].Warnings)
	}
}

func TestStructYAML(t *testing.T) {
	x := strings.Repeat("y", dataUnitBytes)
	src := "# top\nname: demo  # the name\nlist:\n  - a\n  - b\nlong:\n  first: " + x + "\n  # about second\n  second: 2\n'odd key': [3]\n"
	checkStruct(t, "a.yaml", src,
		code("# top\nname: demo  # the name\nlist:\n  - a\n  - b", "name", "list"),
		code("long:\n  first: "+x, "long.first", ""),
		code("  # about second\n  second: 2", "long.second", ""),
		code("'odd key': [3]", `["odd key"]`, ""))
	// A split sequence of mappings of scalars: a table, quoted values in
	// their quotes, an alias as written.
	var b strings.Builder
	b.WriteString("people:\n")
	for i := range 200 {
		fmt.Fprintf(&b, "  - name: \"P %d\"\n    age: %d\n", i, i)
	}
	b.WriteString("  - &x {name: 'Q', age: *a}\n")
	doc := dataDoc(t, "p.yaml", b.String(), nil)
	got := structBlocks(t, doc, b.String())
	// The key line is a block of its own; a mapping with an anchor is not
	// a record (a row would not show it).
	if len(got) != 3 || got[0].kind != gofastcore.KindCode || got[0].text != "people:" || got[0].anchor != "people" ||
		got[1].kind != gofastcore.KindTable || got[1].anchor != "people[0]" || got[1].end != "people[199]" ||
		got[2].kind != gofastcore.KindCode || got[2].text != "  - &x {name: 'Q', age: *a}" || got[2].anchor != "people[200]" {
		t.Fatalf("%+v", got)
	}
	tb := doc.Parts[0].Blocks[1].Table
	if tb.Rows[1][0] != `"P 0"` || tb.Rows[200][0] != `"P 199"` {
		t.Errorf("rows %q %q", tb.Rows[1], tb.Rows[200])
	}
	// A cell keeps its anchor and tag, an alias as written.
	b.Reset()
	b.WriteString("people:\n")
	for i := range 200 {
		fmt.Fprintf(&b, "  - name: \"P %d\"\n    age: %d\n", i, i)
	}
	b.WriteString("  - {name: &q 'Q', age: *a, id: !!str 7}\n")
	doc = dataDoc(t, "q.yaml", b.String(), nil)
	tb = doc.Parts[0].Blocks[1].Table
	if r := tb.Rows[201]; r[0] != "&q 'Q'" || r[1] != "*a" || r[2] != "!!str 7" {
		t.Errorf("row %q", r)
	}
}

func TestStructYAMLDocuments(t *testing.T) {
	var x string
	src := "a: 1\n# after a\n---\n# about b\nb: 2\n---\n- c\n"
	doc := checkStruct(t, "d.yaml", src,
		code("a: 1", "", ""),
		code("# after a", "", ""),
		code("# about b\nb: 2", "", ""),
		code("- c", "", ""))
	for i, b := range doc.Parts[0].Blocks {
		if want := []string{"1", "1", "2", "3"}[i]; b.Attrs[AttrDocument] != want {
			t.Errorf("block %d document %q", i, b.Attrs[AttrDocument])
		}
	}
	// A key that is a collection is written as its text.
	x = strings.Repeat("z", dataUnitBytes)
	checkStruct(t, "k.yaml", "? [a, b]\n: 1\nc: "+x+"\n", code("? [a, b]\n: 1", `["[a, b]"]`, ""), code("c: "+x, "c", ""))
	// Text that is not YAML is read as lines.
	doc = checkStruct(t, "n.yaml", "a: [1\nb: 2\n", code("a: [1\nb: 2", "", ""))
	if doc.Metadata.Extra[ExtraYAMLSyntaxError] == "" {
		t.Error("no syntax error")
	}
}

func TestStructBudgetCut(t *testing.T) {
	var b strings.Builder
	b.WriteString("a: |\n")
	for i := range 100 {
		fmt.Fprintf(&b, "  line %d\n", i)
	}
	src := b.String()
	doc := dataDoc(t, "b.yaml", src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: gofastcore.BlockCost + 100}})
	bl := doc.Parts[0].Blocks
	if len(bl) != 1 || len(bl[0].Text) > 100 || !strings.HasPrefix(src, bl[0].Text+"\n") {
		t.Fatalf("%d blocks %+v", len(bl), bl)
	}
	w := csvWarnings(doc, gofastcore.WarnLimitExceeded)
	read := strings.Count(bl[0].Text, "\n") + 1
	if len(w) != 1 || !strings.HasSuffix(w[0], fmt.Sprintf("(%d lines not read)", 101-read)) {
		t.Errorf("%q", w)
	}
}

func TestDetectNewExtensions(t *testing.T) {
	for name, want := range map[string]string{"r.mdc": "md", "q.QMD": "md", "x.Rmd": "md", "t.jsonc": "json", "l.jsonlines": "json"} {
		if got := Detect(name, nil); got != want {
			t.Errorf("Detect(%q) = %q", name, got)
		}
	}
}

// Extractor.DataBlockBytes sets the size JSON and YAML are cut to.
func TestDataBlockBytes(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := range 40 {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, " \"k%02d\": \"%s\"", i, strings.Repeat("v", 90))
	}
	b.WriteString("\n}\n") // 40 members of about 100 bytes: 4 KiB
	src := b.String()
	blocks := func(n int) int {
		doc, err := (&Extractor{Format: "json", DataBlockBytes: n}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), &gofastcore.Options{})
		if err != nil || doc.Validate() != nil {
			t.Fatal(err)
		}
		return len(doc.Parts[0].Blocks)
	}
	// The default is 1 KiB: about ten members a block.
	if n := blocks(0); n != 4 && n != 5 {
		t.Errorf("default: %d blocks", n)
	}
	if n := blocks(-1); n != blocks(0) {
		t.Errorf("negative: %d blocks", n)
	}
	if n := blocks(8 << 10); n != 1 {
		t.Errorf("8 KiB: %d blocks", n)
	}
	if n := blocks(100); n != 40 {
		t.Errorf("100 bytes: %d blocks", n)
	}
}
