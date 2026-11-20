package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// TestCommandStoredKinds checks every row of the -stored table in
// COMMAND_FLAGS.md: markup comments are stored data, shown only when
// asked for, by -stored or -stored=markup_comment.
func TestCommandStoredKinds(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "p.html")
	src := "<html><head><style>p{color:kept-style}</style>" +
		"<script>var kept = 'kept-script';</script></head>" +
		"<body><p>Visible</p><!--[if mso]>kept-comment<![endif]--></body></html>"
	if err := os.WriteFile(page, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	type want struct{ comment, script, style bool }
	rows := []struct {
		args []string
		want want
	}{
		{nil, want{}},
		{[]string{"-stored"}, want{true, true, true}},
		{[]string{"-stored=true"}, want{true, true, true}},
		{[]string{"-stored=all"}, want{true, true, true}},
		{[]string{"-stored=false"}, want{}},
		{[]string{"-stored="}, want{}},
		{[]string{"-stored=markup_comment"}, want{comment: true}},
		{[]string{"-stored=macro"}, want{script: true}},
		{[]string{"-stored=stylesheet"}, want{style: true}},
		{[]string{"-stored=markup_comment,stylesheet"}, want{comment: true, style: true}},
		{[]string{"-stored=markup_comment+macro"}, want{comment: true, script: true}},
		{[]string{"-stored=field_code"}, want{}},
		{[]string{"-stored=manifest_item"}, want{}},
	}
	for _, format := range []string{"text", "markdown"} {
		for _, r := range rows {
			args := append(append([]string{"-format", format}, r.args...), page)
			out, errs, code := cli(t, "", args...)
			got := want{strings.Contains(out, "kept-comment"), strings.Contains(out, "kept-script"), strings.Contains(out, "kept-style")}
			if code != 0 || got != r.want || !strings.Contains(out, "Visible") {
				t.Errorf("%s %v: exit %d, got %+v want %+v\n%s%s", format, r.args, code, got, r.want, out, errs)
			}
		}
	}
	// JSON keeps the comment, as a note marked stored=markup_comment with
	// no note type.
	out, _, _ := cli(t, "", page)
	var doc gofastcore.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range doc.Parts {
		for _, b := range p.Blocks {
			if strings.Contains(b.Text, "kept-comment") {
				found = true
				if b.Kind != gofastcore.KindNote || b.Attrs[gofastcore.AttrStored] != gofastcore.StoredMarkupComment || b.Attrs[gofastcore.AttrNoteType] != "" {
					t.Errorf("comment block: %+v", b)
				}
			}
		}
	}
	if !found {
		t.Errorf("no comment block in JSON: %s", out)
	}
	// A name that is not a kind is an error listing the kinds.
	for _, v := range []string{"bogus", "markup_comment,bogus", "comment"} {
		_, errs, code := cli(t, "", "-stored="+v, page)
		if code != 2 || !strings.Contains(errs, "the kinds are") ||
			!strings.Contains(errs, "markup_comment") || !strings.Contains(errs, "manifest_item") || !strings.Contains(errs, "stylesheet") {
			t.Errorf("-stored=%s: exit %d, %q", v, code, errs)
		}
	}
}
