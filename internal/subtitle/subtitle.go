// Package subtitle reads SubRip (.srt) and WebVTT (.vtt) text, decoded to
// UTF-8, into cues and the other blocks of the file, with their offsets in
// that text. Nothing is fetched or run.
package subtitle

import (
	"bytes"
	"context"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/entity"
)

// MaxText bounds the text of one cue or block: past it the text is cut,
// and the item says so (Cut).
const MaxText = 8 << 20

// Kind says what an item is.
type Kind uint8

const (
	// Cue is a cue: its timings, settings, identifier and text.
	Cue Kind = iota
	// Note is a WebVTT NOTE block, a comment no player shows.
	Note
	// Style is a WebVTT STYLE block: its text is a style sheet.
	Style
	// Stray is text that is in no cue: before the first cue of a SubRip
	// file, or a WebVTT block that is not a cue, NOTE, STYLE or REGION.
	Stray
)

// Item is one block of the file.
type Item struct {
	Kind Kind
	// Start and End are the offsets in the text of the first byte of the
	// item's first line (a cue's identifier or number line, else its
	// timing line) and of the end of its last line with text, its line
	// end not included.
	Start, End int
	// ID is a WebVTT cue's identifier, a SubRip cue's number, as written.
	ID string
	// Begin and Finish are the cue's timings, HH:MM:SS.mmm when TimingOK,
	// else as written.
	Begin, Finish string
	TimingOK      bool
	// Settings are WebVTT cue settings, or SubRip coordinates, as written.
	Settings string
	// Text is the text as a viewer sees it, markup removed, trimmed; empty
	// for a cue with no text.
	Text  string
	Spans []gofastcore.Span
	// Voices are the names of the WebVTT voices of the cue, in order, each
	// once.
	Voices []string
	// Controls counts the control characters dropped from the text.
	Controls int
	// Cut says that the text was longer than MaxText and was cut there.
	Cut bool
}

// Field is a WebVTT header line of the form "Key: value".
type Field struct{ Key, Value string }

// Header is what a WebVTT file says before its first cue.
type Header struct {
	// Signature says that the file begins with the WebVTT signature.
	Signature bool
	// Text is what follows WEBVTT on the first line, trimmed, and the
	// header lines that are not "Key: value", one per line.
	Text string
	// Fields are the header lines "Key: value", in order.
	Fields []Field
	// Regions are the REGION definitions, as written.
	Regions []string
	// Controls counts the control characters dropped from the header.
	Controls int
}

// line is one line of the text.
type line struct {
	raw        []byte // as decoded, without its end
	text       []byte // control characters but tab dropped
	start, end int    // offsets of the line, its end not included
	controls   int
}

func (l *line) blank() bool { return len(bytes.TrimSpace(l.text)) == 0 }

func (l *line) timing() bool { return bytes.Contains(l.text, []byte("-->")) }

// reader gives the lines of the text one by one; one line can be put
// back.
type reader struct {
	ctx  context.Context
	src  []byte
	pos  int
	n    int
	back *line
	err  error
}

func (r *reader) next() (line, bool) {
	if r.back != nil {
		l := *r.back
		r.back = nil
		return l, true
	}
	if r.pos >= len(r.src) || r.err != nil {
		return line{}, false
	}
	if r.n++; r.n&1023 == 0 {
		if err := r.ctx.Err(); err != nil {
			r.err = err
			return line{}, false
		}
	}
	s := r.pos
	e := s
	for e < len(r.src) && r.src[e] != '\n' && r.src[e] != '\r' {
		e++
	}
	r.pos = e
	if e < len(r.src) {
		r.pos++
		if r.src[e] == '\r' && r.pos < len(r.src) && r.src[r.pos] == '\n' {
			r.pos++
		}
	}
	l := line{raw: r.src[s:e], start: s, end: e}
	l.text = dropControls(l.raw, &l.controls)
	return l, true
}

func (r *reader) unread(l line) { r.back = &l }

