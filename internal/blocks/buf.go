package blocks

import (
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/dom"
)

// run is a stretch of the text with the display reasons declared for it.
type run struct {
	start, end int
	h          gofastcore.Display
}

// buf collects the text of one block.
type buf struct {
	b       []byte
	runs    []run
	spans   []gofastcore.Span  // styles and links
	pending bool               // collapsed white space waiting for a following character
	from    int                // where the text being added starts
	cur     gofastcore.Display // and its display reasons
	// start and end are the block's range in the decoded input; start is
	// -1 while nothing has been added.
	start, end int
	anchor     string
	href       string // the first link, for a table cell
}

func newBuf() *buf { return &buf{start: -1} }

func (b *buf) reset() {
	b.b = b.b[:0]
	b.runs = b.runs[:0]
	b.spans = b.spans[:0]
	b.pending = false
	b.start, b.end = -1, 0
	b.anchor, b.href = "", ""
}

// cover widens the block's source range to [start, end).
func (b *buf) cover(start, end int) {
	if b.start < 0 || start < b.start {
		b.start = start
	}
	if end > b.end {
		b.end = end
	}
}

// space writes the collapsed white space waiting before a character,
// unless it would lead the block or follow a line break.
//
// A space written at the start of the text being added stands between it
// and the text before it, and carries only the reasons both share.
func (b *buf) space() {
	if b.pending && len(b.b) > 0 && b.b[len(b.b)-1] != '\n' {
		b.b = append(b.b, ' ')
		if len(b.b)-1 == b.from {
			n := len(b.runs)
			var prev gofastcore.Display
			if n > 0 && b.runs[n-1].end == b.from {
				prev = b.runs[n-1].h
			}
			h := prev & b.cur
			if n > 0 && b.runs[n-1].end == b.from && h == prev {
				b.runs[n-1].end++
			} else {
				b.runs = append(b.runs, run{b.from, b.from + 1, h})
			}
			b.from++
		}
	}
	b.pending = false
}

// lineBreak ends the line: white space before it is dropped.
func (b *buf) lineBreak() {
	b.pending = false
	n := len(b.b)
	for n > 0 && b.b[n-1] == ' ' {
		n--
	}
	b.b = b.b[:n]
	b.clip()
	if n > 0 {
		b.b = append(b.b, '\n')
	}
}

// softBreak separates what follows from what came before, as a block
// boundary does inside a list item or table cell.
func (b *buf) softBreak() {
	if len(b.b) > 0 && b.b[len(b.b)-1] != '\n' {
		b.lineBreak()
	}
	b.pending = false
}

// clip keeps runs and spans within the text after it was shortened.
func (b *buf) clip() {
	n := len(b.b)
	for i := len(b.runs) - 1; i >= 0 && b.runs[i].end > n; i-- {
		b.runs[i].end = n
	}
	// Spans are added as their elements end, so in order of End.
	for i := len(b.spans) - 1; i >= 0 && b.spans[i].End > n; i-- {
		b.spans[i].End = n
	}
}

// add appends text in the given white-space mode with its declared
// display reasons. Verbatim text (declared code) keeps every character but
// controls (Unicode Cc); other text has its white space collapsed per mode
// and its ligatures written as letters. Format characters (zero-width
// characters, bidirectional controls, tag characters) and variation
// selectors are kept as they are.
func (b *buf) add(t []byte, ws dom.WS, h gofastcore.Display, verbatim bool) {
	b.from, b.cur = len(b.b), h
	defer func() { b.mark(b.from, h) }()
	keep := verbatim || ws == dom.Pre
	for i := 0; i < len(t); {
		c := t[i]
		if c < utf8.RuneSelf {
			i++
			if c == '\r' {
				if i < len(t) && t[i] == '\n' {
					continue
				}
				c = '\n'
			}
			if keep {
				if c == '\n' || c == '\t' || c >= 0x20 && c != 0x7F {
					if !verbatim {
						b.space()
					}
					b.b = append(b.b, c)
				}
				continue
			}
			switch c {
			case ' ', '\t', '\n', '\f':
				if ws == dom.PreLine && c == '\n' {
					b.lineBreak()
				} else {
					b.pending = true
				}
				continue
			}
			if c < 0x20 || c == 0x7F {
				continue
			}
			b.space()
			b.b = append(b.b, c)
			continue
		}
		r, size := utf8.DecodeRune(t[i:])
		chunk := t[i : i+size]
		i += size
		if unicode.IsControl(r) {
			continue
		}
		if !verbatim {
			b.space()
			if l := gofastcore.Ligature(r); l != "" {
				b.b = append(b.b, l...)
				continue
			}
		}
		b.b = append(b.b, chunk...)
	}
}

