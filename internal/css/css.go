// Package css reads the declarations of an inline style attribute that
// say how content is displayed: display, visibility, opacity, the alpha of
// the text colour, and white-space. Nothing is measured or compared:
// colours are read only for whether they are fully transparent. Stylesheets
// are not read.
package css

import (
	"math"
	"strconv"
	"strings"
)

// Style holds what an inline style declares. A field is its zero value
// when the property is not declared.
type Style struct {
	Display    string // lowercased keyword
	Visibility string
	// Opacity, clamped to 0..1 as CSS clamps it.
	Opacity    float64
	OpacitySet bool
	// ColorAlpha is the alpha of the declared text colour, 0 for
	// transparent. ColorSet is false when no colour is declared, or the
	// value inherits (inherit, unset, currentcolor) or is not a colour.
	ColorAlpha float64
	ColorSet   bool
	WhiteSpace string
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
				st.Opacity, st.OpacitySet = math.Max(0, math.Min(1, v)), true
			}
		case "color":
			if a, ok := Alpha(lv); ok {
				st.ColorAlpha, st.ColorSet = a, true
			} else if lv == "inherit" || lv == "unset" || lv == "currentcolor" {
				st.ColorSet = false
			}
		case "white-space":
			st.WhiteSpace = lv
		}
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

// number reads a number or a percentage, as a fraction.
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