// dropControls returns src without its control characters other than
// tab, counted in *n: src itself when it has none.
func dropControls(src []byte, n *int) []byte {
	var dst []byte
	from := 0
	for i := 0; i < len(src); {
		c := src[i]
		w := 0
		switch {
		case c < 0x20 && c != '\t', c == 0x7F:
			w = 1
		case c == 0xC2 && i+1 < len(src) && src[i+1] >= 0x80 && src[i+1] <= 0x9F:
			w = 2
		}
		if w == 0 {
			i++
			continue
		}
		*n++
		dst = append(dst, src[from:i]...)
		i += w
		from = i
	}
	if dst == nil && from == 0 {
		return src
	}
	return append(dst, src[from:]...)
}

// SRT reads SubRip text, giving yield each cue (also one with no text)
// and the text before the first cue. It stops when yield returns false,
// and returns the context's error when it is cancelled.
func SRT(ctx context.Context, src []byte, yield func(*Item) bool) error {
	r := &reader{ctx: ctx, src: src}
	var (
		cur     *Item
		body    []byte // the cue's text, markup and all
		hasText bool
		blank   bool // blank lines since the last line of text
		ctrl    int
		over    bool // lines past MaxText left out of body
	)
	flush := func() bool {
		if cur == nil {
			return true
		}
		it := cur
		cur = nil
		if it.Kind == Stray && !hasText {
			return true
		}
		b := newBuilder()
		if it.Kind == Cue {
			srtText(body, b)
		} else {
			b.str(body)
		}
		it.Text, it.Spans, it.Cut = b.finish()
		it.Cut = it.Cut || over
		it.Controls = ctrl + b.controls
		body, hasText, blank, ctrl, over = body[:0], false, false, 0, false
		return yield(it)
	}
	for {
		l, ok := r.next()
		if !ok {
			break
		}
		var num *line
		if !l.timing() && isNumber(l.text) {
			if t, ok := r.next(); ok {
				if t.timing() {
					n := l
					num, l = &n, t
				} else {
					r.unread(t)
				}
			}
		}
		if l.timing() {
			if !flush() {
				return nil
			}
			cur = &Item{Kind: Cue, Start: l.start, End: l.end}
			if num != nil {
				cur.Start = num.start
				cur.ID = string(bytes.TrimSpace(num.text))
			}
			cur.Begin, cur.Finish, cur.Settings, cur.TimingOK = timing(l.text)
			continue
		}
		if l.blank() {
			blank = hasText
			ctrl += l.controls // counted with the next item
			continue
		}
		if cur == nil {
			cur = &Item{Kind: Stray, Start: l.start}
		}
		if hasText {
			if blank {
				body = append(body, "\n\n"...)
			} else {
				body = append(body, '\n')
			}
		}
		if len(body) <= MaxText {
			body = append(body, l.text...)
		} else {
			over = true
		}
		ctrl += l.controls
		hasText, blank = true, false
		cur.End = l.end
	}
	if r.err != nil {
		return r.err
	}
	flush()
	return nil
}

