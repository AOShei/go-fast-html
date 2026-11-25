package gofasthtml

import (
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

func TestFrontMatterYAML(t *testing.T) {
	src := "---\ntitle: Release notes\nauthors:\n  - name: Ann\n  - Bob\ndescription: \"What changed\"\ntags: [go, pdf]\nkeywords: extraction\nlang: fr\ndate: 2026-01-15\nlastmod: 2026-02-01T10:00:00Z\nallowed-tools: [Read, Bash]\nmetadata:\n  version: \"1.2\"\n---\n\n# Hello\n\nBody.\n"
	doc := dataDoc(t, "p.md", src, nil)
	md := doc.Metadata
	if md.Title != "Release notes" || strings.Join(md.Authors, "|") != "Ann|Bob" || md.Language != "fr" ||
		strings.Join(md.Keywords, "|") != "go|pdf|extraction" || md.Extra["description"] != "What changed" ||
		!md.Created.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)) || !md.Modified.Equal(time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)) ||
		md.Extra["front_matter.allowed-tools"] != "Read, Bash" || md.Extra["front_matter.metadata.version"] != "1.2" {
		t.Errorf("metadata: %+v", md)
	}
	b := doc.Parts[0].Blocks
	if len(b) != 3 || b[0].Kind != gofastcore.KindCode || b[0].Attrs[AttrFrontMatter] != "yaml" || b[0].Attrs[gofastcore.AttrLanguage] != "yaml" ||
		!strings.HasPrefix(b[0].Text, "title: Release notes\n") || !strings.HasSuffix(b[0].Text, "  version: \"1.2\"") {
		t.Fatalf("blocks: %+v", b)
	}
	// The front matter's location covers it with its fences; the blocks
	// after it are where they were in the file.
	end := strings.Index(src[4:], "---\n") + 4 + 3
	if b[0].Loc.Offset != 0 || b[0].Loc.Length != int64(end) {
		t.Errorf("front matter location %+v, want 0, %d", b[0].Loc, end)
	}
	if b[1].Kind != gofastcore.KindHeading || b[1].Text != "Hello" || src[b[1].Loc.Offset:b[1].Loc.Offset+b[1].Loc.Length] != "# Hello" {
		t.Errorf("heading: %+v", b[1])
	}
}

func TestFrontMatterTOML(t *testing.T) {
	src := "+++\ntitle = \"Hugo post\"\ndate = 2024-03-01T10:00:00Z\ntags = [\"a\", 'b'] # c\ndraft = false\n[params]\nauthor = \"Me\"\n+++\nBody.\n"
	doc := dataDoc(t, "h.md", src, nil)
	md := doc.Metadata
	if md.Title != "Hugo post" || strings.Join(md.Keywords, "|") != "a|b" || md.Extra["front_matter.draft"] != "false" ||
		md.Extra["front_matter.params.author"] != "Me" || md.Created.IsZero() {
		t.Errorf("metadata: %+v", md)
	}
	if b := doc.Parts[0].Blocks; len(b) != 2 || b[0].Attrs[AttrFrontMatter] != "toml" || b[1].Text != "Body." {
		t.Errorf("blocks: %+v", b)
	}
}

func TestFrontMatterNot(t *testing.T) {
	for _, src := range []string{
		// A thematic break, then text that is not a mapping.
		"---\n\nA paragraph.\n\n---\nMore.\n",
		// Not closed.
		"---\ntitle: x\n\n# Heading\n",
		// Not at the start.
		"\n---\ntitle: x\n---\n",
		// A fence with text after it.
		"--- x\ntitle: y\n---\n",
	} {
		doc := dataDoc(t, "n.md", src, nil)
		for _, b := range doc.Parts[0].Blocks {
			if b.Attrs[AttrFrontMatter] != "" {
				t.Errorf("%q: front matter %q", src, b.Text)
			}
		}
		if doc.Metadata.Title != "" {
			t.Errorf("%q: title %q", src, doc.Metadata.Title)
		}
	}
	// Front matter is a Markdown thing: a notebook's Markdown cell has none.
	nb := `{"nbformat": 4, "nbformat_minor": 5, "metadata": {}, "cells": [{"cell_type": "markdown", "metadata": {}, "source": "---\ntitle: x\n---\nText"}]}`
	doc := dataDoc(t, "c.ipynb", nb, nil)
	if doc.Metadata.Title != "" {
		t.Errorf("notebook: title %q", doc.Metadata.Title)
	}
	// An empty front matter, and "..." closing one.
	doc = dataDoc(t, "e.md", "---\n---\nText.\n", nil)
	if b := doc.Parts[0].Blocks; len(b) != 1 || b[0].Text != "Text." {
		t.Errorf("empty: %+v", b)
	}
	doc = dataDoc(t, "f.md", "---\ntitle: T\n...\nText.\n", nil)
	if b := doc.Parts[0].Blocks; len(b) != 2 || b[0].Attrs[AttrFrontMatter] != "yaml" || b[1].Text != "Text." || doc.Metadata.Title != "T" {
		t.Errorf("closed by ...: %+v", b)
	}
}

