package epub

import (
	"context"
	"encoding/xml"
	"strings"

	"github.com/AOShei/go-fast-html/internal/dom"
)

// Entry is one entry of the table of contents.
type Entry struct {
	Title string
	Level int
	// Path is the member the entry points to, without its fragment;
	// empty when it points nowhere in the archive.
	Path string
}

// Bounds of a table of contents.
const (
	// MaxEntries bounds the entries read; the rest are left out.
	MaxEntries = 100_000
	// MaxLevel bounds how deeply the lists of a navigation document are
	// read; lists nested deeper are left out.
	MaxLevel = 64
	// MaxTitle bounds the length of an entry's title; the rest is cut
	// off.
	MaxTitle = 4 << 10
)

// Cut reports the bounds of a table of contents that were reached.
type Cut struct {
	Entries bool // there were more than MaxEntries entries
	Deep    bool // lists were nested deeper than MaxLevel
	Titles  int  // titles longer than MaxTitle, which were cut there
}

// NCX reads an EPUB 2 NCX document at ncxPath.
func NCX(ncxPath string, b []byte) ([]Entry, Cut) {
	var out []Entry
	var cut Cut
	var open []int // indexes in out of the navPoints open
	d := decoder(b)
	inText := false
	var text []byte
	textCut := false
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "navPoint":
				if len(out) >= MaxEntries {
					cut.Entries = true
					return out, cut
				}
				out = append(out, Entry{Level: len(open) + 1})
				open = append(open, len(out)-1)
			case "text":
				inText = len(open) > 0
				text, textCut = text[:0], false
			case "content":
				if len(open) > 0 {
					e := &out[open[len(open)-1]]
					if e.Path == "" {
						e.Path, _ = Resolve(ncxPath, attr(t, "src"))
					}
				}
			}
		case xml.CharData:
			if inText {
				var c bool
				text, c = appendCapped(text, t, MaxTitle)
				textCut = textCut || c
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "text":
				if inText {
					e := &out[open[len(open)-1]]
					if e.Title == "" {
						e.Title = string(text)
						if textCut {
							cut.Titles++
						}
					}
				}
				inText = false
			case "navPoint":
				if len(open) > 0 {
					open = open[:len(open)-1]
				}
			}
		}
	}
	return out, cut
}

// Nav reads the table of contents of an EPUB 3 navigation document at
// navPath: the nav element whose epub:type includes "toc".
func Nav(ctx context.Context, navPath string, b []byte) ([]Entry, Cut) {
	var cut Cut
	d, err := dom.Parse(ctx, b, dom.Options{XHTML: true})
	if err != nil {
		return nil, cut
	}
	nav := find(d.Root, func(n *dom.Node) bool {
		if n.Name != "nav" {
			return false
		}
		v, _ := n.Attr("epub:type")
		for _, f := range strings.Fields(v) {
			if f == "toc" {
				return true
			}
		}
		return false
	})
	if nav == nil {
		return nil, cut
	}
	var out []Entry
	var list func(ol *dom.Node, level int)
	list = func(ol *dom.Node, level int) {
		for li := ol.FirstChild; li != nil && !cut.Entries; li = li.Next {
			if li.Type != dom.ElementNode || li.Name != "li" {
				continue
			}
			if len(out) >= MaxEntries {
				cut.Entries = true
				return
			}
			var sub *dom.Node
			e := Entry{Level: level}
			for c := li.FirstChild; c != nil; c = c.Next {
				switch {
				case c.Type != dom.ElementNode:
				case (c.Name == "a" || c.Name == "span") && e.Title == "":
					var titleCut bool
					e.Title, titleCut = text(c)
					if titleCut {
						cut.Titles++
					}
					if href, ok := c.Attr("href"); ok {
						e.Path, _ = Resolve(navPath, href)
					}
				case c.Name == "ol" || c.Name == "ul":
					sub = c
				}
			}
			out = append(out, e)
			if sub != nil {
				if level < MaxLevel {
					list(sub, level+1)
				} else if hasItem(sub) {
					cut.Deep = true
				}
			}
		}
	}
	if ol := find(nav, func(n *dom.Node) bool { return n.Name == "ol" || n.Name == "ul" }); ol != nil {
		list(ol, 1)
	}
	return out, cut
}

func hasItem(ol *dom.Node) bool {
	for li := ol.FirstChild; li != nil; li = li.Next {
		if li.Type == dom.ElementNode && li.Name == "li" {
			return true
		}
	}
	return false
}

func find(n *dom.Node, match func(*dom.Node) bool) *dom.Node {
	for c := n.FirstChild; c != nil; c = c.Next {
		if c.Type != dom.ElementNode {
			continue
		}
		if match(c) {
			return c
		}
		if f := find(c, match); f != nil {
			return f
		}
	}
	return nil
}

// text returns the text of n, cut at MaxTitle bytes, and whether it was
// cut.
func text(n *dom.Node) (string, bool) {
	var b []byte
	cut := false
	var walk func(*dom.Node)
	walk = func(p *dom.Node) {
		for c := p.FirstChild; c != nil && !cut; c = c.Next {
			if c.Type == dom.TextNode {
				b, cut = appendCapped(b, c.Text, MaxTitle)
			} else if c.Type == dom.ElementNode {
				walk(c)
			}
		}
	}
	walk(n)
	return string(b), cut
}
