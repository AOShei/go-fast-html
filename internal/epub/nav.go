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

// MaxEntries bounds the entries read from a table of contents.
const MaxEntries = 100_000

// NCX reads an EPUB 2 NCX document at ncxPath.
func NCX(ncxPath string, b []byte) []Entry {
	var out []Entry
	var open []int // indexes in out of the navPoints open
	d := decoder(b)
	inText := false
	var text strings.Builder
	for len(out) < MaxEntries {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "navPoint":
				out = append(out, Entry{Level: len(open) + 1})
				open = append(open, len(out)-1)
			case "text":
				inText = len(open) > 0
				text.Reset()
			case "content":
				if len(open) > 0 {
					e := &out[open[len(open)-1]]
					if e.Path == "" {
						e.Path, _ = Resolve(ncxPath, attr(t, "src"))
					}
				}
			}
		case xml.CharData:
			if inText && text.Len() < 1<<12 {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "text":
				if inText {
					e := &out[open[len(open)-1]]
					if e.Title == "" {
						e.Title = text.String()
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
	return out
}

// Nav reads the table of contents of an EPUB 3 navigation document at
// navPath: the nav element whose epub:type includes "toc".
func Nav(ctx context.Context, navPath string, b []byte) []Entry {
	d, err := dom.Parse(ctx, b, dom.Options{XHTML: true})
	if err != nil {
		return nil
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
		return nil
	}
	var out []Entry
	var list func(ol *dom.Node, level int)
	list = func(ol *dom.Node, level int) {
		for li := ol.FirstChild; li != nil && len(out) < MaxEntries; li = li.Next {
			if li.Type != dom.ElementNode || li.Name != "li" {
				continue
			}
			var sub *dom.Node
			e := Entry{Level: level}
			for c := li.FirstChild; c != nil; c = c.Next {
				switch {
				case c.Type != dom.ElementNode:
				case (c.Name == "a" || c.Name == "span") && e.Title == "":
					e.Title = text(c)
					if href, ok := c.Attr("href"); ok {
						e.Path, _ = Resolve(navPath, href)
					}
				case c.Name == "ol" || c.Name == "ul":
					sub = c
				}
			}
			out = append(out, e)
			if sub != nil && level < 64 {
				list(sub, level+1)
			}
		}
	}
	if ol := find(nav, func(n *dom.Node) bool { return n.Name == "ol" || n.Name == "ul" }); ol != nil {
		list(ol, 1)
	}
	return out
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

func text(n *dom.Node) string {
	var b strings.Builder
	var walk func(*dom.Node)
	walk = func(p *dom.Node) {
		for c := p.FirstChild; c != nil && b.Len() < 1<<12; c = c.Next {
			if c.Type == dom.TextNode {
				b.Write(c.Text)
			} else if c.Type == dom.ElementNode {
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}
