package charset

import (
	"math"
	"testing"
)

// r builds a string of code points, so that this file holds no Private
// Use Area or look-alike characters, literal or escaped.
func r(cps ...rune) string { return string(cps) }

// The WHATWG Shift_JIS decoder reads 0x80 as U+0080 and the user-defined
// area (lead bytes 0xF0 to 0xF9, pointers 8836 to 10715) as U+E000 to
// U+E757, which Microsoft's code page 932 also does. Without them a
// user-defined character with an ASCII trail byte became U+FFFD followed
// by a letter the author did not write.
func TestShiftJISUserDefined(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"\x80", r(0x80)},
		{"\xF0\x40", r(0xE000)},
		{"\xF0\x7E\xF0\x80", r(0xE03E, 0xE03F)},
		{"\xF9\xFC", r(0xE757)},
		{"\xF0\x7F", r(0xFFFD, 0x7F)},
	} {
		out, _, _, _ := Decode([]byte(c.src), Result{Enc: ShiftJIS}, math.MaxInt64)
		if string(out) != c.want {
			t.Errorf("%q: got %+q, want %+q", c.src, out, c.want)
		}
	}
}
