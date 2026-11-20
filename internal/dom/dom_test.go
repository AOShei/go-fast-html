package dom

import (
	"context"
	"strings"
	"testing"
)

// render writes the tree as nested tags, text in quotes, hidden text
// marked with its reasons.
func render(n *Node, sb *strings.Builder) {
	for c := n.FirstChild; c != nil; c = c.Next {
		switch c.Type {
		case TextNode:
			sb.WriteString(`"` + string(c.Text) + `"`)
			if c.Display != 0 {
				sb.WriteString("{" + c.Display.String() + "}")
			}
		case CommentNode:
			sb.WriteString("<!" + string(c.Text) + ">")
		case ElementNode:
			sb.WriteString("<" + c.Name + ">")
			render(c, sb)
			sb.WriteString("</" + c.Name + ">")
		}
	}
}

func tree(t *testing.T, src string) string {
	t.Helper()
	d, err := Parse(context.Background(), []byte(src), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	render(d.Root, &sb)
	return sb.String()
}

func TestTree(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"<p>a<p>b", `<p>"a"</p><p>"b"</p>`},
		{"<ul><li>a<li>b</ul>", `<ul><li>"a"</li><li>"b"</li></ul>`},
		{"<dl><dt>a<dd>b<dt>c</dl>", `<dl><dt>"a"</dt><dd>"b"</dd><dt>"c"</dt></dl>`},
		{"<p>a<div>b</div>", `<p>"a"</p><div>"b"</div>`},
		{"<b>a<i>b</b>c</i>", `<b>"a"<i>"b"</i></b><i>"c"</i>`},
		{"<p><b>a</p><p>b", `<p><b>"a"</b></p><p><b>"b"</b></p>`},
		{"<b><p>a</b>b</p>", `<b><p>"a""b"</p></b>`},
		{"<a href=1>x<a href=2>y", `<a>"x"</a><a>"y"</a>`},
		{"<table><tr><td>a<td>b<tr><td>c</table>", `<table><tr><td>"a"</td><td>"b"</td></tr><tr><td>"c"</td></tr></table>`},
		{"<table><td>a</table>", `<table><tr><td>"a"</td></tr></table>`},
		{"<table>x<tr><td>a</table>", `"x"<table><tr><td>"a"</td></tr></table>`},
		{"<table><tr><div>x</div><td>a</table>", `<div>"x"</div><table><tr><td>"a"</td></tr></table>`},
		{"<select><option>a<option>b</select>", `<select><option>"a"</option><option>"b"</option></select>`},
		{"<h1>a<h2>b</h1>c", `<h1>"a"</h1><h2>"b"</h2>"c"`},
		{"<svg><title>t</title><p>x", `<svg><title>"t"</title></svg><p>"x"</p>`},
		{"<pre>\nx\n</pre>", "<pre>\"x\n\"</pre>"},
		{"<td>x</td>", `"x"`},
		{"<head><title>T</title></head>x", `<head><title>"T"</title></head>"x"`},
		// Declared: the hidden attribute, display, visibility, opacity 0,
		// a transparent colour, a template, SVG attributes.
		{"<p hidden>a<span style='display:block'>b</span></p>", `<p>"a"{hidden}<span>"b"{hidden}</span></p>`},
		{"<p hidden=until-found>a</p><p hidden style='display:block'>b</p>", `<p>"a"{hidden}</p><p>"b"</p>`},
		{"<div style='visibility:hidden'>a<b style='visibility:visible'>b</b></div>", `<div>"a"{hidden}<b>"b"</b></div>`},
		{"<div style='visibility:collapse'>a</div>", `<div>"a"{hidden}</div>`},
		{"<span style='opacity:0'>a<b style='opacity:1'>b</b></span><span style='opacity:0.01'>c</span>", `<span>"a"{no_ink}<b>"b"{no_ink}</b></span><span>"c"</span>`},
		{"<span style='color:transparent'>a<b style='color:red'>b</b></span><span style='color:rgba(0,0,0,0)'>c</span>", `<span>"a"{no_ink}<b>"b"</b></span><span>"c"{no_ink}</span>`},
		{"<span style='color:rgb(0 0 0 / 0.01)'>a</span><span style='color:transparent'><i style='color:inherit'>b</i></span>", `<span>"a"</span><span><i>"b"{no_ink}</i></span>`},
		{"<template>t</template><noscript>n</noscript>", `<template>"t"{hidden}</template><noscript>"n"</noscript>`},
		{"<svg><g display=none><text>a</text></g><text visibility=hidden>b<tspan visibility=visible>c</tspan></text></svg>", `<svg><g><text>"a"{hidden}</text></g><text>"b"{hidden}<tspan>"c"</tspan></text></svg>`},
		{"<svg><text hidden>a</text></svg>", `<svg><text>"a"</text></svg>`},
		// Not declared, so not reported: inferred from size, colour,
		// position or clipping, or shown on a condition.
		{"<span style='font-size:0'>a</span><span style='color:#fff'>b</span><font color=white>c</font>", `<span>"a"</span><span>"b"</span><font>"c"</font>`},
		{"<div style='position:absolute;left:-10000px'>a</div><div style='width:1px;height:1px;overflow:hidden'>b</div><div style='clip:rect(0 0 0 0)'>c</div>", `<div>"a"</div><div>"b"</div><div>"c"</div>`},
		{"<details><summary>s</summary>d</details><dialog>g</dialog><ruby>r<rp>(</rp></ruby>", `<details><summary>"s"</summary>"d"</details><dialog>"g"</dialog><ruby>"r"<rp>"("</rp></ruby>`},
		{"<div aria-hidden=true>a</div>", `<div>"a"</div>`},
		{"<div aria-hidden=true style='display:none'>a</div><p hidden aria-hidden=false>b</p>", `<div>"a"{hidden}</div><p>"b"{hidden}</p>`},
		{"<script>var a='<p>'</script><style>p{}</style>", `<script>"var a='<p>'"</script><style>"p{}"</style>`},
		{"a<!-- c -->b", `"a"<! c >"b"`},
	} {
		if got := tree(t, c.in); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestDepthLimit(t *testing.T) {
	src := strings.Repeat("<div>", 10000) + "<span hidden>secret</span>" + strings.Repeat("</div>", 10000) + "after"
	d, err := Parse(context.Background(), []byte(src), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Limited {
		t.Error("limit not reported")
	}
	var found bool
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		if depth > DefaultMaxDepth+2 {
			t.Fatalf("depth %d", depth)
		}
		for c := n.FirstChild; c != nil; c = c.Next {
			if c.Type == TextNode && string(c.Text) == "secret" {
				found = true
				if c.Display == 0 {
					t.Error("hidden text past the stack bound lost its flag")
				}
			}
			walk(c, depth+1)
		}
	}
	walk(d.Root, 0)
	if !found {
		t.Error("text lost")
	}
}

func TestDeepHiddenWithinStack(t *testing.T) {
	src := strings.Repeat("<div>", 1000) + "<span hidden>" + strings.Repeat("<b>", 10) + "secret"
	d, _ := Parse(context.Background(), []byte(src), Options{})
	var check func(n *Node)
	check = func(n *Node) {
		for c := n.FirstChild; c != nil; c = c.Next {
			if c.Type == TextNode && c.Display == 0 {
				t.Errorf("text %q not flagged", c.Text)
			}
			check(c)
		}
	}
	check(d.Root)
}

// Run with -fuzzminimizetime=2s: see FuzzExtract in the root package.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"<p>a<b>b<i>c</b>d</i>", "<table>x<tr><td>a<td>b</table>", "<svg><p>x</svg>",
		"<ul><li>a<li>b</ul>", "<select><option>a</select>", "<a><a><a>", "<b><p>a</b>b",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := Parse(context.Background(), []byte(s), Options{MaxDepth: 16, MaxStack: 32})
		if err != nil {
			t.Fatal(err)
		}
		var walk func(n *Node, depth int)
		walk = func(n *Node, depth int) {
			if depth > 20 {
				t.Fatalf("depth %d", depth)
			}
			for c := n.FirstChild; c != nil; c = c.Next {
				if c.Parent != n || c.Start > c.End || c.End > len(s) {
					t.Fatalf("bad node %+v", c)
				}
				walk(c, depth+1)
			}
		}
		walk(d.Root, 0)
	})
}
