package main

import (
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
	if !strings.Contains(errs, "text.hidden") {
		t.Errorf("stderr %q", errs)
	}

	// Hidden text left out of rendered text on request.
	out, _, _ = cli(t, "", "-format", "text", "-exclude-hidden", filepath.Join(dir, "a.html"))
	if strings.Contains(out, "secret") || !strings.Contains(out, "Page") {
		t.Errorf("exclude-hidden: %q", out)
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