// isNumber reports whether a line is a cue number: digits, with white
// space around them.
func isNumber(b []byte) bool {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// VTT reads WebVTT text, giving yield each cue (also one with no text),
// NOTE and STYLE block, and block that is none of these, and returning
// the header and the REGION definitions. It stops when yield returns
// false, and returns the context's error when it is cancelled.
func VTT(ctx context.Context, src []byte, yield func(*Item) bool) (Header, error) {
	r := &reader{ctx: ctx, src: src}
	var h Header
	if l, ok := r.next(); ok {
		if rest, ok := signature(l.raw); ok {
			h.Signature = true
			h.Text = string(bytes.TrimSpace(dropControls(rest, &h.Controls)))
			// The header: lines up to the first blank one, or to one
			// with an arrow, which begins a cue.
			for {
				l, ok := r.next()
				if !ok || l.blank() {
					break
				}
				if l.timing() {
					r.unread(l)
					break
				}
				h.Controls += l.controls
				if k, v, ok := field(l.text); ok {
					h.Fields = append(h.Fields, Field{k, v})
				} else if t := bytes.TrimSpace(l.text); len(h.Text)+len(t) < MaxText {
					if h.Text != "" {
						h.Text += "\n"
					}
					h.Text += string(t)
				}
			}
		} else {
			r.unread(l)
		}
	}
	var body []byte
	ctrl := 0 // of blank lines, counted with the next item
	for {
		l, ok := r.next()
		if !ok {
			break
		}
		if l.blank() {
			ctrl += l.controls
			continue
		}
		it := &Item{Start: l.start, End: l.end}
		var t *line
		if l.timing() {
			t = &l
		} else if n, ok := r.next(); ok {
			if !n.blank() && n.timing() {
				t = &n
				it.ID = string(bytes.TrimSpace(l.text))
				ctrl += l.controls
			} else {
				r.unread(n)
			}
		}
		body = body[:0]
		lines, over := 0, false
		// add appends the lines of the block to body, up to a blank line
		// or, the spec's rule, a line with an arrow, which is put back
		// to begin the next block.
		add := func() {
			for {
				n, ok := r.next()
				if !ok || n.blank() {
					ctrl += n.controls
					return
				}
				if n.timing() {
					r.unread(n)
					return
				}
				if lines > 0 {
					body = append(body, '\n')
				}
				lines++
				if len(body) <= MaxText {
					body = append(body, n.text...)
				} else {
					over = true
				}
				ctrl += n.controls
				it.End = n.end
			}
		}
		b := newBuilder()
		if t != nil {
			it.Kind = Cue
			it.Begin, it.Finish, it.Settings, it.TimingOK = timing(t.text)
			add()
			vttText(body, b, &it.Voices)
		} else {
			first := l.text
			lines = 1
			body = append(body, first...)
			ctrl += l.controls
			add()
			switch {
			case keyword(first, "NOTE", false):
				it.Kind = Note
				b.str(body[4:])
			case keyword(first, "STYLE", true):
				it.Kind = Style
				b.verbatim(body[len(first):])
			case keyword(first, "REGION", true):
				if t := bytes.TrimSpace(body[len(first):]); len(t) > 0 && len(t) < MaxText {
					h.Regions = append(h.Regions, string(t))
				}
				h.Controls += ctrl
				ctrl = 0
				continue
			default:
				it.Kind = Stray
				b.str(body)
			}
		}
		it.Text, it.Spans, it.Cut = b.finish()
		it.Cut = it.Cut || over
		it.Controls = ctrl + b.controls
		ctrl = 0
		if !yield(it) {
			return h, nil
		}
	}
	return h, r.err
}

// signature reports whether a first line is the WebVTT signature, and
// returns what follows it.
func signature(raw []byte) ([]byte, bool) {
	if !bytes.HasPrefix(raw, []byte("WEBVTT")) {
		return nil, false
	}
	rest := raw[6:]
	if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' {
		return nil, false
	}
	return rest, true
}

// keyword reports whether a block's first line is the word w followed by
// a space, a tab or nothing; alone, the rest of the line is only white
// space.
func keyword(first []byte, w string, alone bool) bool {
	if !bytes.HasPrefix(first, []byte(w)) {
		return false
	}
	rest := first[len(w):]
	if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' {
		return false
	}
	return !alone || len(bytes.TrimSpace(rest)) == 0
}

// field reads a header line "Key: value": a key of letters, digits, "-",
// "_" or ".".
func field(b []byte) (string, string, bool) {
	i := bytes.IndexByte(b, ':')
	if i <= 0 || i > 64 {
		return "", "", false
	}
	for _, c := range b[:i] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return "", "", false
		}
	}
	return string(b[:i]), string(bytes.TrimSpace(b[i+1:])), true
}

