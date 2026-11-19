// Package charset decides the character encoding of an HTML document and
// decodes it to UTF-8, keeping a map from offsets in the decoded text back
// to offsets in the source bytes.
//
// The decision follows the WHATWG HTML standard's encoding sniffing, cut
// down to what this library decodes: a byte order mark, then a <meta>
// declaration found by the prescan of the first 1024 bytes, then UTF-8 if
// the whole input is valid UTF-8, otherwise windows-1252. Four encodings
// are decoded: UTF-8, UTF-16LE, UTF-16BE and windows-1252 (which the
// Encoding standard also uses for every ISO-8859-1 and US-ASCII label). A
// declaration of any other encoding is recorded and the input is read as
// windows-1252.
package charset

import (
	"bytes"
	"sort"
	"unicode/utf8"
)

// Encoding is one of the encodings this package decodes.
type Encoding uint8

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
	Windows1252
)

func (e Encoding) String() string {
	switch e {
	case UTF16LE:
		return "utf-16le"
	case UTF16BE:
		return "utf-16be"
	case Windows1252:
		return "windows-1252"
	}
	return "utf-8"
}

// How the encoding was decided.
const (
	FromBOM      = "bom"
	FromMeta     = "meta"
	FromDetected = "detected"
)

// Result is the outcome of Sniff.
type Result struct {
	Enc Encoding
	// From says how the encoding was decided: FromBOM, FromMeta or
	// FromDetected.
	From string
	// BOM is the length of the byte order mark, 0 if there is none.
	BOM int
	// Declared is the label a <meta> declaration gives, lowercased, held
	// to 64 bytes; empty if there was none.
	Declared string
	// Unsupported reports that Declared names an encoding this package
	// does not decode; Enc is then Windows1252.
	Unsupported bool
}

// Sniff decides the encoding of b.
func Sniff(b []byte) Result {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return Result{Enc: UTF8, From: FromBOM, BOM: 3}
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return Result{Enc: UTF16BE, From: FromBOM, BOM: 2}
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return Result{Enc: UTF16LE, From: FromBOM, BOM: 2}
	}
	if label, ok := prescan(b[:min(len(b), 1024)]); ok {
		r := Result{From: FromMeta, Declared: label}
		enc, known := Lookup(label)
		switch {
		case !known:
			r.Enc, r.Unsupported = Windows1252, true
		case enc == UTF16LE || enc == UTF16BE:
			// A document that can declare its encoding in ASCII is not
			// UTF-16; the standard reads the declaration as UTF-8.
			r.Enc = UTF8
		default:
			r.Enc = enc
		}
		return r
	}
	if utf8.Valid(b) {
		return Result{Enc: UTF8, From: FromDetected}
	}
	return Result{Enc: Windows1252, From: FromDetected}
}

// labels maps the WHATWG Encoding standard's labels of the encodings
// decoded here. x-user-defined is read as windows-1252, as the prescan
// prescribes.
var labels = map[string]Encoding{
	"unicode-1-1-utf-8": UTF8, "unicode11utf8": UTF8, "unicode20utf8": UTF8,
	"utf-8": UTF8, "utf8": UTF8, "x-unicode20utf8": UTF8,

	"csunicode": UTF16LE, "iso-10646-ucs-2": UTF16LE, "ucs-2": UTF16LE,
	"unicode": UTF16LE, "unicodefeff": UTF16LE, "utf-16": UTF16LE, "utf-16le": UTF16LE,
	"unicodefffe": UTF16BE, "utf-16be": UTF16BE,

	"ansi_x3.4-1968": Windows1252, "ascii": Windows1252, "cp1252": Windows1252,
	"cp819": Windows1252, "csisolatin1": Windows1252, "ibm819": Windows1252,
	"iso-8859-1": Windows1252, "iso-ir-100": Windows1252, "iso8859-1": Windows1252,
	"iso88591": Windows1252, "iso_8859-1": Windows1252, "iso_8859-1:1987": Windows1252,
	"l1": Windows1252, "latin1": Windows1252, "us-ascii": Windows1252,
	"windows-1252": Windows1252, "x-cp1252": Windows1252, "x-user-defined": Windows1252,
}

// Lookup returns the encoding a label names, if it is one decoded here.
// The label is matched after trimming ASCII whitespace and lowercasing.
func Lookup(label string) (Encoding, bool) {
	e, ok := labels[string(bytes.ToLower(bytes.Trim([]byte(label), "\t\n\f\r ")))]
	return e, ok
}

// Map takes offsets in the decoded text back to offsets in the source.
// It keeps a checkpoint every few kilobytes of decoded text and decodes
// forward from the nearest one, so it costs little memory whatever the
// input.
type Map struct {
	src      []byte
	enc      Encoding
	identity bool  // decoded text is the source after the BOM
	base     int64 // the BOM length
	cps      []checkpoint
}

type checkpoint struct{ dec, src int }

const checkpointEvery = 4096

