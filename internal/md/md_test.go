package md

import (
	"context"
	"math"
	"strings"
	"testing"
)

func render(t *testing.T, src string) string {
	t.Helper()
	d, err := Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	h := d.Render(math.MaxInt)
	// Compare without the line breaks between blocks.
	return strings.ReplaceAll(string(h.B), "\n", "")
}

// Cases from the CommonMark 0.31 spec and GFM, rendered without line
// breaks.
func TestRender(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"# Foo\n## Bar ##\n#Baz", "<h1>Foo</h1><h2>Bar</h2><p>#Baz</p>"},
		{"Foo\n===\nBar\n---", "<h1>Foo</h1><h2>Bar</h2>"},
		{"a\nb\n\nc", "<p>ab</p><p>c</p>"},
		{"***\n- - -", "<hr /><hr />"},
		{"> a\n> b\nc", "<blockquote><p>abc</p></blockquote>"},
		{"- a\n- b\n\n- c", "<ul><li><p>a</p></li><li><p>b</p></li><li><p>c</p></li></ul>"},
		{"- a\n  - b\n- c", "<ul><li>a<ul><li>b</li></ul></li><li>c</li></ul>"},
		{"3. a\n4. b", `<ol start="3"><li>a</li><li>b</li></ol>`},
		{"- a\n* b", "<ul><li>a</li></ul><ul><li>b</li></ul>"},
		{"```go\nx := 1\n\n```", `<pre><code class="language-go">x := 1</code></pre>`},
		{"    code\n      more", "<pre><code>code  more</code></pre>"},
		{"~~~\n<b>\n~~~", "<pre><code>&lt;b&gt;</code></pre>"},
		{"<div hidden>\n*x*\n</div>\n\n*y*", "<div hidden>*x*</div><p><em>y</em></p>"},
		{"<!-- c -->\nx", "<!-- c --><p>x</p>"},
		{"*a* **b** _c_ __d__ ***e***", "<p><em>a</em> <strong>b</strong> <em>c</em> <strong>d</strong> <em><strong>e</strong></em></p>"},
		{"foo_bar_ *a **b** c*", "<p>foo_bar_ <em>a <strong>b</strong> c</em></p>"},
		{"`a ` b` ``c``", "<p><code>a </code> b` <code>c</code></p>"},
		{"[a](/u \"t\") ![i](/p.png)", `<p><a href="/u" title="t">a</a> <img src="/p.png" alt="i" /></p>`},
		{"[x][r] [r]\n\n[r]: /dest", `<p><a href="/dest">x</a> <a href="/dest">r</a></p>`},
		{"<http://a.b/c> <me@x.org>", `<p><a href="http://a.b/c">http://a.b/c</a> <a href="mailto:me@x.org">me@x.org</a></p>`},
		{"a  \nb\\\nc", "<p>a<br />b<br />c</p>"},
		{"a <span style=\"display:none\">b</span> &amp; &copy; &#65; \\*", `<p>a <span style="display:none">b</span> &amp; ` + "\u00a9" + ` A *</p>`},
		{"| a | b |\n|---|:-:|\n| 1 | 2 \\| 3 |", "<table><thead><tr><th>a</th><th>b</th></tr></thead><tbody><tr><td>1</td><td>2 | 3</td></tr></tbody></table>"},
		{"[a [b](/1)](/2)", `<p>[a <a href="/1">b</a>](/2)</p>`},
		{"-\t\tx", "<ul><li><pre><code>  x</code></pre></li></ul>"},
		{"- \tx", "<ul><li>x</li></ul>"},
		{"1. a\n\n   b\n2. c", "<ol><li><p>a</p><p>b</p></li><li><p>c</p></li></ol>"},
		{"- a\n\n  b", "<ul><li><p>a</p><p>b</p></li></ul>"},
		{"[r]: /d \"t\"\n[r]", `<p><a href="/d" title="t">r</a></p>`},
		{"> - a\n>\n> b", "<blockquote><ul><li>a</li></ul><p>b</p></blockquote>"},
	} {
		if got := render(t, c.in); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestSourceMap(t *testing.T) {
	src := "# Head\n\npara one\nline two\n\n<div>\n<p id=x>raw</p>\n</div>\n"
	d, _ := Parse(context.Background(), src)
	h := d.Render(math.MaxInt)
	html := string(h.B)
	at := func(sub string) (int, int) {
		i := strings.Index(html, sub)
		return h.Source(i), h.Source(i + len(sub))
	}
	if s, e := at("<h1>Head</h1>"); src[s:e] != "# Head" {
		t.Errorf("heading maps to %q", src[s:e])
	}
	if s, e := at("<p>para one\nline two</p>"); src[s:e] != "para one\nline two" {
		t.Errorf("paragraph maps to %q", src[s:e])
	}
	if s, e := at("<p id=x>raw</p>"); src[s:e] != "<p id=x>raw</p>" {
		t.Errorf("raw HTML maps to %q", src[s:e])
	}
}

func TestDeep(t *testing.T) {
	src := strings.Repeat("> ", 1000) + "x\n" + strings.Repeat("- ", 1000) + "y\n" +
		strings.Repeat("*a ", 20000) + strings.Repeat("[", 20000) + strings.Repeat("`", 3000)
	d, err := Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	d.Render(math.MaxInt)
}

// Run with -fuzzminimizetime=2s: see FuzzExtract in the root package.
func FuzzMarkdown(f *testing.F) {
	for _, s := range []string{
		"# a\n\n- b\n  - c\n\n> d *e* **f**\n", "```\nx\n```\n", "[a]: /b\n[a] ![c](d)",
		"| a |\n|---|\n| b |", "<div>\nx\n</div>", "a  \nb\\\nc <x@y.z> `c`", "1. a\n\n   b\n2) c",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := Parse(context.Background(), s)
		if err != nil {
			t.Fatal(err)
		}
		h := d.Render(1 << 20)
		prev := -1
		for i := 0; i <= len(h.B); i += 7 {
			m := h.Source(i)
			if m < 0 || m > len(s) {
				t.Fatalf("Source(%d) = %d outside %d", i, m, len(s))
			}
			_ = prev
			prev = m
		}
	})
}