func TestFrontMatterLoose(t *testing.T) {
	// Cursor's rules: globs that YAML reads as aliases.
	src := "---\ndescription: Rules for C++\nglobs: **/*.cpp, **/*.h\nalwaysApply: false\n---\n# C++\n"
	doc := dataDoc(t, "r.mdc", src, nil)
	md := doc.Metadata
	if md.Extra["description"] != "Rules for C++" || md.Extra["front_matter.globs"] != "**/*.cpp, **/*.h" || md.Extra["front_matter.alwaysApply"] != "false" {
		t.Errorf("metadata: %+v", md)
	}
	if w := csvWarnings(doc, gofastcore.WarnInputRepaired); len(w) != 1 || !strings.Contains(w[0], "line 3") {
		t.Errorf("warnings: %q", w)
	}
	if b := doc.Parts[0].Blocks; len(b) != 2 || b[0].Attrs[AttrFrontMatter] != "yaml" || b[1].Text != "C++" {
		t.Errorf("blocks: %+v", b)
	}
	// Lines that are not key: value: not front matter.
	doc = dataDoc(t, "s.md", "---\nnot: [a\njust words here\n---\nx\n", nil)
	for _, b := range doc.Parts[0].Blocks {
		if b.Attrs[AttrFrontMatter] != "" {
			t.Errorf("loose: front matter %q", b.Text)
		}
	}
}

func TestFrontMatterBounds(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\n")
	for i := range maxFrontMatterKeys + 10 {
		b.WriteString("k")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(strings.Repeat("y", i/26))
		b.WriteString(": v\n")
	}
	b.WriteString("long: " + strings.Repeat("z", MaxMetaValue+10) + "\n---\nText.\n")
	doc := dataDoc(t, "b.md", b.String(), nil)
	w := csvWarnings(doc, gofastcore.WarnLimitExceeded)
	if len(w) != 1 || !strings.Contains(w[0], "more than 256 values") {
		t.Errorf("warnings: %q", w)
	}
	n := 0
	for k := range doc.Metadata.Extra {
		if strings.HasPrefix(k, ExtraFrontMatterPrefix) {
			n++
		}
	}
	if n != maxFrontMatterKeys {
		t.Errorf("%d values in Extra", n)
	}
	// A long value is cut in the metadata, whole in the block.
	doc = dataDoc(t, "c.md", "---\ntitle: "+strings.Repeat("t", MaxMetaValue+10)+"\n---\n", nil)
	if len(doc.Metadata.Title) != MaxMetaValue || len(csvWarnings(doc, gofastcore.WarnLimitExceeded)) != 1 ||
		len(doc.Parts[0].Blocks[0].Text) != len("title: ")+MaxMetaValue+10 {
		t.Errorf("long title: %d", len(doc.Metadata.Title))
	}
}

// Nulls are no value; keys after an array of tables are read again from
// the next [table]; a key path past maxFrontMatterKey is left out.
func TestFrontMatterNullsTablesLongPaths(t *testing.T) {
	doc := dataDoc(t, "n.md", "---\ntags: [a, ~, null, b]\nother: NULL\nkept: \"null\"\n---\nBody\n", nil)
	md := doc.Metadata
	if strings.Join(md.Keywords, "|") != "a|b" || md.Extra["front_matter.other"] != "" || md.Extra["front_matter.kept"] != "null" {
		t.Errorf("nulls: %+v", md)
	}
	doc = dataDoc(t, "t.md", "+++\n[[resources]]\nsrc = \"x\"\n[params]\nsrc = \"y\"\n+++\nBody\n", nil)
	if md := doc.Metadata; md.Extra["front_matter.src"] != "" || md.Extra["front_matter.params.src"] != "y" {
		t.Errorf("toml: %+v", md.Extra)
	}
	long := strings.Repeat("k", maxFrontMatterKey)
	doc = dataDoc(t, "l.md", "---\n"+long+": 1\n"+long+":\n  a: 2\n---\nBody\n", nil)
	if md := doc.Metadata; md.Extra["front_matter."+long] != "1" || len(md.Extra) != 1 ||
		len(csvWarnings(doc, gofastcore.WarnLimitExceeded)) != 1 {
		t.Errorf("long paths: %d extra, %+v", len(md.Extra), doc.Warnings)
	}
}
