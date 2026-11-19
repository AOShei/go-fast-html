package blocks

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/dom"
)

// The note a stored attribute's text becomes says so in Attrs: type
// "attribute", and the attribute's name under "attribute".
const (
	NoteAttribute = "attribute"
	AttrAttribute = "attribute"
)

// unshown reports the attributes whose text a page stores and does not
// show: what assistive technology reads (aria-label, aria-description,
// aria-roledescription, aria-placeholder), tooltips (title), the hint of
// an empty field (placeholder), and the alt text of an image map's area.
func unshown(attr, element string) bool {
	switch attr {
	case "aria-label", "aria-description", "aria-roledescription", "aria-placeholder", "title", "placeholder":
		return true
	case "alt":
		return element == "area"
	}
	return false
}

// attrNotes emits a note for each unshown attribute of n, flagged
// invisible, at n's place. A value that only repeats what the element
// shows (its visible text, or an image's alt text) is left out, so that
// the common case of a label equal to the text stays quiet. Elements
// that aria-labelledby or aria-describedby point to are not resolved:
// they are read where they stand.
func (w *walker) attrNotes(n *dom.Node) {
	if w.inHead {
		return
	}
	var seen []string
	for _, a := range n.Attrs {
		if !unshown(a.Name, n.Name) {
			continue
		}
		v := cleanProse(a.Value, true)
		if v == "" || contains(seen, v) {
			continue
		}
		seen = append(seen, v)
		if vis, ok := visibleText(n, len(v)); ok && vis == v {
			continue
		}
		if n.Name == "img" {
			if alt, ok := n.Attr("alt"); ok && cleanProse(alt, true) == v {
				continue
			}
		}
		w.later(gofastcore.Block{Kind: gofastcore.KindNote, Text: v,
			Hidden: n.Hidden | w.extra | gofastcore.HiddenInvisible,
			Attrs:  map[string]string{gofastcore.AttrNoteType: NoteAttribute, AttrAttribute: a.Name},
			Loc:    w.loc(a.ValStart, a.ValEnd, anchorOf(n))})
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

// visibleText returns the text under n that is not hidden, white space
// collapsed. It reads no more than a few times limit bytes, and reports
// false when there was more: the text is then longer than a value of
// limit bytes and cannot equal it.
func visibleText(n *dom.Node, limit int) (string, bool) {
	max := 4*limit + 64
	var out []byte
	over := false
	var walk func(*dom.Node)
	walk = func(p *dom.Node) {
		for c := p.FirstChild; c != nil && !over; c = c.Next {
			switch {
			case c.Type == dom.TextNode && c.Hidden == 0:
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
	return cleanProse(string(out), true), true
}
