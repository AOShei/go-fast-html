package gofasthtml

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// Random JSON, JSON Lines and YAML with comments everywhere they may
// be, values long enough to be split, and runs of records: every comment
// is in a block.

// cgen writes random JSON: comments (C1, C2, ...) in every gap.
type cgen struct {
	r        *rand.Rand
	comments []string // each as a block holds it: a line comment with its line end
	sb       strings.Builder
}

func (g *cgen) gap() {
	switch g.r.Intn(6) {
	case 0:
		c := fmt.Sprintf("/*C%d*/", len(g.comments)+1)
		g.comments = append(g.comments, c)
		g.sb.WriteString(" " + c + " ")
	case 1:
		c := fmt.Sprintf("//C%d\n", len(g.comments)+1)
		g.comments = append(g.comments, c)
		g.sb.WriteString(" " + c)
	case 2:
		g.sb.WriteString("\n  ")
	default:
		g.sb.WriteString(" ")
	}
}

func (g *cgen) value(depth int) {
	k := g.r.Intn(5)
	if depth > 3 {
		k = 4
	}
	switch k {
	case 0, 1:
		g.sb.WriteString("{")
		n := g.r.Intn(5)
		for i := range n {
			g.gap()
			fmt.Fprintf(&g.sb, "\"k%d\":", i)
			g.gap()
			g.value(depth + 1)
			g.gap()
			if i < n-1 {
				g.sb.WriteString(",")
			}
		}
		g.gap()
		g.sb.WriteString("}")
	case 2:
		g.sb.WriteString("[")
		n := g.r.Intn(5)
		flat := g.r.Intn(2) == 0
		for i := range n {
			g.gap()
			if flat {
				fmt.Fprintf(&g.sb, "{\"a\": %d, \"b\": \"%s\"}", i, strings.Repeat("y", g.r.Intn(1500)))
			} else {
				g.value(depth + 1)
			}
			g.gap()
			if i < n-1 {
				g.sb.WriteString(",")
			}
		}
		g.gap()
		g.sb.WriteString("]")
	case 3:
		fmt.Fprintf(&g.sb, "%d", g.r.Intn(100))
	default:
		fmt.Fprintf(&g.sb, "\"%s\"", strings.Repeat("x", g.r.Intn(3000)))
	}
}

func TestCommentsKeptJSON(t *testing.T) {
	bad := 0 // only the first few are shown
	for seed := range 2000 {
		g := &cgen{r: rand.New(rand.NewSource(int64(seed)))}
		name := "p.json"
		g.gap()
		g.value(0)
		g.gap()
		if seed%2 == 1 {
			name = "p.jsonl"
			for range 2 {
				g.sb.WriteString("\n")
				g.value(0)
				g.gap()
			}
		}
		src := g.sb.String()
		doc := dataDoc(t, name, src, nil)
		if doc.Metadata.Extra[ExtraJSONSyntaxError] != "" {
			t.Fatalf("seed %d: %s", seed, doc.Metadata.Extra[ExtraJSONSyntaxError])
		}
		var all strings.Builder
		for _, b := range doc.Parts[0].Blocks {
			all.WriteString(b.Text)
			all.WriteString("\n") // a line comment at a block's end has no line end
		}
		got := all.String()
		for _, c := range g.comments {
			if !strings.Contains(got, c) {
				if bad < 8 {
					at := strings.Index(src, c)
					lo, hi := max(0, at-120), min(len(src), at+120)
					sh := strings.NewReplacer(strings.Repeat("x", 50), "x…", strings.Repeat("y", 50), "y…").Replace(src[lo:hi])
					t.Errorf("seed %d: %s lost near %q", seed, c, sh)
				}
				bad++
			}
		}
	}
}

// ygen writes random block YAML, with flow collections in it: comments
// (# C1, # C2, ...) at line ends and on lines of their own.
type ygen struct {
	r  *rand.Rand
	n  int
	sb strings.Builder
}

func (g *ygen) c() string { g.n++; return fmt.Sprintf("# C%d", g.n) }

func (g *ygen) lineEnd() {
	if g.r.Intn(3) == 0 {
		g.sb.WriteString("  " + g.c())
	}
	g.sb.WriteString("\n")
}