// timing reads a timing line "start --> end settings". The timings are
// written HH:MM:SS.mmm when both can be read, else as written.
func timing(b []byte) (start, end, settings string, ok bool) {
	i := bytes.Index(b, []byte("-->"))
	l := bytes.TrimSpace(b[:i])
	rest := bytes.TrimSpace(b[i+3:])
	e := rest
	if j := bytes.IndexAny(rest, " \t"); j >= 0 {
		e, rest = rest[:j], bytes.TrimSpace(rest[j:])
	} else {
		rest = nil
	}
	settings = string(rest)
	s1, ok1 := Timestamp(l)
	s2, ok2 := Timestamp(e)
	if ok1 && ok2 {
		return s1, s2, settings, true
	}
	return string(l), string(e), settings, false
}

// Timestamp reads a timestamp: [hours:]minutes:seconds, then "." or ","
// and one to three digits of a fraction, or none. Minutes and seconds
// are one or two digits, under 60. It returns it as HH:MM:SS.mmm.
func Timestamp(b []byte) (string, bool) {
	var f []byte
	if i := bytes.IndexAny(b, ".,"); i >= 0 {
		b, f = b[:i], b[i+1:]
		if len(f) == 0 || len(f) > 3 || !digits(f) {
			return "", false
		}
	}
	g := bytes.Split(b, []byte(":"))
	if len(g) == 2 {
		g = append([][]byte{[]byte("00")}, g...)
	}
	if len(g) != 3 || len(g[0]) == 0 || len(g[0]) > 9 || !digits(g[0]) {
		return "", false
	}
	for _, x := range g[1:] {
		if len(x) == 0 || len(x) > 2 || !digits(x) || x[0] > '5' && len(x) == 2 {
			return "", false
		}
	}
	out := make([]byte, 0, 16)
	for i, x := range g {
		if i > 0 {
			out = append(out, ':')
		}
		if len(x) == 1 {
			out = append(out, '0')
		}
		out = append(out, x...)
	}
	out = append(out, '.')
	out = append(out, f...)
	for range 3 - len(f) {
		out = append(out, '0')
	}
	return string(out), true
}

func digits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// builder makes the text of an item and its spans.
type builder struct {
	b        []byte
	spans    []gofastcore.Span
	cut      bool
	controls int
	verb     bool // verbatim: ligatures kept, nothing trimmed but line ends
}

func newBuilder() *builder { return &builder{} }

// add appends a character: a control character but tab and line feed is
// dropped and counted, a ligature written as its letters.
func (w *builder) add(r rune) {
	if w.cut {
		return
	}
	if r != '\t' && r != '\n' && unicode.IsControl(r) {
		w.controls++
		return
	}
	if len(w.b) >= MaxText {
		w.cut = true
		return
	}
	if l := gofastcore.Ligature(r); l != "" && !w.verb {
		w.b = append(w.b, l...)
		return
	}
	w.b = utf8.AppendRune(w.b, r)
}

// str appends text that has no markup.
func (w *builder) str(p []byte) {
	for len(p) > 0 && !w.cut {
		if c := p[0]; c < utf8.RuneSelf {
			w.add(rune(c))
			p = p[1:]
			continue
		}
		r, n := utf8.DecodeRune(p)
		w.add(r)
		p = p[n:]
	}
}

// verbatim appends code: as it is, ligatures kept.
func (w *builder) verbatim(p []byte) {
	w.verb = true
	w.str(p)
}

func (w *builder) span(start int, s gofastcore.Style) {
	if s != 0 && start < len(w.b) {
		w.spans = append(w.spans, gofastcore.Span{Start: start, End: len(w.b), Style: s})
	}
}

