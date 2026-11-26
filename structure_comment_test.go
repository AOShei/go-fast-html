package gofasthtml

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// Comments around tables and at the end of split containers are kept,
// each in one block.
func TestStructCommentsKept(t *testing.T) {
	x := big(dataUnitBytes)
	// A comment between rows ends the run; the comment begins the next
	// run's text, the key line and the comments above the first.
	checkStruct(t, "r.json", "{\n  // the users\n  \"users\": [\n    {\"a\": 1},\n    {\"a\": 2},\n    // more\n    {\"a\": 3},\n    {\"a\": 4}, // four\n    {\"a\": 5},\n    \""+x+"\"\n  ]\n}\n",
		code("{\n  // the users\n  \"users\": [", "users", ""),
		table("a/1/2", "users[0]", "users[1]"),
		code("    // more", "users", ""),
		table("a/3/4", "users[2]", "users[3]"),
		code("// four\n    {\"a\": 5}", "users[4]", ""),
		code("    \""+x+"\"", "users[5]", ""))
	// A comment after the last member joins the last group, or is a block
	// of its own when the group is made.
	checkStruct(t, "e.json", "{\n  \"a\": [\n    \""+x+"\",\n    1\n    // the end of a\n  ],\n  \"b\": 1\n}\n",
		code("{\n  \"a\": [\n    \""+x+"\"", "a[0]", ""),
		code("    1\n    // the end of a", "a[1]", ""),
		code("  \"b\": 1", "b", ""))
	checkStruct(t, "e.yaml", "a:\n  - "+x+"\n  - 1\n  # the end of a\nb: 1\n",
		code("a:\n  - "+x, "a[0]", ""),
		code("  - 1\n  # the end of a", "a[1]", ""),
		code("b: 1", "b", ""))
	checkStruct(t, "y.yaml", "# deploys\n- name: a\n  x: 1\n- name: b\n  x: 2\n",
		code("# deploys", "", ""),
		table("name|x/a|1/b|2", "[0]", "[1]"))
}

// The comments the random tests found in no block: after a document's
// last value, after the last row of a top-level table, at the end of a
// YAML table's last row, and inside a record.
func TestStructCommentsAfterTables(t *testing.T) {
	x := big(dataUnitBytes)
	// After the closing bracket of a split top-level value: the comments
	// from the first to the end of the last, brackets between them.
	checkStruct(t, "a.json", "[\""+x+"\",\n 1 /* one */\n] /* end */\n// last\n",
		code("[\""+x+"\"", "[0]", ""),
		code(" 1 /* one */", "[1]", ""),
		code("/* end */\n// last", "", ""))
	// After the last row of a top-level array of records.
	checkStruct(t, "b.json", "[{\"a\": 1}, {\"a\": 2} /* two */ ] // end\n",
		table("a/1/2", "[0]", "[1]"),
		code("/* two */", "", ""),
		code("// end", "", ""))
	// At the end of the line of a YAML table's last row.
	checkStruct(t, "c.yaml", "- a: 1\n- a: 2  # two\n",
		table("a/1/2", "[0]", "[1]"),
		code("# two", "", ""))
	// A record with a comment inside is no row: the array is no table of
	// records, and is one block as written.
	checkStruct(t, "d.json", "[{\"a\": 1}, {\"a\": 2 /* two */}, {\"a\": 3}]",
		code("[{\"a\": 1}, {\"a\": 2 /* two */}, {\"a\": 3}]", "", ""))
	checkStruct(t, "e.yaml", "- a: 1\n- a: 2  # two\n  b: 3\n- a: 4\n",
		code("- a: 1\n- a: 2  # two\n  b: 3\n- a: 4", "", ""))
	// In a split array, it breaks the run.
	checkStruct(t, "g.json", "[\""+x+"\", {\"a\": 1}, {\"a\": 2 /* two */}, {\"a\": 3}, {\"a\": 4}]",
		code("[\""+x+"\"", "[0]", ""),
		code("{\"a\": 1}, {\"a\": 2 /* two */}", "[1]", "[2]"),
		table("a/3/4", "[3]", "[4]"))
	// A URL in a string is no comment.
	checkStruct(t, "f.json", `[{"u": "http://x"}, {"u": "/*y*/"}]`,
		table(`u/"http://x"/"/*y*/"`, "[0]", "[1]"))
}

// A JSON Lines value nested past the bound is kept as written, with
// limit.exceeded.
func TestJSONLinesTooDeep(t *testing.T) {
	deep := strings.Repeat("[", 600) + strings.Repeat("]", 600)
	doc := dataDoc(t, "d.jsonl", "{\"a\": 1}\n"+deep+"\n{\"a\": 2}\n"+deep+"\n", nil)
	w := csvWarnings(doc, gofastcore.WarnLimitExceeded)
	if len(w) != 1 || w[0] != "2 values nested deeper than 512: kept as written, not read by structure (the first at line 2, column 1)" {
		t.Errorf("warnings: %q", w)
	}
	var text strings.Builder
	for _, b := range doc.Parts[0].Blocks {
		text.WriteString(b.Text)
	}
	if strings.Count(text.String(), deep) != 2 {
		t.Errorf("blocks: %d bytes", text.Len())
	}
}
