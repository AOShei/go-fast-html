package gofasthtml

import (
	"fmt"
	"strings"
	"testing"
)

// TestReviewStructLongLineQuadratic: minified JSON (one line) costs time
// and memory quadratic in its length. For every block, extendEnd scans to
// the end of the line (lineAt) and jsonComment copies the rest of the
// line into a string; extendStart's lineStart scans back to the line's
// start. A 1 MiB line of small members allocates about 128 MB (2 MiB:
// 514 MB, 0.35 s; 64 MiB would be hours).
func TestReviewStructLongLineQuadratic(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; b.Len() < 1<<20; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"%d":1`, i)
	}
	b.WriteString("}")
	src := b.String()
	if n := reviewAlloc(t, "min.json", src); n > 24*uint64(len(src)) {
		t.Errorf("%d bytes of minified JSON allocate %d MB", len(src), n>>20)
	}
}

// TestReviewStructDeepPathMemory: framePath caches every open
// container's full path, each a new string, though paths over maxPath
// are never used: 500 levels of 1,000-byte keys (500 KB) allocate about
// 124 MB, quadratic in the depth.
func TestReviewStructDeepPathMemory(t *testing.T) {
	var b strings.Builder
	for i := range 500 {
		fmt.Fprintf(&b, `{"%04d%s":`, i, strings.Repeat("k", 996))
	}
	b.WriteString(`"` + strings.Repeat("x", 5000) + `"`)
	b.WriteString(strings.Repeat("}", 500))
	src := b.String()
	if n := reviewAlloc(t, "deep.json", src); n > 24*uint64(len(src)) {
		t.Errorf("%d bytes of nested JSON allocate %d MB", len(src), n>>20)
	}
}

// TestReviewFrontMatterDeepPathMemory: yamlFrontMatter builds each
// value's whole key path (o.path + "." + key) with no bound, though only
// maxFrontMatterKey bytes of it are kept: 60 levels of 1,000-byte keys
// and 256 values under them hold 256 paths of 60 KB each.
func TestReviewFrontMatterDeepPathMemory(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\n")
	for i := range 60 {
		fmt.Fprintf(&b, "%sk%03d%s:\n", strings.Repeat(" ", i), i, strings.Repeat("k", 996))
	}
	for i := range 256 {
		fmt.Fprintf(&b, "%sv%d: 1\n", strings.Repeat(" ", 60), i)
	}
	b.WriteString("---\nBody\n")
	src := b.String()
	if n := reviewAlloc(t, "deep.md", src); n > 24*uint64(len(src)) {
		t.Errorf("%d bytes of front matter allocate %d MB", len(src), n>>20)
	}
}

// TestReviewFrontMatterLooseQuadratic: looseFrontMatter appends each line
// under a key to its value with cur.items[0] += " " + t, copying the
// value every time: quadratic in the number of lines.
func TestReviewFrontMatterLooseQuadratic(t *testing.T) {
	// "Use when: x" is not YAML, so the front matter is read line by line.
	src := "---\ndescription: Use when: x\n" + strings.Repeat("  more text\n", 8000) + "---\nBody\n"
	if n := reviewAlloc(t, "loose.md", src); n > 64*uint64(len(src)) {
		t.Errorf("%d bytes of front matter allocate %d MB", len(src), n>>20)
	}
}