// mark records that the text from offset from to the end has reasons h.
func (b *buf) mark(from int, h gofastcore.Display) {
	to := len(b.b)
	if to <= from {
		return
	}
	if n := len(b.runs); n > 0 && b.runs[n-1].h == h && b.runs[n-1].end == from {
		b.runs[n-1].end = to
		return
	}
	b.runs = append(b.runs, run{from, to, h})
}

// finish returns the block's text, trimmed, with its display reasons and
// spans.
func (b *buf) finish() (text string, display gofastcore.Display, spans []gofastcore.Span) {
	s := b.b
	lead := 0
	for lead < len(s) {
		r, n := utf8.DecodeRune(s[lead:])
		if !unicode.IsSpace(r) {
			break
		}
		lead += n
	}
	end := len(s)
	for end > lead {
		r, n := utf8.DecodeLastRune(s[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= n
	}
	if end <= lead {
		return "", 0, nil
	}
	text = string(s[lead:end])
	display = b.displaySpans(lead, end, &spans)
	spans = b.styleSpans(lead, end, spans)
	return text, display, spans
}

// displaySpans works out the block's display reasons and appends spans
// for the passages with reasons the rest of the block does not share.
func (b *buf) displaySpans(lead, end int, spans *[]gofastcore.Span) gofastcore.Display {
	type piece struct {
		start, end int
		h          gofastcore.Display
		blank      bool
	}
	var ps []piece
	for _, r := range b.runs {
		s, e := max(r.start, lead), min(r.end, end)
		if s >= e {
			continue
		}
		ps = append(ps, piece{s - lead, e - lead, r.h, isBlank(b.b[s:e])})
	}
	whole, any, seen := gofastcore.DisplayAny, gofastcore.Display(0), false
	for _, p := range ps {
		if !p.blank {
			whole &= p.h
			any |= p.h
			seen = true
		}
	}
	if !seen || any == 0 {
		return 0
	}
	// White space between two passages with the same reasons takes them.
	for i := range ps {
		if !ps[i].blank {
			continue
		}
		var before, after gofastcore.Display
		if i > 0 {
			before = ps[i-1].h
		}
		if i+1 < len(ps) {
			after = ps[i+1].h
		}
		if before == after {
			ps[i].h = before
		} else {
			ps[i].h = 0
		}
	}
	for _, p := range ps {
		part := p.h &^ whole
		if part == 0 {
			continue
		}
		if n := len(*spans); n > 0 && (*spans)[n-1].End == p.start && (*spans)[n-1].Display == part {
			(*spans)[n-1].End = p.end
			continue
		}
		*spans = append(*spans, gofastcore.Span{Start: p.start, End: p.end, Display: part})
	}
	return any
}

// styleSpans appends the style and link spans, moved to the trimmed text.
func (b *buf) styleSpans(lead, end int, spans []gofastcore.Span) []gofastcore.Span {
	for _, sp := range b.spans {
		s, e := max(sp.Start, lead), min(sp.End, end)
		// A span that starts at collapsed white space starts after it.
		for s < e && b.b[s] == ' ' || s < e && b.b[s] == '\n' {
			s++
		}
		if s >= e {
			continue
		}
		sp.Start, sp.End = s-lead, e-lead
		spans = append(spans, sp)
	}
	return spans
}

func isBlank(s []byte) bool {
	for _, c := range s {
		if c != ' ' && c != '\n' && c != '\t' {
			return false
		}
	}
	return true
}
