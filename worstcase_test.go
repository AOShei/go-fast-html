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
		"deep in cell":      "<table><tr><td>" + strings.Repeat("<div>", n) + "x",
		"deep in list":      "<ul><li>" + strings.Repeat("<span>", n) + "x",
		"style":             strings.Repeat(`<span style="color:#fff;background:rgb(1,2,3);font-size:1%">`, 2000) + "x",
		"select":            "<select>" + strings.Repeat("<option>x", n),
		"svg breakout":      strings.Repeat("<svg><p>", n),
		"nested blockquote": strings.Repeat("<blockquote>", n) + "x",
		"nested lists":      strings.Repeat("<ul><li>", n) + "x",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			doc, err := Extract(context.Background(), strings.NewReader(src), int64(len(src)),
				&gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 8 << 20}})
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("%d bytes took %v", len(src), d)
			}
		})
	}
}
