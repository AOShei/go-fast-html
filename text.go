package gofasthtml

import (
	"context"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
)

// plainText splits decoded plain text into paragraphs at blank lines and
// form feeds. Line breaks inside a paragraph are kept as hard breaks
// ("\n"): a text file does not say whether its lines are wrapped prose or
// lines that mean something (an address, a verse, a list), and keeping
// them loses nothing. Trailing white space of each line and control
// characters other than tab are dropped, and ligatures written as their
// letters. It reports whether the text budget cut the part off.
func plainText(ctx context.Context, src []byte, source func(int) int64, budget *gofastcore.TextBudget, part *gofastcore.Part) bool {
	var buf []byte
	start, end := -1, 0
	flush := func() bool {
		text := trimSpace(buf)
		buf = buf[:0]
		if len(text) == 0 {
			start = -1
			return true
		}
		if !budget.Take(gofastcore.BlockCost + len(text)) {
			return false
		}
		off := source(start)
		part.Blocks = append(part.Blocks, gofastcore.Block{Kind: gofastcore.KindParagraph, Text: string(text),
			Loc: gofastcore.Location{Offset: off, Length: source(end) - off}})
		start = -1
		return true
	}
	for pos, n := 0, 0; pos < len(src); n++ {
		if n&1023 == 0 && ctx.Err() != nil {
			return false
		}
		// One line, without its ending.
		e := pos
		for e < len(src) && src[e] != '\n' && src[e] != '\r' && src[e] != '\f' {
			e++
		}
		line := src[pos:e]
		next := e
		ff := false
		if e < len(src) {
			ff = src[e] == '\f'
			next++
			if src[e] == '\r' && next < len(src) && src[next] == '\n' {
				next++
			}
		}
		if blankLine(line) {
			if !flush() {
				return true
			}
		} else {
			if start < 0 {
				start = pos
			} else {
				buf = append(buf, '\n')
			}
			buf = appendLine(buf, line)
			end = e
		}
		if ff && !flush() {
			return true
		}
		pos = next
	}
	return !flush()
}

func blankLine(b []byte) bool {
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if !unicode.IsSpace(r) && !unicode.IsControl(r) {
			return false
		}
		b = b[n:]
	}
	return true
}

// appendLine appends a line without its trailing white space, dropping
// control characters but tab and writing ligatures as letters.
func appendLine(dst, line []byte) []byte {
	from := len(dst)
	for len(line) > 0 {
		r, n := utf8.DecodeRune(line)
		switch {
		case r == '\t' || !unicode.IsControl(r):
			if l := gofastcore.Ligature(r); l != "" {
				dst = append(dst, l...)
			} else {
				dst = append(dst, line[:n]...)
			}
		}
		line = line[n:]
	}
	tail := dst[from:]
	for len(tail) > 0 {
		r, n := utf8.DecodeLastRune(tail)
		if !unicode.IsSpace(r) {
			break
		}
		tail = tail[:len(tail)-n]
	}
	return dst[:from+len(tail)]
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if !unicode.IsSpace(r) {
			break
		}
		b = b[n:]
	}
	for len(b) > 0 {
		r, n := utf8.DecodeLastRune(b)
		if !unicode.IsSpace(r) {
			break
		}
		b = b[:len(b)-n]
	}
	return b
}
