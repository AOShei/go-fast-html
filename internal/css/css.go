// Package css reads the declarations of an inline style attribute that
// decide whether text is seen: display, visibility, opacity, font-size,
// color and background, white-space, and the offsets and clipping that
// move or cut text out of view. Stylesheets are not read.
package css

import (
	"math"
	"strconv"
	"strings"
)

// RGBA is a color with alpha from 0 to 1.
type RGBA struct {
	R, G, B uint8
	A       float64
}

// FontSize is a font-size declaration: an absolute size in points, or a
// factor of the inherited size.
type FontSize struct {
	Set      bool
	Relative bool
	Pt       float64 // when absolute
	Factor   float64 // when relative
}

// Style holds what an inline style declares. A field is its zero value
// when the property is not declared.
type Style struct {
	Display    string // lowercased keyword
	Visibility string
	Opacity    float64
	OpacitySet bool
	FontSize   FontSize
	Color      RGBA
	ColorSet   bool
	Background RGBA
	BgSet      bool
	WhiteSpace string
	// OffPage: an offset, margin or text indent of 999 pixels or more
	// to the left or top moves the content out of view.
	OffPage bool
	// Clipped: a clip of nothing, or a box of at most one pixel with its
	// overflow hidden.
	Clipped bool
}

// MaxLen bounds the length of a style attribute that is read; the rest is
// ignored.
const MaxLen = 16 << 10

// Parse reads an inline style. A declaration marked !important wins over
// a later one that is not; otherwise the last one wins.
func Parse(s string) Style {
	if len(s) > MaxLen {
		s = s[:MaxLen]
	}
	s = stripComments(s)
	var st Style
	important := map[string]bool{}
	var width, height = -1.0, -1.0
	overflowHidden := false
	for _, decl := range splitDecls(s) {
		prop, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		prop = strings.ToLower(strings.TrimSpace(prop))
		val = strings.TrimSpace(val)
		imp := false
		if i := strings.LastIndexByte(val, '!'); i >= 0 && strings.EqualFold(strings.TrimSpace(val[i+1:]), "important") {
			val, imp = strings.TrimSpace(val[:i]), true
		}
		if important[prop] && !imp {
			continue
		}
		if imp {
			important[prop] = true
		}
		lv := strings.ToLower(val)
		switch prop {
		case "display":
			st.Display = lv
		case "visibility":
			st.Visibility = lv
		case "opacity":
			if v, ok := number(lv); ok {
				st.Opacity, st.OpacitySet = v, true
			}
		case "font-size":
			if fs, ok := fontSize(lv); ok {
				st.FontSize = fs
			}
		case "font":
			// The size is the token before the family, possibly with a
			// "/line-height".
			for _, tok := range strings.Fields(lv) {
				tok, _, _ = strings.Cut(tok, "/")
				if fs, ok := fontSize(tok); ok {
					st.FontSize = fs
					break
				}
			}
		case "color":
			if c, ok := ParseColor(lv); ok {
				st.Color, st.ColorSet = c, true
			}
		case "background-color":
			if c, ok := ParseColor(lv); ok {
				st.Background, st.BgSet = c, true
			}
		case "background":
			for _, tok := range colorTokens(lv) {
				if c, ok := ParseColor(tok); ok {
					st.Background, st.BgSet = c, true
				}
			}
		case "white-space":
			st.WhiteSpace = lv
		case "left", "top", "margin-left", "margin-top", "text-indent":
			if px, ok := Pixels(lv); ok && px <= -999 {
				st.OffPage = true
			}
		case "margin":
			for _, tok := range strings.Fields(lv) {
				if px, ok := Pixels(tok); ok && px <= -999 {
					st.OffPage = true
				}
			}
		case "clip":
			if isEmptyClip(lv) {
				st.Clipped = true
			}
		case "clip-path":
			if strings.HasPrefix(lv, "inset(50%") || strings.HasPrefix(lv, "inset(100%") || lv == "circle(0)" || strings.HasPrefix(lv, "circle(0px") || strings.HasPrefix(lv, "circle(0 ") {
				st.Clipped = true
			}
		case "width", "max-width":
			if px, ok := Pixels(lv); ok && (width < 0 || px < width) {
				width = px
			}
		case "height", "max-height":
			if px, ok := Pixels(lv); ok && (height < 0 || px < height) {
				height = px
			}
		case "overflow", "overflow-x", "overflow-y":
			if strings.HasPrefix(lv, "hidden") || strings.HasPrefix(lv, "clip") {
				overflowHidden = true
			}
		}
	}
	if overflowHidden && (width >= 0 && width <= 1 || height >= 0 && height <= 1) {
		st.Clipped = true
	}
	return st
}