func (g *ygen) commentLines(ind int) {
	for g.r.Intn(4) == 0 {
		g.sb.WriteString(strings.Repeat(" ", g.r.Intn(ind+3)) + g.c() + "\n")
	}
}

func (g *ygen) scalar() string {
	switch g.r.Intn(4) {
	case 0:
		return fmt.Sprint(g.r.Intn(100))
	case 1:
		return `"q` + strings.Repeat("x", g.r.Intn(1500)) + `"`
	case 2:
		return "null"
	}
	return "p" + strings.Repeat("x", g.r.Intn(3000))
}

func (g *ygen) flow(depth, ind int) string {
	if depth > 2 || g.r.Intn(3) == 0 {
		return g.scalar()
	}
	var b strings.Builder
	obj := g.r.Intn(2) == 0
	if obj {
		b.WriteString("{")
	} else {
		b.WriteString("[")
	}
	n := g.r.Intn(4)
	for i := range n {
		if obj {
			fmt.Fprintf(&b, "k%d: ", i)
		}
		b.WriteString(g.flow(depth+1, ind))
		if i < n-1 {
			b.WriteString(",")
		}
		if g.r.Intn(4) == 0 {
			b.WriteString(" " + g.c() + "\n" + strings.Repeat(" ", ind+1))
		} else {
			b.WriteString(" ")
		}
	}
	if obj {
		b.WriteString("}")
	} else {
		b.WriteString("]")
	}
	return b.String()
}

// block writes a block node at indentation ind, after "key:" or "-".
func (g *ygen) block(ind, depth int) {
	k := g.r.Intn(5)
	if depth > 3 {
		k = 4
	}
	pad := strings.Repeat(" ", ind)
	switch k {
	case 0, 1: // mapping
		g.lineEnd()
		n := 1 + g.r.Intn(4)
		for i := range n {
			g.commentLines(ind)
			fmt.Fprintf(&g.sb, "%sk%d:", pad, i)
			g.block(ind+2, depth+1)
		}
	case 2: // sequence, maybe of records
		g.lineEnd()
		n := 1 + g.r.Intn(4)
		rec := g.r.Intn(2) == 0
		for range n {
			g.commentLines(ind)
			if rec {
				fmt.Fprintf(&g.sb, "%s- a: %d", pad, g.r.Intn(9))
				g.lineEnd()
				fmt.Fprintf(&g.sb, "%s  b: %s", pad, g.scalar())
				g.lineEnd()
				continue
			}
			fmt.Fprintf(&g.sb, "%s-", pad)
			g.block(ind+2, depth+1)
		}
	case 3:
		g.sb.WriteString(" " + g.flow(0, ind))
		g.lineEnd()
	default:
		g.sb.WriteString(" " + g.scalar())
		g.lineEnd()
	}
}

func TestCommentsKeptYAML(t *testing.T) {
	bad := 0
	for seed := range 2000 {
		g := &ygen{r: rand.New(rand.NewSource(int64(seed)))}
		g.commentLines(0)
		g.sb.WriteString("root:")
		g.block(2, 0)
		g.commentLines(0)
		src := g.sb.String()
		doc := dataDoc(t, "p.yaml", src, nil)
		if e := doc.Metadata.Extra[ExtraYAMLSyntaxError]; e != "" {
			if bad < 3 {
				t.Errorf("seed %d: %s\n%s", seed, e, strings.ReplaceAll(src, strings.Repeat("x", 50), "x…"))
			}
			bad++
			continue
		}
		var all strings.Builder
		for _, b := range doc.Parts[0].Blocks {
			all.WriteString(b.Text)
			all.WriteString("\n")
		}
		got := all.String()
		for i := 1; i <= g.n; i++ {
			c := fmt.Sprintf("# C%d\n", i)
			if !strings.Contains(got, c) {
				if bad < 8 {
					at := strings.Index(src, c)
					lo, hi := max(0, at-150), min(len(src), at+80)
					t.Errorf("seed %d: C%d lost near %q", seed, i, strings.ReplaceAll(src[lo:hi], strings.Repeat("x", 50), "x…"))
				}
				bad++
			}
		}
	}
}
