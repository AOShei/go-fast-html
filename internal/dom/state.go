package dom

import (
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/css"
	"github.com/AOShei/go-fast-html/internal/lexer"
)

// state is what an element passes on to its content about display, as
// the page declares it, and its white-space handling. Nothing is judged:
// no colour is compared, no size or position measured.
type state struct {
	// sticky are reasons no descendant can undo: the hidden attribute,
	// display:none, opacity:0, a template's inert content.
	sticky gofastcore.Display
	// vis is visibility:hidden or collapse, which a descendant undoes
	// with visibility:visible, as CSS has it.
	vis bool
	// clear is a text colour whose alpha is exactly 0 (color:
	// transparent), which a descendant undoes by declaring another
	// colour, since colour is inherited.
	clear bool
	ws    WS
}

func rootState() state { return state{} }

// display returns the reasons the page declares for text in this state.
func (s *state) display() gofastcore.Display {
	d := s.sticky
	if s.vis {
		d |= gofastcore.DisplayHidden
	}
	if s.clear {
		d |= gofastcore.DisplayNoInk
	}
	return d
}

// child returns the state of an element inside one in state s. style is
// the element's parsed style attribute, nil if it has none.
func (s state) child(name string, ns NS, attrs []lexer.Attr, style *css.Style) state {
	c := s
	attr := func(k string) (string, bool) {
		for _, a := range attrs {
			if a.Name == k {
				return a.Value, true
			}
		}
		return "", false
	}
	switch ns {
	case HTML:
		switch name {
		case "pre", "listing", "xmp", "plaintext", "textarea":
			c.ws = Pre
		case "template":
			// A template's content is inert by the element's definition.
			c.sticky |= gofastcore.DisplayHidden
		}
	case SVG:
		// SVG presentation attributes; inline style, read below, wins
		// over them as CSS has it.
		if v, ok := attr("display"); ok && keyword(v) == "none" {
			c.sticky |= gofastcore.DisplayHidden
		}
		if v, ok := attr("visibility"); ok {
			switch keyword(v) {
			case "hidden", "collapse":
				c.vis = true
			case "visible":
				c.vis = false
			}
		}
	}
	// The hidden attribute is HTML's (SVG and MathML do not define it).
	_, hiddenAttr := attr("hidden")
	hiddenAttr = hiddenAttr && ns == HTML
	if style != nil {
		switch style.Display {
		case "":
		case "none":
			c.sticky |= gofastcore.DisplayHidden
		default:
			// An author's display wins over the hidden attribute, which
			// is display:none in the browser's own stylesheet.
			hiddenAttr = false
		}
		switch style.Visibility {
		case "hidden", "collapse":
			c.vis = true
		case "visible":
			c.vis = false
		}
		if style.OpacitySet && style.Opacity == 0 {
			c.sticky |= gofastcore.DisplayNoInk
		}
		if style.ColorSet {
			c.clear = style.ColorAlpha == 0
		}
		switch style.WhiteSpace {
		case "pre", "pre-wrap", "break-spaces":
			c.ws = Pre
		case "pre-line":
			c.ws = PreLine
		case "normal", "nowrap":
			c.ws = Normal
		}
	}
	if hiddenAttr {
		c.sticky |= gofastcore.DisplayHidden
	}
	return c
}

func keyword(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
