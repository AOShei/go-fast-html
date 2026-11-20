package css

import (
	"math"
	"strconv"
	"strings"
	"sync"
)

// colorNames are the named colours of CSS Color Module Level 4 (section
// 6.1) and its system colours. Only whether a word is a colour is used:
// all of them are opaque.
const colorNames = `aliceblue antiquewhite aqua aquamarine azure beige bisque black
blanchedalmond blue blueviolet brown burlywood cadetblue chartreuse chocolate coral
cornflowerblue cornsilk crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen
darkgrey darkkhaki darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon
darkseagreen darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink
deepskyblue dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro
ghostwhite gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo
ivory khaki lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan
lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen
lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen linen
magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen
mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream
mistyrose moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid
palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum
powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown
seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen
steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow yellowgreen
accentcolor accentcolortext activetext buttonborder buttonface buttontext canvas canvastext
field fieldtext graytext highlight highlighttext linktext mark marktext selecteditem
selecteditemtext visitedtext initial revert revert-layer`

var (
	namesOnce sync.Once
	names     map[string]bool
)

// colorFuncs are the colour functions whose alpha follows a "/", or is
// the fourth argument in the legacy comma syntax.
var colorFuncs = map[string]bool{"rgb": true, "rgba": true, "hsl": true, "hsla": true, "hwb": true,
	"lab": true, "lch": true, "oklab": true, "oklch": true, "color": true}

// Alpha returns the alpha, from 0 to 1, of a CSS colour value, and false
// when the value is not a colour this reads (a keyword that inherits,
// color-mix(), a malformed value). "transparent" is 0; a named colour 1;
// #rgba and #rrggbbaa carry their alpha; a colour function its "/ alpha"
// or fourth argument.
func Alpha(s string) (float64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "transparent":
		return 0, true
	case strings.HasPrefix(s, "#"):
		h := s[1:]
		if _, err := strconv.ParseUint(h, 16, 32); err != nil {
			return 0, false
		}
		switch len(h) {
		case 3, 6:
			return 1, true
		case 4:
			v, _ := strconv.ParseUint(h[3:], 16, 8)
			return float64(v) / 15, true
		case 8:
			v, _ := strconv.ParseUint(h[6:], 16, 8)
			return float64(v) / 255, true
		}
		return 0, false
	}
	if i := strings.IndexByte(s, '('); i > 0 && strings.HasSuffix(s, ")") {
		if !colorFuncs[s[:i]] {
			return 0, false
		}
		args := s[i+1 : len(s)-1]
		var a string
		if j := strings.LastIndexByte(args, '/'); j >= 0 {
			a = strings.TrimSpace(args[j+1:])
		} else if parts := strings.Split(args, ","); len(parts) == 4 {
			a = strings.TrimSpace(parts[3])
		} else {
			return 1, true
		}
		v, ok := number(a)
		if !ok {
			return 0, false
		}
		return math.Max(0, math.Min(1, v)), true
	}
	namesOnce.Do(func() {
		names = map[string]bool{}
		for _, n := range strings.Fields(colorNames) {
			names[n] = true
		}
	})
	if names[s] {
		return 1, true
	}
	return 0, false
}
