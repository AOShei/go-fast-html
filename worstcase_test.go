package gofasthtml

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

func distinctAttrs(n int) string {
	var sb strings.Builder
	for i := range n {
		sb.WriteString("a" + strconv.Itoa(i) + "=b ")
	}
	return sb.String()
}

// TestWorstCase runs inputs built to cost much for their size and checks
// that each is read quickly and within its limits.
func TestWorstCase(t *testing.T) {
	const n = 50000
	cases := map[string]string{
		"deep divs":         strings.Repeat("<div>", n) + "x",
		"deep spans":        strings.Repeat("<span>", n) + "x",
		"deep formatting":   strings.Repeat("<b>", n) + "x",
		"reopened":          strings.Repeat("<b id=1><i id=2><u id=3><s id=4>", 16) + strings.Repeat("<p>x</p>", n),
		"end tags":          strings.Repeat("<div>", 400) + strings.Repeat("</p></li></x>", n),
		"nested anchors":    strings.Repeat("<a href=x>", n),
		"fostered":          "<table>" + strings.Repeat("x<b>y", n),
		"tables":            strings.Repeat("<table><tr><td>", n),
		"wide colspan":      "<table>" + strings.Repeat("<tr><td colspan=1000>x", 200),
		"rowspan":           "<table>" + strings.Repeat("<tr><td rowspan=0>x<td>y", 500),
		"list items":        "<ul>" + strings.Repeat("<li>", n),
		"comments":          strings.Repeat("<!--x-->", n),
		"entities":          strings.Repeat("&NotNestedGreaterGreater;", n),
		"attributes":        "<p " + strings.Repeat("a=b ", n) + ">x",
		"distinct attrs":    "<p " + distinctAttrs(n) + ">x",
		"reopened style":    `<b style="` + strings.Repeat("color:red;", 1600) + `">` + strings.Repeat("<p>x</p>", n),
		"breaks with spans": "<p>" + strings.Repeat("<b>x</b><br>", n),
		"deep in cell":      "<table><tr><td>" + strings.Repeat("<div>", n) + "x",
		"deep in list":      "<ul><li>" + strings.Repeat("<span>", n) + "x",
		"style":             strings.Repeat(`<span style="color:#fff;background:rgb(1,2,3);font-size:1%">`, 2000) + "x",
		"select":            "<select>" + strings.Repeat("<option>x", n),
		"svg breakout":      strings.Repeat("<svg><p>", n),
		"nested blockquote": strings.Repeat("<blockquote>", n) + "x",
		"nested lists":      strings.Repeat("<ul><li>", n) + "x",
	}
	// Markdown built to be costly; every case is read in every format.
	for k, v := range map[string]string{
		"md quotes":     strings.Repeat(">", n) + " x\n" + strings.Repeat(strings.Repeat("> ", 90)+"y\n", 2000),
		"md lists":      strings.Repeat(strings.Repeat("  ", 50)+"- x\n", 5000),
		"md emphasis":   strings.Repeat("*a _b ", n),
		"md closers":    strings.Repeat("a* ", n),
		"md brackets":   strings.Repeat("[", n) + strings.Repeat("](x)", n),
		"md images":     strings.Repeat("![", n) + "x",
		"md ticks":      backticks(3000),
		"md refdefs":    strings.Repeat("[a]: /b\n", n) + strings.Repeat("[a] ", n),
		"md links":      strings.Repeat("[a](b \"", n),
		"md html":       strings.Repeat("<a b='c' ", n),
		"md comments":   strings.Repeat("<!-- ", n),
		"md long para":  strings.Repeat("a\n", 4*n),
		"md long code":  "```\n" + strings.Repeat("a\n", 4*n),
		"md table":      "|a|b|\n|-|-|\n" + strings.Repeat("|x|y|\n", n),
		"md entities":   strings.Repeat("&#x10FFFF;&amp;&bogus;", n),
		"md fences":     strings.Repeat("```\n", n),
		"md setext":     strings.Repeat("a\n=\n", n),
		"titled nest":   strings.Repeat(`<span title="`+strings.Repeat("t", 4000)+`">`, 600) + strings.Repeat("x", 20000),
		"titles":        strings.Repeat(`<b aria-label="a" title="b" placeholder="c">x</b>`, n),
		"txt long line": strings.Repeat("word ", n*4),
		"txt blanks":    strings.Repeat("\n \n\f", n),
	} {
		cases[k] = v
	}
	for name, src := range cases {
		for _, format := range []string{"html", "md", "txt"} {
			t.Run(name+"/"+format, func(t *testing.T) {
				worstCase(t, format, src)
			})
		}
	}
}

func backticks(n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		sb.WriteString(strings.Repeat("`", i%200+1) + "a")
	}
	return sb.String()
}

func worstCase(t *testing.T, format, src string) {
	start := time.Now()
	doc, err := (&Extractor{Format: format}).Extract(context.Background(), strings.NewReader(src), int64(len(src)),
		&gofastcore.Options{Spans: true, Limits: gofastcore.Limits{MaxTextBytes: 8 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("%d bytes took %v", len(src), d)
	}
}