func stripComments(s string) string {
	if !strings.Contains(s, "/*") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteByte(' ')
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return b.String()
		}
		s = s[i+2+j+2:]
	}
}

// splitDecls splits at semicolons outside parentheses and quotes.
func splitDecls(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case c == ';' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// colorTokens splits a shorthand value into tokens, keeping a function
// such as rgb(...) whole.
func colorTokens(s string) []string {
	var out []string
	depth, start := 0, -1
	for i := 0; i <= len(s); i++ {
		if i == len(s) || depth == 0 && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == ',' && depth == 0) {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
	}
	return out
}

func number(s string) (float64, bool) {
	pct := strings.HasSuffix(s, "%")
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	if pct {
		v /= 100
	}
	return v, true
}

// splitUnit splits "12.5px" into 12.5 and "px".
func splitUnit(s string) (float64, string, bool) {
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' || s[i] == '-' || s[i] == '+') {
		i++
	}
	if i == 0 {
		return 0, "", false
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, "", false
	}
	return v, s[i:], true
}

// Pixels converts a length to CSS pixels, taking em and rem as 16
// pixels. It reports false for a percentage, a keyword or a calc().
func Pixels(s string) (float64, bool) {
	v, unit, ok := splitUnit(s)
	if !ok {
		return 0, false
	}
	switch unit {
	case "", "px":
		if unit == "" && v != 0 {
			return 0, false
		}
		return v, true
	case "pt":
		return v * 4 / 3, true
	case "pc":
		return v * 16, true
	case "in":
		return v * 96, true
	case "cm":
		return v * 96 / 2.54, true
	case "mm":
		return v * 96 / 25.4, true
	case "q":
		return v * 96 / 101.6, true
	case "em", "rem", "ex", "ch":
		return v * 16, true
	case "vw", "vh", "vmin", "vmax":
		return v * 10, true // a small viewport of 1000 pixels
	}
	return 0, false
}

// fontSizeKeywords are the absolute size keywords in points (CSS Fonts 4
// with a medium of 16 pixels).
var fontSizeKeywords = map[string]float64{
	"xx-small": 6.75, "x-small": 7.5, "small": 9.75, "medium": 12,
	"large": 13.5, "x-large": 18, "xx-large": 24, "xxx-large": 36,
}

func fontSize(s string) (FontSize, bool) {
	if pt, ok := fontSizeKeywords[s]; ok {
		return FontSize{Set: true, Pt: pt}, true
	}
	switch s {
	case "smaller":
		return FontSize{Set: true, Relative: true, Factor: 0.83}, true
	case "larger":
		return FontSize{Set: true, Relative: true, Factor: 1.2}, true
	}
	v, unit, ok := splitUnit(s)
	if !ok || v < 0 {
		return FontSize{}, false
	}
	switch unit {
	case "%":
		return FontSize{Set: true, Relative: true, Factor: v / 100}, true
	case "em", "ex", "ch":
		return FontSize{Set: true, Relative: true, Factor: v}, true
	case "rem":
		return FontSize{Set: true, Pt: v * 12}, true
	}
	px, ok := Pixels(s)
	if !ok {
		return FontSize{}, false
	}
	return FontSize{Set: true, Pt: px * 0.75}, true
}

func isEmptyClip(s string) bool {
	s = strings.TrimSpace(s)
	inner, ok := strings.CutPrefix(s, "rect(")
	if !ok {
		return false
	}
	inner = strings.TrimSuffix(inner, ")")
	var vals []float64
	for _, f := range strings.FieldsFunc(inner, func(r rune) bool { return r == ',' || r == ' ' }) {
		px, ok := Pixels(f)
		if !ok {
			return false
		}
		vals = append(vals, px)
	}
	if len(vals) != 4 {
		return false
	}
	// rect(top, right, bottom, left): nothing shows when bottom <= top+1
	// or right <= left+1.
	return vals[2]-vals[0] <= 1 || vals[1]-vals[3] <= 1
}