// Source returns the offset in the source of the character that starts
// at or covers offset dec of the decoded text. An offset at the end of the
// decoded text gives the end of the source.
func (m *Map) Source(dec int) int64 {
	if m == nil {
		return int64(dec)
	}
	if m.identity {
		return m.base + int64(dec)
	}
	i := sort.Search(len(m.cps), func(i int) bool { return m.cps[i].dec > dec }) - 1
	if i < 0 {
		i = 0
	}
	d, s := m.cps[i].dec, m.cps[i].src
	for d < dec && s < len(m.src) {
		dn, sn := step(m.src, s, m.enc)
		if d+dn > dec {
			break
		}
		d, s = d+dn, s+sn
	}
	return int64(s)
}

// step decodes the character at src[s:] and returns how many bytes it
// becomes in UTF-8 and how many it takes in the source. It must agree with
// Decode.
func step(src []byte, s int, enc Encoding) (dec, n int) {
	switch enc {
	case Windows1252:
		return utf8.RuneLen(win1252(src[s])), 1
	case UTF16LE, UTF16BE:
		r, n := utf16At(src, s, enc == UTF16BE)
		return utf8.RuneLen(r), n
	}
	r, n := utf8.DecodeRune(src[s:])
	if r == utf8.RuneError && n <= 1 {
		return 3, 1
	}
	return n, n
}

// Decode decodes src, from which the BOM has not been removed, to UTF-8.
// It returns the text, the map back to src, and the number of byte
// sequences that could not be decoded and were replaced with U+FFFD.
//
// limit bounds the decoded size; Decode stops there and reports cut.
func Decode(src []byte, r Result, limit int64) (out []byte, m *Map, invalid int, cut bool) {
	m = &Map{src: src, enc: r.Enc, base: int64(r.BOM)}
	body := src[r.BOM:]
	if r.Enc == UTF8 && utf8.Valid(body) {
		m.identity = true
		if int64(len(body)) > limit {
			// Cut at a character boundary.
			n := int(limit)
			for n > 0 && !utf8.RuneStart(body[n]) {
				n--
			}
			return body[:n], m, 0, true
		}
		return body, m, 0, false
	}
	m.cps = append(m.cps, checkpoint{0, r.BOM})
	grow := len(body)
	if r.Enc == UTF16LE || r.Enc == UTF16BE {
		grow = len(body) / 2 * 3 / 2
	}
	out = make([]byte, 0, int(min(int64(grow)+8, limit)))
	last := 0
	for s := r.BOM; s < len(src); {
		if len(out)-last >= checkpointEvery {
			m.cps = append(m.cps, checkpoint{len(out), s})
			last = len(out)
		}
		var ch rune
		var n int
		switch r.Enc {
		case Windows1252:
			ch, n = win1252(src[s]), 1
		case UTF16LE, UTF16BE:
			ch, n = utf16At(src, s, r.Enc == UTF16BE)
			if ch == utf8.RuneError {
				invalid++
			}
		default:
			ch, n = utf8.DecodeRune(src[s:])
			if ch == utf8.RuneError && n <= 1 {
				n = 1
				invalid++
			}
		}
		if int64(len(out)+utf8.RuneLen(ch)) > limit {
			return out, m, invalid, true
		}
		out = utf8.AppendRune(out, ch)
		s += n
	}
	return out, m, invalid, false
}

// utf16At decodes the UTF-16 code unit, or surrogate pair, at src[s:].
// An unpaired surrogate or an odd final byte is U+FFFD.
func utf16At(src []byte, s int, be bool) (rune, int) {
	unit := func(i int) (rune, bool) {
		if i+1 >= len(src) {
			return 0, false
		}
		if be {
			return rune(src[i])<<8 | rune(src[i+1]), true
		}
		return rune(src[i+1])<<8 | rune(src[i]), true
	}
	u, ok := unit(s)
	if !ok {
		return utf8.RuneError, len(src) - s
	}
	switch {
	case u >= 0xD800 && u < 0xDC00:
		if v, ok := unit(s + 2); ok && v >= 0xDC00 && v < 0xE000 {
			return 0x10000 + (u-0xD800)<<10 + (v - 0xDC00), 4
		}
		return utf8.RuneError, 2
	case u >= 0xDC00 && u < 0xE000:
		return utf8.RuneError, 2
	}
	return u, 2
}

// win1252High is windows-1252 for bytes 0x80 to 0x9F, as the WHATWG
// Encoding standard's index-windows-1252 gives it. The five bytes it
// leaves undefined map to the C1 control of the same value.
var win1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

func win1252(b byte) rune {
	if b >= 0x80 && b < 0xA0 {
		return win1252High[b-0x80]
	}
	return rune(b)
}

// C1 returns the character a numeric character reference to a C1 control
// stands for, as the HTML standard's table gives it (the windows-1252
// character of that byte), and false for any other code point or for one
// of the five C1 values the table leaves alone.
func C1(r rune) (rune, bool) {
	if r < 0x80 || r >= 0xA0 {
		return 0, false
	}
	m := win1252High[r-0x80]
	return m, m != r
}
