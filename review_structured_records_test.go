package gofasthtml

import (
	"strings"
	"testing"
)

// TestReviewStructDuplicateKeyRecord: a record with a key twice is made
// a row whose cell for that key is the last value; the first value is in
// no block and no cell. structure.go table(): row[r.colOf[key]] = v
// overwrites.
func TestReviewStructDuplicateKeyRecord(t *testing.T) {
	src := `[{"a": "first", "a": "second"}, {"a": "third"}]`
	doc := reviewExtract(t, "d.json", src, nil)
	if all := reviewAllText(doc); !strings.Contains(all, "first") {
		t.Errorf("the value \"first\" is lost; the document holds:\n%s", all)
	}
}

// TestReviewStructTableDropsCommentAbove: the comments above a value (and
// the key line of a split container) that becomes a table are put in the
// table's Loc but in no text: a table has none. structure.go becomeSplit
// sets s.head, and table() consumes it with headed() for Loc only. Seen in
// the dev corpus: an Ansible tasks file's opening comment, the
// "sandwiches": [ key line of corpora-data files.
func TestReviewStructTableDropsCommentAbove(t *testing.T) {
	for _, c := range []struct{ name, src, comment string }{
		{"top.json", "// the users\n[{\"a\": 1}, {\"a\": 2}]\n", "the users"},
		{"top.yaml", "# deploys the servers\n- name: a\n  x: 1\n- name: b\n  x: 2\n", "deploys the servers"},
		{"nested.json", "{\n  // the users\n  \"users\": [\n    {\"a\": 1},\n    {\"a\": 2},\n    {\"b\": \"" + big(dataUnitBytes) + "\"}\n  ]\n}\n", "the users"},
	} {
		doc := reviewExtract(t, c.name, c.src, nil)
		if all := reviewAllText(doc); !strings.Contains(all, c.comment) {
			t.Errorf("%s: the comment %q is in no block", c.name, c.comment)
		}
	}
}

// TestReviewStructCommentBetweenRows: a comment line between two records
// of a table is in neither row (cells hold values only) nor in any block.
func TestReviewStructCommentBetweenRows(t *testing.T) {
	src := "[\n  {\"a\": 1},\n  // the second one is special\n  {\"a\": 2}\n]\n"
	doc := reviewExtract(t, "c.json", src, nil)
	if all := reviewAllText(doc); !strings.Contains(all, "the second one is special") {
		t.Errorf("the comment between rows is lost; the document holds:\n%s", all)
	}
}

// TestReviewStructCommentAfterLastMember: in a split container that is
// not the top-level value, a comment after its last member (before its
// closing bracket) is in no block: extendStart of the next member stops
// at the "]," line, and trailing() only looks after the last block.
func TestReviewStructCommentAfterLastMember(t *testing.T) {
	src := "{\n  \"a\": [\n    \"" + big(dataUnitBytes) + "\",\n    1\n    // the end of a\n  ],\n  \"b\": 1\n}\n"
	doc := reviewExtract(t, "e.json", src, nil)
	if all := reviewAllText(doc); !strings.Contains(all, "the end of a") {
		t.Errorf("the comment after a's last member is lost")
	}
}

// TestReviewYAMLCellDropsAnchorAndTag (SUSPECTED): a YAML record's cells
// are the scalar's value without its node properties, so an anchor that a
// later alias names, and a tag that types the value, are in no cell and
// no block: "- &base {a: !!str 1}" gives the cell "1" and "*base" refers
// to nothing visible. yamldata.go yamlCell ignores ev.Anchor/ev.Tag.
func TestReviewYAMLCellDropsAnchorAndTag(t *testing.T) {
	src := "- {name: &who ann, id: !!str 7}\n- {name: *who, id: 8}\n"
	doc := reviewExtract(t, "a.yaml", src, nil)
	all := reviewAllText(doc)
	if !strings.Contains(all, "&who") || !strings.Contains(all, "!!str") {
		t.Errorf("the anchor or tag is lost; the document holds:\n%s", all)
	}
}
