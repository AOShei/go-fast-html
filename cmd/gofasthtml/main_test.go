package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

func cli(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errw bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(stdin), &out, &errw)
	return out.String(), errw.String(), code
}

func TestCommandEPUB(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range [][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container><rootfiles><rootfile full-path="c.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"c.opf", `<package version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>T</dc:title></metadata>
<manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/><item id="b" href="b.xhtml" media-type="application/xhtml+xml"/>
<item id="x" href="x.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="a"/><itemref idref="b"/></spine></package>`},
		{"a.xhtml", "<p>first chapter</p>"},
		{"b.xhtml", "<p>second chapter</p>"},
		{"x.xhtml", "<p>beside the spine</p>"},
	} {
		w, _ := zw.Create(f[0])
		w.Write([]byte(f[1]))
	}
	zw.Close()
	p := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errs, code := cli(t, "", p)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc gofastcore.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	if doc.Source.Format != "epub" || len(doc.Parts) != 2 || len(doc.Children) != 1 || len(doc.Children[0].Parts) != 1 {
		t.Errorf("document %s", out)
	}
	out, _, _ = cli(t, "", "-format", "text", "-parts", "2", p)
	if strings.TrimSpace(out) != "second chapter" {
		t.Errorf("-parts 2: %q", out)
	}

	// -part-markers puts a line before each chapter; without it there is none.
	out, _, _ = cli(t, "", "-format", "text", "-part-markers", p)
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) < 4 || !strings.HasPrefix(lines[0], "[") || !strings.Contains(out, "first chapter") || !strings.Contains(out, "second chapter") {
		t.Errorf("-part-markers: %q", out)
	}
	out, _, _ = cli(t, "", "-format", "text", "-children=false", p)
	if strings.Contains(out, "[") {
		t.Errorf("markers without -part-markers: %q", out)
	}

	// -children (default true): the document beside the spine follows the
	// chapters after a line naming it; -children=false leaves both out.
	out, _, _ = cli(t, "", "-format", "text", p)
	if !strings.Contains(out, "[child 1: x.xhtml]") || !strings.Contains(out, "beside the spine") ||
		strings.Index(out, "[child 1: x.xhtml]") > strings.Index(out, "beside the spine") {
		t.Errorf("children by default: %q", out)
	}
	out, _, _ = cli(t, "", "-format", "text", "-children=false", p)
	if strings.Contains(out, "[child") || strings.Contains(out, "beside the spine") || !strings.Contains(out, "second chapter") {
		t.Errorf("-children=false: %q", out)
	}
	out, _, _ = cli(t, "", "-format", "markdown", p)
	if !strings.Contains(out, "beside the spine") {
		t.Errorf("children in Markdown: %q", out)
	}
}

