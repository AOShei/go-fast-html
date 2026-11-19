package css

import (
	"math"
	"strconv"
	"strings"
	"sync"
)

// namedColors is the CSS Color Module Level 4 list of named colors
// (section 6.1), as name and hex value.
const namedColors = `aliceblue f0f8ff antiquewhite faebd7 aqua 00ffff aquamarine 7fffd4
azure f0ffff beige f5f5dc bisque ffe4c4 black 000000 blanchedalmond ffebcd
blue 0000ff blueviolet 8a2be2 brown a52a2a burlywood deb887 cadetblue 5f9ea0
chartreuse 7fff00 chocolate d2691e coral ff7f50 cornflowerblue 6495ed
cornsilk fff8dc crimson dc143c cyan 00ffff darkblue 00008b darkcyan 008b8b
darkgoldenrod b8860b darkgray a9a9a9 darkgreen 006400 darkgrey a9a9a9
darkkhaki bdb76b darkmagenta 8b008b darkolivegreen 556b2f darkorange ff8c00
darkorchid 9932cc darkred 8b0000 darksalmon e9967a darkseagreen 8fbc8f
darkslateblue 483d8b darkslategray 2f4f4f darkslategrey 2f4f4f
darkturquoise 00ced1 darkviolet 9400d3 deeppink ff1493 deepskyblue 00bfff
dimgray 696969 dimgrey 696969 dodgerblue 1e90ff firebrick b22222
floralwhite fffaf0 forestgreen 228b22 fuchsia ff00ff gainsboro dcdcdc
ghostwhite f8f8ff gold ffd700 goldenrod daa520 gray 808080 green 008000
greenyellow adff2f grey 808080 honeydew f0fff0 hotpink ff69b4
indianred cd5c5c indigo 4b0082 ivory fffff0 khaki f0e68c lavender e6e6fa
lavenderblush fff0f5 lawngreen 7cfc00 lemonchiffon fffacd lightblue add8e6
lightcoral f08080 lightcyan e0ffff lightgoldenrodyellow fafad2
lightgray d3d3d3 lightgreen 90ee90 lightgrey d3d3d3 lightpink ffb6c1
lightsalmon ffa07a lightseagreen 20b2aa lightskyblue 87cefa
lightslategray 778899 lightslategrey 778899 lightsteelblue b0c4de
lightyellow ffffe0 lime 00ff00 limegreen 32cd32 linen faf0e6 magenta ff00ff
maroon 800000 mediumaquamarine 66cdaa mediumblue 0000cd mediumorchid ba55d3
mediumpurple 9370db mediumseagreen 3cb371 mediumslateblue 7b68ee
mediumspringgreen 00fa9a mediumturquoise 48d1cc mediumvioletred c71585
midnightblue 191970 mintcream f5fffa mistyrose ffe4e1 moccasin ffe4b5
navajowhite ffdead navy 000080 oldlace fdf5e6 olive 808000 olivedrab 6b8e23
orange ffa500 orangered ff4500 orchid da70d6 palegoldenrod eee8aa
palegreen 98fb98 paleturquoise afeeee palevioletred db7093 papayawhip ffefd5
peachpuff ffdab9 peru cd853f pink ffc0cb plum dda0dd powderblue b0e0e6
purple 800080 rebeccapurple 663399 red ff0000 rosybrown bc8f8f
royalblue 4169e1 saddlebrown 8b4513 salmon fa8072 sandybrown f4a460
seagreen 2e8b57 seashell fff5ee sienna a0522d silver c0c0c0 skyblue 87ceeb
slateblue 6a5acd slategray 708090 slategrey 708090 snow fffafa
springgreen 00ff7f steelblue 4682b4 tan d2b48c teal 008080 thistle d8bfd8
tomato ff6347 turquoise 40e0d0 violet ee82ee wheat f5deb3 white ffffff
whitesmoke f5f5f5 yellow ffff00 yellowgreen 9acd32`

var (
	namedOnce sync.Once
	named     map[string]RGBA
)

func loadNamed() {
	f := strings.Fields(namedColors)
	named = make(map[string]RGBA, len(f)/2+1)
	for i := 0; i+1 < len(f); i += 2 {
		c, _ := hexColor(f[i+1])
		named[f[i]] = c
	}
	named["transparent"] = RGBA{}
}

// ParseColor reads a CSS color: a name, #rgb, #rgba, #rrggbb, #rrggbbaa,
// rgb()/rgba() or hsl()/hsla(). Keywords whose value depends on context
// (currentcolor, inherit, system colors) report false.
func ParseColor(s string) (RGBA, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, "#") {
		return hexColor(s[1:])
	}
	if i := strings.IndexByte(s, '('); i > 0 && strings.HasSuffix(s, ")") {
		fn := s[:i]
		args := strings.FieldsFunc(s[i+1:len(s)-1], func(r rune) bool { return r == ',' || r == ' ' || r == '/' || r == '\t' })
		if len(args) < 3 || len(args) > 4 {
			return RGBA{}, false
		}
		alpha := 1.0
		if len(args) == 4 {
			a, ok := number(args[3])
			if !ok {
				return RGBA{}, false
			}
			alpha = clamp(a, 0, 1)
		}
		switch fn {
		case "rgb", "rgba":
			var ch [3]uint8
			for k := range 3 {
				v, ok := number(args[k])
				if !ok {
					return RGBA{}, false
				}
				if strings.HasSuffix(args[k], "%") {
					v *= 255
				}
				ch[k] = uint8(math.Round(clamp(v, 0, 255)))
			}
			return RGBA{ch[0], ch[1], ch[2], alpha}, true
		case "hsl", "hsla":
			h, err := strconv.ParseFloat(strings.TrimSuffix(args[0], "deg"), 64)
			sat, ok1 := number(args[1])
			lig, ok2 := number(args[2])
			if err != nil || !ok1 || !ok2 {
				return RGBA{}, false
			}
			r, g, b := hslToRGB(h, clamp(sat, 0, 1), clamp(lig, 0, 1))
			return RGBA{r, g, b, alpha}, true
		}
		return RGBA{}, false
	}
	namedOnce.Do(loadNamed)
	c, ok := named[s]
	return c, ok
}

func hexColor(h string) (RGBA, bool) {
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return RGBA{}, false
	}
	switch len(h) {
	case 3:
		return RGBA{uint8(v>>8&0xF) * 17, uint8(v>>4&0xF) * 17, uint8(v&0xF) * 17, 1}, true
	case 4:
		return RGBA{uint8(v>>12&0xF) * 17, uint8(v>>8&0xF) * 17, uint8(v>>4&0xF) * 17, float64(v&0xF) / 15}, true
	case 6:
		return RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 1}, true
	case 8:
		return RGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), float64(v&0xFF) / 255}, true
	}
	return RGBA{}, false
}

func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	h = math.Mod(math.Mod(h, 360)+360, 360) / 360
	f := func(n float64) uint8 {
		k := math.Mod(n+h*12, 12)
		a := s * math.Min(l, 1-l)
		return uint8(math.Round(255 * (l - a*math.Max(-1, math.Min(math.Min(k-3, 9-k), 1)))))
	}
	return f(0), f(8), f(4)
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Over composites c over the opaque background bg.
func Over(c, bg RGBA) RGBA {
	a := clamp(c.A, 0, 1)
	mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*a + float64(y)*(1-a))) }
	return RGBA{mix(c.R, bg.R), mix(c.G, bg.G), mix(c.B, bg.B), 1}
}
