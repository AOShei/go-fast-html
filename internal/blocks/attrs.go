package blocks

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/dom"
)

// The note an attribute's text becomes says so in Attrs: type
// "attribute" (core's NoteAttribute), and the attribute's name under
// "attribute".
const (
	NoteAttribute = gofastcore.NoteAttribute
	AttrAttribute = "attribute"
	// AttrChecked is set on a list item that begins with a checkbox (a
	// task-list item): "true" when checked, "false" when not.
	AttrChecked = "checked"
)

// textAttr reports the attributes whose text a page keeps without drawing
// it in the flow of the page: what assistive technology reads
// (aria-label, aria-description, aria-roledescription, aria-placeholder),
// tooltips (title), the hint of an empty field (placeholder), and the alt
// text of an image map's area.
func textAttr(attr, element string) bool {
	switch attr {
	case "aria-label", "aria-description", "aria-roledescription", "aria-placeholder", "title", "placeholder":
		return true
	case "alt":
		return element == "area"
	}
	return false
}

// attrNotes emits a note for each attribute of n whose text the page
// keeps and does not draw (above), at n's place, with no display reason:
// the page declares nothing about its display. A value that only repeats
// the element's own text, or an image's alt text, is left out, since that
// text is extracted already. Elements that aria-labelledby or
// aria-describedby point to are not resolved: they are read where they
// stand.
func (w *walker) attrNotes(n *dom.Node) {
	if w.inHead {
		return
	}
	var seen []string
	for _, a := range n.Attrs {
		if !textAttr(a.Name, n.Name) {
			continue
		}
		v, ctl := cleanProse(a.Value, true)
		if v == "" || contains(seen, v) {
			continue
		}
		seen = append(seen, v)
		if own, ok := elementText(n, len(v)); ok && own == v {
			continue
		}
		if n.Name == "img" {
			if alt, ok := n.Attr("alt"); ok {
				if a, _ := cleanProse(alt, true); a == v {
					continue
				}
			}
		}
		w.res.Controls += ctl
		w.later(gofastcore.Block{Kind: gofastcore.KindNote, Text: v,
			Attrs: map[string]string{gofastcore.AttrNoteType: NoteAttribute, AttrAttribute: a.Name},
			Loc:   w.loc(a.ValStart, a.ValEnd, w.anchorOf(n))})
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// elementText returns the text under n, white space collapsed. It reads no
// more than a few times limit bytes, and reports false when there was
// more: the text is then longer than a value of limit bytes and cannot
// equal it.
func elementText(n *dom.Node, limit int) (string, bool) {
	max := 4*limit + 64
	var out []byte
	over := false
	var walk func(*dom.Node)
	walk = func(p *dom.Node) {
		for c := p.FirstChild; c != nil && !over; c = c.Next {
			switch {
			case c.Type == dom.TextNode:
				if len(out)+len(c.Text) > max {
					over = true
					return
				}
				out = append(out, c.Text...)
			case c.Type == dom.ElementNode:
				out = append(out, ' ')
				walk(c)
			}
		}
	}
	walk(n)
	if over {
		return "", false
	}
	t, _ := cleanProse(string(out), true)
	return t, true
}