func TestCommandSubtitles(t *testing.T) {
	const srt = "1\n00:00:01,000 --> 00:00:02,000\n<i>Hello</i>\n\n2\n00:00:03,000 --> 00:00:04,000\nworld\n"
	const vtt = "WEBVTT\n\nNOTE n\n\nc1\n00:01.000 --> 00:02.000\n<v Ann>Hi &amp; bye\n"
	for _, tc := range []struct {
		stdin string
		args  []string
		want  []string
	}{
		{srt, []string{"-as", "srt", "-"}, []string{"Hello", "world"}},
		{vtt, []string{"-as", "vtt", "-"}, []string{"Hi & bye"}},
		{vtt, []string{"-"}, []string{"Hi & bye"}}, // by its signature
	} {
		out, errs, code := cli(t, tc.stdin, tc.args...)
		var doc gofastcore.Document
		if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || doc.Validate() != nil {
			t.Fatalf("%v: exit %d: %s %s", tc.args, code, errs, out)
		}
		var got []string
		for _, b := range doc.Parts[0].Blocks {
			if b.Attrs["start"] != "" {
				got = append(got, b.Text)
			}
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%v: %q", tc.args, got)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.srt")
	if err := os.WriteFile(path, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errs, code := cli(t, "", "-format", "text", dir)
	if code != 0 || !strings.Contains(out, "Hello\n\nworld") {
		t.Errorf("directory: exit %d: %s %q", code, errs, out)
	}
	out, errs, code = cli(t, "", path)
	var doc gofastcore.Document
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || doc.Source.Format != "srt" ||
		doc.Parts[0].Blocks[0].Attrs["start"] != "00:00:01.000" || doc.Parts[0].Blocks[0].Loc.Anchor != "1" {
		t.Errorf("a.srt: exit %d: %s %s", code, errs, out)
	}
}

func TestCommandCSV(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"a.csv": "x;y\n1;2\n", "b.tsv": "p\tq\n", "c.tab": "r\ts\n", "d.dat": "u,v\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, errs, code := cli(t, "", "-format", "markdown", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"| x | y |", "| p | q |", "| r | s |"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "| u |") || strings.Contains(out, "u,v") {
		t.Errorf("d.dat was read:\n%s", out)
	}
	out, errs, code = cli(t, "a;b,c\n", "-as", "csv", "-delimiter", ",", "-header", "-")
	var doc gofastcore.Document
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || doc.Validate() != nil {
		t.Fatalf("exit %d: %s %s", code, errs, out)
	}
	if tb := doc.Parts[0].Blocks[0].Table; tb.HeaderRows != 1 || len(tb.Rows[0]) != 2 || tb.Rows[0][0] != "a;b" || doc.Metadata.Extra["delimiter_source"] != "option" {
		t.Errorf("document %s", out)
	}
	for _, bad := range []string{`"`, "ab", "\n"} {
		if _, _, code := cli(t, "", "-delimiter", bad, "-"); code != 2 {
			t.Errorf("-delimiter %q: exit %d", bad, code)
		}
	}
}

func TestCommand(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.html":    "<h1>Page</h1><p hidden>secret</p>",
		"b.md":      "# Notes\n\n- one\n- two\n",
		"c.txt":     "plain\n\ntext",
		"sub/d.htm": "<p>deep</p>",
		"skip.bin":  "\x00\x01",
		"noext":     "<p>sniffed</p>",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// One file: indented JSON that decodes and validates.
	out, errs, code := cli(t, "", filepath.Join(dir, "b.md"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc gofastcore.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	if doc.Source.Format != "md" || len(doc.Parts[0].Blocks) != 3 || !strings.Contains(out, "\n  ") {
		t.Errorf("document %s", out)
	}

	// A directory: one line per document, sorted, only known extensions.
	out, errs, code = cli(t, "", "-format", "text", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"==> " + filepath.Join(dir, "a.html"), "Page", "secret", "Notes", "plain", "deep"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sniffed") || strings.Contains(out, "skip.bin") {
		t.Errorf("unexpected file read:\n%s", out)
	}
	if strings.Contains(errs, "text.hidden") {
		t.Errorf("stderr %q", errs)
	}

	// The stored part (here a hidden input's value) only with -stored.
	stored := filepath.Join(t.TempDir(), "form.html")
	if err := os.WriteFile(stored, []byte(`<p>Hi</p><form><input type="hidden" name="tok" value="kept-value"></form>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := cli(t, "", "-format", "text", stored); strings.Contains(out, "kept-value") {
		t.Errorf("stored part rendered without -stored: %q", out)
	}
	if out, _, _ := cli(t, "", "-format", "text", "-stored", stored); !strings.Contains(out, "kept-value") {
		t.Errorf("-stored: %q", out)
	}

	// Text declared not displayed left out of rendered text on request.
	for _, flag := range []string{"-exclude-display", "-exclude-display=hidden"} {
		out, _, _ = cli(t, "", "-format", "text", flag, filepath.Join(dir, "a.html"))
		if strings.Contains(out, "secret") || !strings.Contains(out, "Page") {
			t.Errorf("%s: %q", flag, out)
		}
	}
	out, _, _ = cli(t, "", "-format", "text", "-exclude-display=no_ink", filepath.Join(dir, "a.html"))
	if !strings.Contains(out, "secret") {
		t.Errorf("exclude-display=no_ink: %q", out)
	}
	if _, _, code := cli(t, "", "-exclude-display=tiny", filepath.Join(dir, "a.html")); code != 2 {
		t.Errorf("a removed reason accepted: exit %d", code)
	}
	for _, flag := range []string{"-exclude-display=all", "-exclude-display=true", "-exclude-display=hidden+no_ink", "-exclude-display=hidden,no_ink"} {
		out, _, code = cli(t, "", "-format", "text", flag, filepath.Join(dir, "a.html"))
		if code != 0 || strings.Contains(out, "secret") || !strings.Contains(out, "Page") {
			t.Errorf("%s: %d %q", flag, code, out)
		}
	}
	for _, flag := range []string{"-exclude-display=false", "-exclude-display="} {
		out, _, code = cli(t, "", "-format", "text", flag, filepath.Join(dir, "a.html"))
		if code != 0 || !strings.Contains(out, "secret") {
			t.Errorf("%s: %d %q", flag, code, out)
		}
	}
	for _, v := range []string{"unknown", "bogus"} {
		_, errs, code := cli(t, "", "-exclude-display="+v, filepath.Join(dir, "a.html"))
		if code != 2 || !strings.Contains(errs, "the reasons are hidden, no_ink") || strings.Contains(errs, "no_ink, unknown") {
			t.Errorf("-exclude-display=%s: exit %d, %q", v, code, errs)
		}
	}

	// The other shared render flags are accepted, and the defaults stay off.
	for _, flag := range []string{"-furniture", "-stored", "-part-markers"} {
		if out, errs, code := cli(t, "", "-format", "markdown", flag, filepath.Join(dir, "a.html")); code != 0 || !strings.Contains(out, "Page") {
			t.Errorf("%s: %d %q %q", flag, code, out, errs)
		}
	}

	// A file without a known extension is sniffed; -as overrides.
	out, _, _ = cli(t, "", "-format", "text", filepath.Join(dir, "noext"))
	if strings.TrimSpace(out) != "sniffed" {
		t.Errorf("sniffed: %q", out)
	}
	out, _, _ = cli(t, "", "-format", "text", "-as", "txt", filepath.Join(dir, "noext"))
	if strings.TrimSpace(out) != "<p>sniffed</p>" {
		t.Errorf("-as txt: %q", out)
	}

	// Standard input.
	out, _, code = cli(t, "# From stdin", "-as", "md", "-format", "markdown", "-")
	if code != 0 || strings.TrimSpace(out) != "# From stdin" {
		t.Errorf("stdin: %d %q", code, out)
	}

	// Errors.
	if _, _, code = cli(t, ""); code != 2 {
		t.Errorf("no args: %d", code)
	}
	if _, _, code = cli(t, "", "-format", "xml", dir); code != 2 {
		t.Errorf("bad format: %d", code)
	}
	if _, _, code = cli(t, "", "-as", "pdf", dir); code != 2 {
		t.Errorf("bad -as: %d", code)
	}
	if _, errs, code = cli(t, "", filepath.Join(dir, "missing.html")); code != 1 || errs == "" {
		t.Errorf("missing file: %d %q", code, errs)
	}
	if _, errs, code = cli(t, "", "-max-input-bytes", "3", filepath.Join(dir, "a.html")); code != 1 || !strings.Contains(errs, "limit") {
		t.Errorf("input limit: %d %q", code, errs)
	}
}
