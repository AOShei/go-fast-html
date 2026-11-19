package lexer

import (
	"fmt"
	"strings"
	"testing"
)

// dump tokenizes src, switching to raw states after the elements that
// have them, as the tree builder does, and renders the tokens.
func dump(src string) string {
	l := New([]byte(src))
	var out []string
	for {
		t := l.Next()
		switch t.Kind {
		case EOF:
			return strings.Join(out, " ")
		case Text:
			if t.Data != "" {
				out = append(out, fmt.Sprintf("T%q", t.Data))
			}
		case StartTag:
			s := "<" + t.Name
			for _, a := range t.Attrs {
				s += fmt.Sprintf(" %s=%q", a.Name, a.Value)
			}
			if t.SelfClosing {
				s += "/"
			}
			out = append(out, s+">")
			switch t.Name {
			case "title", "textarea":
				l.SetRaw(RCDATA, t.Name)
			case "style", "xmp":
				l.SetRaw(RAWTEXT, t.Name)
			case "script":
				l.SetRaw(ScriptData, t.Name)
			case "plaintext":
				l.SetRaw(Plaintext, t.Name)
			}
		case EndTag:
			out = append(out, "</"+t.Name+">")
		case Comment:
			out = append(out, fmt.Sprintf("C%q", t.Data))
		case Doctype:
			out = append(out, "D")
		}
	}
}

func TestTokens(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a<b>c</b>", `T"a" <b> T"c" </b>`},
		{"<P CLASS=x ID='y' data-z=\"1 2\" bare>", `<p class="x" id="y" data-z="1 2" bare="">`},
		{"<a href=x href=y>", `<a href="x">`},
		{"<br/><br />", `<br/> <br/>`},
		{"<div/ class=x>", `<div class="x">`},
		{"a < b", `T"a < b"`},
		{"a <3", `T"a <3"`},
		{"x</>y", `T"x" T"y"`},
		{"x</", `T"x" T"</"`},
		{"<!-- c -->", `C" c "`},
		{"<!---->", `C""`},
		{"<!-->x", `C"" T"x"`},
		{"<!--->x", `C"" T"x"`},
		{"<!--a--!>b", `C"a" T"b"`},
		{"<!--a-- b-->", `C"a-- b"`},
		{"<!--open", `C"open"`},
		{"<?php x ?>y", `C"?php x ?" T"y"`},
		{"</ x>y", `C" x" T"y"`},
		{"<!DOCTYPE html><p>", `D <p>`},
		{"<![CDATA[x]]>", `C"[CDATA[x]]"`},
		{"<div title=\"x", ``},
		{"&amp;&lt;&gt;&quot;&#39;&#x41;&#X42;&#67", `T"&<>\"'ABC"`},
		{"&notin; &notit; &amp &ampx &foo; &", "T\"\u2209 \u00ACit; & &x &foo; &\""},
		{"&#0;&#x110000;&#xD800;&#128;&#x9F;", "T\"\uFFFD\uFFFD\uFFFD\u20AC\u0178\""},
		{"&#;&#x;", `T"&#;&#x;"`},
		{"<a href='?a=1&amp=2&copy=3&copy;'>", "<a href=\"?a=1&amp=2&copy=3\u00A9\">"},
		{"<a title=&lt>", `<a title="<">`},
		{"<title>a<b>&amp;</title>", `<title> T"a<b>&" </title>`},
		{"<textarea>x</textareax></textarea>", `<textarea> T"x</textareax>" </textarea>`},
		{"<style>p{}</p></style >", `<style> T"p{}</p>" </style>`},
		{"<script>a</b>&amp;</script>", `<script> T"a</b>&amp;" </script>`},
		{"<script><!--x</script>y", `<script> T"<!--x" </script> T"y"`},
		{"<script><!--<script>x</script>y-->z</script>w", `<script> T"<!--<script>x</script>y-->z" </script> T"w"`},
		{"<script><!--<script>x-->y</script>", `<script> T"<!--<script>x-->y" </script>`},
		{"<script>never closed", `<script> T"never closed"`},
		{"<plaintext></plaintext><b>", `<plaintext> T"</plaintext><b>"`},
		{"<xmp><b></xmp>", `<xmp> T"<b>" </xmp>`},
	} {
		if got := dump(c.in); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestCDATA(t *testing.T) {
	l := New([]byte("<![CDATA[a<b]]>c"))
	l.CDATA = true
	if tok := l.Next(); tok.Kind != Text || tok.Data != "a<b" {
		t.Errorf("got %+v", tok)
	}
}

func TestOffsets(t *testing.T) {
	src := "ab<p id=x>cd</p><!--e-->"
	l := New([]byte(src))
	var got []string
	for tok := l.Next(); tok.Kind != EOF; tok = l.Next() {
		got = append(got, src[tok.Start:tok.End])
	}
	want := []string{"ab", "<p id=x>", "cd", "</p>", "<!--e-->"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Run with -fuzzminimizetime=2s: see FuzzExtract in the root package.
func FuzzLexer(f *testing.F) {
	for _, s := range []string{
		"<p class=x>a&amp;b</p>", "<script><!--<script>x</script>-->", "<!--x--!>",
		"<title>&lt;</title>", "<![CDATA[x]]>", "&#x1F600;&notin", "<a b='c' d=\"e\" f=g/>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		l := New([]byte(s))
		l.CDATA = len(s)%2 == 0
		last := 0
		for n := 0; ; n++ {
			tok := l.Next()
			if tok.Start < last && tok.Kind != EOF || tok.End < tok.Start || tok.End > len(s) {
				t.Fatalf("token %d %+v out of order (last %d)", n, tok, last)
			}
			if tok.Kind == EOF {
				break
			}
			if l.Pos() <= last && n > 0 {
				t.Fatalf("no progress at %d", last)
			}
			last = l.Pos()
			if tok.Kind == StartTag && tok.Name == "script" {
				l.SetRaw(ScriptData, "script")
			}
		}
	})
}
