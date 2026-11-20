package dom

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/css"
	"github.com/AOShei/go-fast-html/internal/lexer"
)

// state is what an element passes on to its content that decides whether
// text is seen.
type state struct {
	// sticky are reasons no descendant can undo: display:none, opacity 0,
	// moved out of view, clipped, the hidden attribute, template, ...
	sticky gofastcore.Hidden
	// vis is visibility:hidden, which a descendant can undo with
	// visibility:visible.
	vis bool
	// fontPt is the font size in points, as far as inline style says.
	fontPt float64
	// color is the text color and bg the background painted behind the
	// element, composited, when inline style or a legacy attribute sets
	// them.
	color, bg       css.RGBA
	colorSet, bgSet bool
	ws              WS
	closedDetails   bool // a <details> without open: its text is hidden but for its summary
}

const defaultFontPt = 12

var white = css.RGBA{R: 255, G: 255, B: 255, A: 1}

func rootState() state { return state{fontPt: defaultFontPt} }

// hidden returns the reasons text in this state is not seen.
func (s *state) hidden() gofastcore.Hidden {
	h := s.sticky
	if s.vis {
		h |= gofastcore.HiddenInvisible
	}
	if s.fontPt < gofastcore.HiddenTinySize {
		h |= gofastcore.HiddenTiny
	}
	if s.colorSet || s.bgSet {
		bg := white
		if s.bgSet {
			bg = s.bg
		}
		fg := css.RGBA{A: 1}
		if s.colorSet {
			fg = s.color
		}
		if s.colorSet && fg.A <= 0.01 {
			h |= gofastcore.HiddenTransparent
		} else {
			fg = css.Over(fg, bg)
			if gofastcore.LowContrast(fg.R, fg.G, fg.B, bg.R, bg.G, bg.B) {
				h |= gofastcore.HiddenLowContrast
			}
		}
	}
	return h
}

// textHidden is hidden for text directly inside the element.
func (s *state) textHidden() gofastcore.Hidden {
	h := s.hidden()
	if s.closedDetails {
		h |= gofastcore.HiddenInvisible
	}
	return h
}

// child returns the state of an element inside one in state s. style is
// the element's parsed style attribute, nil if it has none.
func (s state) child(name string, ns NS, attrs []lexer.Attr, style *css.Style) state {
	c := s
	c.closedDetails = false
	if s.closedDetails && name != "summary" {
		c.sticky |= gofastcore.HiddenInvisible
	}
	attr := func(k string) (string, bool) {
		for _, a := range attrs {
			if a.Name == k {
				return a.Value, true
			}
		}
		return "", false
	}
	if ns == HTML {
		switch name {
		// template, noscript, noembed, noframes, iframe fallback text,
		// script and style text, closed details and dialog, hidden
		// inputs: pages routinely hold such content and do not show it,
		// so it is not a signal and is not flagged (core's "What earns a
		// flag"). The block walker reads it, and flags only outliers.
		case "pre", "listing", "xmp", "plaintext", "textarea":
			c.ws = Pre
		case "font":
			if v, ok := attr("color"); ok {
				if col, ok := css.ParseColor(v); ok {
					c.color, c.colorSet = col, true
				}
			}
		}
		if v, ok := attr("bgcolor"); ok {
			if col, ok := css.ParseColor(v); ok && col.A > 0 {
				c.bg, c.bgSet = css.Over(col, c.background()), true
			}
		}
	}
	hiddenAttr := false
	if _, ok := attr("hidden"); ok {
		hiddenAttr = true
	}
	// aria-hidden hides content from screen readers only: a sighted
	// reader sees it, so it is not a reason here.
	if style != nil {
		st := style
		switch st.Display {
		case "":
		case "none":
			c.sticky |= gofastcore.HiddenInvisible
		default:
			// An author's display wins over the hidden attribute, which
			// is display:none in the browser's own stylesheet.
			hiddenAttr = false
		}
		switch st.Visibility {
		case "hidden", "collapse":
			c.vis = true
		case "visible":
			c.vis = false
		}
		if st.OpacitySet && st.Opacity <= 0.01 {
			c.sticky |= gofastcore.HiddenTransparent
		}
		if fs := st.FontSize; fs.Set {
			if fs.Relative {
				c.fontPt *= fs.Factor
			} else {
				c.fontPt = fs.Pt
			}
		}
		if st.ColorSet {
			c.color, c.colorSet = st.Color, true
		}
		if st.BgSet && st.Background.A > 0 {
			c.bg, c.bgSet = css.Over(st.Background, c.background()), true
		}
		switch st.WhiteSpace {
		case "pre", "pre-wrap", "break-spaces":
			c.ws = Pre
		case "pre-line":
			c.ws = PreLine
		case "normal", "nowrap":
			c.ws = Normal
		}
		if st.OffPage {
			c.sticky |= gofastcore.HiddenOffPage
		}
		if st.Clipped {
			c.sticky |= gofastcore.HiddenClipped
		}
	}
	if hiddenAttr {
		c.sticky |= gofastcore.HiddenInvisible
	}
	return c
}

func (s *state) background() css.RGBA {
	if s.bgSet {
		return s.bg
	}
	return white
}
