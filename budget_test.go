package gofasthtml

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

func TestTextBudget(t *testing.T) {
	src := strings.Repeat("<p>paragraph of text</p>", 1000)
	doc := extract(t, src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 10000}})
	p := doc.Parts[0]
	if !p.Has(gofastcore.WarnLimitExceeded) {
		t.Fatal("no limit.exceeded")
	}
	// Each block costs BlockCost and its 17 bytes.
	if n := len(p.Blocks); n != 10000/(gofastcore.BlockCost+17) {
		t.Errorf("%d blocks", n)
	}
	// The same cut whatever is asked for besides.
	doc2 := extract(t, src, &gofastcore.Options{Workers: 1, Limits: gofastcore.Limits{MaxTextBytes: 10000}})
	if len(doc2.Parts[0].Blocks) != len(p.Blocks) {
		t.Error("cut differs between runs")
	}
}

func TestTableBudget(t *testing.T) {
	// A small file declaring a huge grid is cut by rows.
	src := "<table>" + strings.Repeat("<tr><td colspan=1000>x", 1000)
	doc := extract(t, src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 1 << 20}})
	p := doc.Parts[0]
	if !p.Has(gofastcore.WarnLimitExceeded) {
		t.Fatal("no limit.exceeded")
	}
	rows := len(p.Blocks[0].Table.Rows)
	if rows == 0 || rows*1000*gofastcore.CellCost > 1<<20 {
		t.Errorf("%d rows kept", rows)
	}
}

// allocated runs f and returns the bytes it allocated.
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// A GFM header of 5000 columns over 5000 one-cell rows (35 KB) pads to
// 25 million cells. The padding must be bounded by the cell budget before
// it is made, not after.
func TestMarkdownWideTable(t *testing.T) {
	src := "|" + strings.Repeat("a|", 5000) + "\n|" + strings.Repeat("-|", 5000) + "\n" + strings.Repeat("|x\n", 5000)
	var doc *gofastcore.Document
	n := allocated(func() {
		doc = extractAs(t, "md", src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 8 << 20}})
	})
	if n > 256<<20 {
		t.Errorf("%d MiB allocated", n>>20)
	}
	p := doc.Parts[0]
	if !p.Has(gofastcore.WarnLimitExceeded) {
		t.Error("no limit.exceeded")
	}
	tb := p.Blocks[0].Table
	if tb == nil || len(tb.Rows[0]) != 5000 || len(tb.Rows) < 2 || tb.Rows[1][0] != "x" || tb.Rows[1][1] != "" {
		t.Fatalf("table %v", p.Blocks[0].Kind)
	}
	if len(tb.Rows)*5000*gofastcore.CellCost > 8<<20 {
		t.Errorf("%d rows kept", len(tb.Rows))
	}
}

// One row of 50,000 cells each spanning 1000 columns declares 50 million
// columns in 600 KB; the width is bounded by the cell budget as it grows.
func TestTableWideSpans(t *testing.T) {
	src := "<table><tr>" + strings.Repeat("<td colspan=1000>x", 50000) + strings.Repeat("<tr><td>y", 1000)
	var doc *gofastcore.Document
	n := allocated(func() {
		doc = extract(t, src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 8 << 20}})
	})
	if n > 256<<20 {
		t.Errorf("%d MiB allocated", n>>20)
	}
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Error("no limit.exceeded")
	}
}

func TestDecodeBudget(t *testing.T) {
	src := "<p>" + strings.Repeat("\xE9", 1000) // windows-1252: two bytes a character
	doc := extract(t, src, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 500}})
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Fatal("no limit.exceeded")
	}
	if n := len(doc.Parts[0].Blocks[0].Text); n > 500 {
		t.Errorf("%d bytes of text", n)
	}
}

func TestChildBudget(t *testing.T) {
	// A budget passed down from a parent document bounds this one.
	ctx, parent := gofastcore.WithTextBudget(context.Background(), gofastcore.Limits{MaxTextBytes: 1000})
	defer parent.Close()
	src := strings.Repeat("<p>text</p>", 100)
	doc, err := Extract(ctx, strings.NewReader(src), int64(len(src)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) || parent.Left() > 1000-gofastcore.BlockCost {
		t.Errorf("child not bounded by parent: %d left", parent.Left())
	}
}