// finish returns the text trimmed, the spans moved to it.
func (w *builder) finish() (string, []gofastcore.Span, bool) {
	b := w.b
	if w.verb {
		for len(b) > 0 && (b[0] == '\n' || b[0] == '\r') {
			b = b[1:]
		}
		if len(bytes.TrimSpace(b)) == 0 {
			return "", nil, w.cut
		}
		return string(bytes.TrimRight(b, " \t\n")), nil, w.cut
	}
	lead := len(b) - len(bytes.TrimLeftFunc(b, unicode.IsSpace))
	end := len(bytes.TrimRightFunc(b, unicode.IsSpace))
	if end <= lead {
		return "", nil, w.cut
	}
	var spans []gofastcore.Span
	for _, sp := range w.spans {
		s, e := max(sp.Start, lead), min(sp.End, end)
		for s < e && (b[s] == ' ' || b[s] == '\n') {
			s++
		}
		if s >= e {
			continue
		}
		sp.Start, sp.End = s-lead, e-lead
		spans = append(spans, sp)
	}
	return string(b[lead:end]), spans, w.cut
}

// srtText appends SubRip cue text without its markup: <b> <i> <u> <font
// ...> and their end tags, {b} {i} {u} and their ends, and override codes
// {\...}. Any other "<" or "{" is text.
func srtText(s []byte, w *builder) {
	var depth [3]int
	var start [3]int
	styles := [3]gofastcore.Style{gofastcore.StyleBold, gofastcore.StyleItalic, gofastcore.StyleUnderline}
	for i := 0; i < len(s) && !w.cut; {
		c := s[i]
		if c == '<' || c == '{' {
			if k, end, n := srtTag(s[i:]); n > 0 {
				if k >= 0 {
					if !end {
						if depth[k] == 0 {
							start[k] = len(w.b)
						}
						depth[k]++
					} else if depth[k] > 0 {
						if depth[k]--; depth[k] == 0 {
							w.span(start[k], styles[k])
						}
					}
				}
				i += n
				continue
			}
		}
		if c < utf8.RuneSelf {
			w.add(rune(c))
			i++
			continue
		}
		r, n := utf8.DecodeRune(s[i:])
		w.add(r)
		i += n
	}
	for k := range depth {
		if depth[k] > 0 {
			w.span(start[k], styles[k])
		}
	}
}

// srtTag reads the SubRip markup at the start of s: it returns the style
// (0 bold, 1 italic, 2 underline, -1 none), whether it ends the style,
// and its length, 0 when s does not begin with markup.
func srtTag(s []byte) (k int, end bool, n int) {
	style := func(c byte) int {
		switch c | 0x20 {
		case 'b':
			return 0
		case 'i':
			return 1
		case 'u':
			return 2
		}
		return -1
	}
	open, close := s[0], byte('>')
	if open == '{' {
		close = '}'
		if len(s) > 1 && s[1] == '\\' {
			if j := bytes.IndexByte(s, '}'); j > 0 {
				return -1, false, j + 1
			}
			return -1, false, 0
		}
	}
	if len(s) >= 3 && s[2] == close {
		if k := style(s[1]); k >= 0 {
			return k, false, 3
		}
	}
	if len(s) >= 4 && s[1] == '/' && s[3] == close {
		if k := style(s[2]); k >= 0 {
			return k, true, 4
		}
	}
	if open == '<' {
		if len(s) > 5 && bytes.EqualFold(s[1:5], []byte("font")) && (s[5] == '>' || s[5] == ' ' || s[5] == '\t' || s[5] == '\n') {
			if j := bytes.IndexByte(s, '>'); j > 0 {
				return -1, false, j + 1
			}
		}
		if len(s) >= 7 && bytes.EqualFold(s[:7], []byte("</font>")) {
			return -1, true, 7
		}
	}
	return -1, false, 0
}

