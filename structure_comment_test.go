package gofasthtml

import "testing"

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
