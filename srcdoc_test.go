package gofasthtml

import (
	"context"
	"html"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

func blockTexts(p gofastcore.Part) []string {
	var s []string
	for _, b := range p.Blocks {
		s = append(s, b.Text)
	}
	return s
}

// The document in an iframe's srcdoc is shown where the iframe stands:
// its blocks come in place, located at the iframe element.
func TestSrcdocInPlace(t *testing.T) {
	iframe := `<iframe id="f" srcdoc="<title>inner</title><h1>Inner</h1><p>inner text"></iframe>`
	src := `<title>Outer</title><p>before</p>` + iframe + `<p>after</p>`
	doc := extract(t, src, nil)
	p := doc.Parts[0]
	got := strings.Join(blockTexts(p), "|")
	if got != "before|Inner|inner text|after" {
		t.Fatalf("blocks %q", got)
	}
	if doc.Metadata.Title != "Outer" {
		t.Errorf("title %q", doc.Metadata.Title)
	}
	start := int64(strings.Index(src, "<iframe"))
	for _, b := range p.Blocks[1:3] {
		if b.Loc.Offset != start || b.Loc.Offset+b.Loc.Length != start+int64(len(iframe)) || b.Loc.Anchor != "f" {
			t.Errorf("%q at %+v, want offset %d length %d anchor f", b.Text, b.Loc, start, len(iframe))
		}
		if b.Display != 0 {
			t.Errorf("%q display %v", b.Text, b.Display)
		}
	}
	if p.Blocks[1].Kind != gofastcore.KindHeading {
		t.Errorf("kind %v", p.Blocks[1].Kind)
	}
	// An iframe with src only is read as before.
	doc = extract(t, `<p>a</p><iframe src="x.html">fallback</iframe><p>b</p>`, nil)
	if got := strings.Join(blockTexts(doc.Parts[0]), "|"); got != "a|fallback|b" {
		t.Errorf("src iframe: %q", got)
	}
}

// What the iframe declares applies to all its document; what the inner
// document declares applies inside.
func TestSrcdocDisplay(t *testing.T) {
	doc := extract(t, `<p>x</p><iframe hidden srcdoc="<p>in</p>"></iframe>`, nil)
	b := doc.Parts[0].Blocks[1]
	if b.Text != "in" || b.Display&gofastcore.DisplayHidden == 0 {
		t.Errorf("hidden iframe: %+v", b)
	}
	doc = extract(t, `<iframe style="display:none" srcdoc="<p>in</p>"></iframe>`, nil)
	if b := doc.Parts[0].Blocks[0]; b.Display&gofastcore.DisplayHidden == 0 {
		t.Errorf("display:none iframe: %+v", b)
	}
	doc = extract(t, `<iframe srcdoc="<p>shown</p><p hidden>inner hidden</p>"></iframe>`, nil)
	bl := doc.Parts[0].Blocks
	if len(bl) != 2 || bl[0].Display != 0 || bl[1].Display&gofastcore.DisplayHidden == 0 {
		t.Errorf("inner hidden: %+v", bl)
	}
}

// nestSrcdoc returns n iframes, each in the srcdoc of the one before,
// the innermost holding a paragraph.
func nestSrcdoc(n int) string {
	s := "<p>deep</p>"
	for range n {
		s = `<iframe srcdoc="` + html.EscapeString(s) + `"></iframe>`
	}
	return s
}

// Nesting is bounded by the depth limit, with core's limit warning.
func TestSrcdocDepth(t *testing.T) {
	src := "<p>top</p>" + nestSrcdoc(3)
	doc := extract(t, src, nil)
	if got := strings.Join(blockTexts(doc.Parts[0]), "|"); got != "top|deep" {
		t.Errorf("nested: %q", got)
	}
	// Each iframe is one level deeper: with a limit of 3 the first srcdoc
	// document is read and the second is at the limit.
	doc, err := (&Extractor{MaxDepth: 3}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Parts[0]
	if !p.Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("no limit.exceeded: %+v", p.Warnings)
	}
	if got := strings.Join(blockTexts(p), "|"); got != "top" {
		t.Errorf("at the limit: %q", got)
	}
	if err := doc.Validate(); err != nil {
		t.Error(err)
	}
}

// The srcdoc's text counts against the text and decode budgets.
func TestSrcdocBudget(t *testing.T) {
	big := `<iframe srcdoc="` + strings.Repeat("<p>word word word</p>", 5000) + `"></iframe>`
	doc := extract(t, big, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 4000}})
	p := doc.Parts[0]
	if !p.Has(gofastcore.WarnLimitExceeded) {
		t.Error("text budget: no limit.exceeded")
	}
	n := 0
	for _, b := range p.Blocks {
		n += len(b.Text)
	}
	if n > 4000 {
		t.Errorf("text budget: %d bytes", n)
	}
	doc = extract(t, big, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: int64(len(big)) + 1000}})
	p = doc.Parts[0]
	if !p.Has(gofastcore.WarnLimitExceeded) {
		t.Error("decode budget: no limit.exceeded")
	}
	if len(p.Blocks) > 100 {
		t.Errorf("decode budget: %d blocks", len(p.Blocks))
	}
}