// vttText appends WebVTT cue text, parsed as the WebVTT cue text parsing
// rules have it: tags are markup (those of the spec give spans and voices,
// others are ignored), character references are decoded. A "<" with no
// ">" after it is text.
func vttText(s []byte, w *builder, voices *[]string) {
	type open struct {
		name  string
		style gofastcore.Style
		start int
	}
	var stack []open
	pop := func() {
		o := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		w.span(o.start, o.style)
	}
	for i := 0; i < len(s) && !w.cut; {
		c := s[i]
		switch {
		case c == '&':
			r, n := reference(s[i:])
			if n == 0 {
				w.add('&')
				i++
				continue
			}
			for _, x := range r {
				w.add(x)
			}
			i += n
			continue
		case c == '<':
			j := bytes.IndexByte(s[i:], '>')
			if j < 0 {
				break
			}
			tag := s[i+1 : i+j]
			i += j + 1
			if len(tag) == 0 || tag[0] >= '0' && tag[0] <= '9' {
				continue // a timestamp tag, or nothing
			}
			if tag[0] == '/' {
				name := tagName(tag[1:])
				if len(stack) == 0 {
					continue
				}
				if top := stack[len(stack)-1].name; top == name {
					pop()
				} else if name == "ruby" && top == "rt" {
					pop()
					pop()
				}
				continue
			}
			name := tagName(tag)
			var st gofastcore.Style
			switch name {
			case "b":
				st = gofastcore.StyleBold
			case "i":
				st = gofastcore.StyleItalic
			case "u":
				st = gofastcore.StyleUnderline
			case "c", "ruby", "lang":
			case "rt":
				if len(stack) == 0 || stack[len(stack)-1].name != "ruby" {
					continue
				}
			case "v":
				if a := annotation(tag); a != "" && !contains(*voices, a) {
					*voices = append(*voices, a)
				}
			default:
				continue
			}
			stack = append(stack, open{name, st, len(w.b)})
			continue
		}
		if c < utf8.RuneSelf {
			w.add(rune(c))
			i++
			continue
		}
		r, n := utf8.DecodeRune(s[i:])
		w.add(r)
		i += n
	}
	for len(stack) > 0 {
		pop()
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// tagName is the name of a tag: up to a period, white space or the end.
func tagName(t []byte) string {
	i := bytes.IndexAny(t, ". \t\n\f")
	if i < 0 {
		i = len(t)
	}
	return string(t[:i])
}

// annotation is what follows a tag's name and classes, with its white
// space collapsed, control characters, ligatures and references dealt
// with as in text.
func annotation(t []byte) string {
	i := bytes.IndexAny(t, " \t\n\f")
	if i < 0 {
		return ""
	}
	b := newBuilder()
	for _, f := range bytes.Fields(t[i:]) {
		if len(b.b) > 0 {
			b.add(' ')
		}
		for len(f) > 0 {
			if f[0] == '&' {
				if r, n := reference(f); n > 0 {
					for _, x := range r {
						b.add(x)
					}
					f = f[n:]
					continue
				}
			}
			r, n := utf8.DecodeRune(f)
			b.add(r)
			f = f[n:]
		}
	}
	s, _, _ := b.finish()
	return s
}

// reference reads the character reference at the start of s, which
// begins with "&": what it stands for and its length, 0 when there is
// none.
func reference(s []byte) (string, int) {
	if len(s) > 2 && s[1] == '#' {
		i, hex := 2, false
		if s[i] == 'x' || s[i] == 'X' {
			i, hex = 3, true
		}
		from := i
		var v int64
		for i < len(s) {
			c := s[i]
			var d int64
			switch {
			case c >= '0' && c <= '9':
				d = int64(c - '0')
			case hex && c|0x20 >= 'a' && c|0x20 <= 'f':
				d = int64(c|0x20-'a') + 10
			default:
				goto done
			}
			if hex {
				v = v*16 + d
			} else {
				v = v*10 + d
			}
			v = min(v, 0x110000)
			i++
		}
	done:
		if i == from {
			return "", 0
		}
		if i < len(s) && s[i] == ';' {
			i++
		}
		return string(entity.Numeric(v)), i
	}
	repl, n, _, ok := entity.Named(s[1:])
	if !ok {
		return "", 0
	}
	return repl, n + 1
}
