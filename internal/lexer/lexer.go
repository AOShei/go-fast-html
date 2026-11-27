// Package lexer is an HTML tokenizer following the WHATWG HTML standard,
// section 13.2.5 "Tokenization", for the states that decide what text a
// document holds: data, RCDATA, RAWTEXT, script data with its escaped
// states, PLAINTEXT, tags and attributes, comments, bogus comments,
// DOCTYPE, CDATA sections in foreign content, and character references.
//
// It reports no parse errors; where the standard recovers from one, the
// lexer recovers the same way. The tree builder switches it into the
// RCDATA, RAWTEXT, script data and PLAINTEXT states, as the standard's
// tree construction does.
package lexer

import (
	"bytes"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/AOShei/go-fast-core/entity"
)

// Kind is the kind of a token.
type Kind uint8

const (
	EOF Kind = iota
	Text
	StartTag
	EndTag
	Comment
	Doctype
)

// Attr is one attribute of a tag, its name lowercased. ValStart and
// ValEnd are the byte range of the value in the input, quotes excluded.
type Attr struct {
	Name, Value      string
	ValStart, ValEnd int
}

// Token is one token. Start and End are byte offsets into the input.
type Token struct {
	Kind        Kind
	Name        string // tag name, lowercased
	Attrs       []Attr // the first of two attributes with one name is kept
	SelfClosing bool
	Data        string // text with character references resolved, or comment text
	Start, End  int
}

// Attr returns the value of the named attribute and whether it is present.
func (t *Token) Attr(name string) (string, bool) {
	for _, a := range t.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// Raw is a text state the tree builder can put the lexer in.
type Raw uint8

const (
	Data Raw = iota
	RCDATA
	RAWTEXT
	ScriptData
	Plaintext
)

// Lexer tokenizes one document.
type Lexer struct {
	src    []byte
	str    string    // src as a string: tokens that need no decoding are slices of it
	attrs  []Attr    // the attributes of the tag being read
	block  []Attr    // room for the attributes of tags to come; see keepAttrs
	taken  []*[]Attr // the blocks taken from attrPool, for Release
	pos    int
	raw    Raw
	rawTag string
	// CDATA lets <![CDATA[ open a CDATA section, as it does when the
	// adjusted current node is in a foreign namespace (SVG, MathML).
	CDATA bool
	sb    []byte
	// Replaced counts the character references to no character (&#0;, a
	// surrogate, a number past U+10FFFF) read as U+FFFD.
	Replaced int
}

// New returns a lexer over src, which must be UTF-8 and must not be
// changed while the lexer, its tokens or anything made of them are in
// use: the strings of tokens that need no decoding (text, names, values)
// share src's memory instead of copying it. What is kept past the
// extraction of a document (the Document's strings) is copied first.
func New(src []byte) *Lexer {
	return &Lexer{src: src, str: unsafe.String(unsafe.SliceData(src), len(src))}
}

// SetRaw switches the lexer to a text state that ends at the end tag of
// tag (lowercase); Data switches back.
func (l *Lexer) SetRaw(r Raw, tag string) { l.raw, l.rawTag = r, tag }

// Pos returns the offset of the next byte to be read.
func (l *Lexer) Pos() int { return l.pos }

// Next returns the next token; a token of kind EOF at the end.
func (l *Lexer) Next() Token {
	if l.pos >= len(l.src) {
		return Token{Kind: EOF, Start: len(l.src), End: len(l.src)}
	}
	switch l.raw {
	case RCDATA, RAWTEXT:
		return l.rawText(l.raw == RCDATA)
	case ScriptData:
		return l.scriptData()
	case Plaintext:
		start := l.pos
		l.pos = len(l.src)
		return Token{Kind: Text, Data: l.str[start:], Start: start, End: l.pos}
	}
	if l.src[l.pos] == '<' {
		if t, ok := l.markup(); ok {
			return t
		}
	}
	return l.text()
}

// text reads character data up to the next '<' that may open markup.
func (l *Lexer) text() Token {
	start := l.pos
	// Text with no character reference before the markup that ends it is
	// a slice of the input.
	for {
		rest := l.src[l.pos:]
		lt := bytes.IndexByte(rest, '<')
		upto := rest
		if lt >= 0 {
			upto = rest[:lt]
		}
		if amp := bytes.IndexByte(upto, '&'); amp >= 0 {
			l.pos += amp
			break // a reference: decoded below
		}
		if lt < 0 {
			l.pos = len(l.src)
			return Token{Kind: Text, Data: l.str[start:], Start: start, End: l.pos}
		}
		l.pos += lt
		if l.pos > start && l.opensMarkup() {
			return Token{Kind: Text, Data: l.str[start:l.pos], Start: start, End: l.pos}
		}
		l.pos++ // a '<' that is text
	}
	l.sb = append(l.sb[:0], l.src[start:l.pos]...)
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '<' && l.pos > start && l.opensMarkup() {
			break
		}
		if c == '&' {
			l.pos++
			l.sb = l.charRef(l.sb, false)
			continue
		}
		l.sb = append(l.sb, c)
		l.pos++
	}
	return Token{Kind: Text, Data: string(l.sb), Start: start, End: l.pos}
}

// opensMarkup reports whether the '<' at pos begins something other than
// text.
func (l *Lexer) opensMarkup() bool {
	if l.pos+1 >= len(l.src) {
		return false
	}
	c := l.src[l.pos+1]
	return isLetter(c) || c == '/' || c == '!' || c == '?'
}

// markup reads a tag, comment or declaration at a '<'. It reports false
// when the '<' is text.
func (l *Lexer) markup() (Token, bool) {
	s := l.src
	p := l.pos
	if p+1 >= len(s) {
		return Token{}, false
	}
	switch c := s[p+1]; {
	case isLetter(c):
		return l.tag(StartTag, p+1), true
	case c == '/':
		if p+2 >= len(s) {
			return Token{}, false // "</" at the end is text
		}
		switch d := s[p+2]; {
		case isLetter(d):
			return l.tag(EndTag, p+2), true
		case d == '>':
			// "</>" is dropped: an empty text token stands for it.
			l.pos = p + 3
			return Token{Kind: Text, Start: p, End: l.pos}, true
		default:
			return l.bogusComment(p, p+2), true
		}
	case c == '!':
		rest := s[p+2:]
		switch {
		case bytes.HasPrefix(rest, []byte("--")):
			return l.comment(p, p+4), true
		case len(rest) >= 7 && bytes.EqualFold(rest[:7], []byte("doctype")):
			end := bytes.IndexByte(s[p:], '>')
			if end < 0 {
				l.pos = len(s)
			} else {
				l.pos = p + end + 1
			}
			return Token{Kind: Doctype, Start: p, End: l.pos}, true
		case l.CDATA && bytes.HasPrefix(rest, []byte("[CDATA[")):
			body := p + 9
			end := bytes.Index(s[body:], []byte("]]>"))
			t := Token{Kind: Text, Start: p}
			if end < 0 {
				t.Data = l.str[body:]
				l.pos = len(s)
			} else {
				t.Data = l.str[body : body+end]
				l.pos = body + end + 3
			}
			t.End = l.pos
			return t, true
		}
		return l.bogusComment(p, p+2), true
	case c == '?':
		return l.bogusComment(p, p+1), true
	}
	return Token{}, false
}

// comment reads a comment whose text begins at body, after "<!--".
func (l *Lexer) comment(start, body int) Token {
	s := l.src
	t := Token{Kind: Comment, Start: start}
	// "<!-->" and "<!--->" are empty comments.
	switch {
	case body < len(s) && s[body] == '>':
		l.pos = body + 1
	case body+1 < len(s) && s[body] == '-' && s[body+1] == '>':
		l.pos = body + 2
	default:
		end, after := len(s), len(s)
		for i := body; i+1 < len(s); i++ {
			if s[i] != '-' || s[i+1] != '-' {
				continue
			}
			if i+2 < len(s) && s[i+2] == '>' {
				end, after = i, i+3
				break
			}
			if i+3 < len(s) && s[i+2] == '!' && s[i+3] == '>' {
				end, after = i, i+4
				break
			}
		}
		t.Data = l.str[body:end]
		l.pos = after
	}
	t.End = l.pos
	return t
}

// bogusComment reads a comment that runs to the next '>'.
func (l *Lexer) bogusComment(start, body int) Token {
	s := l.src
	end := bytes.IndexByte(s[body:], '>')
	t := Token{Kind: Comment, Start: start}
	if end < 0 {
		t.Data = l.str[body:]
		l.pos = len(s)
	} else {
		t.Data = l.str[body : body+end]
		l.pos = body + end + 1
	}
	t.End = l.pos
	return t
}

// tag reads a start or end tag whose name begins at p. A tag cut off by
// the end of the input is dropped, as the standard prescribes.
func (l *Lexer) tag(kind Kind, p int) Token {
	s := l.src
	t := Token{Kind: kind, Start: l.pos}
	i := p
	for i < len(s) && !isSpace(s[i]) && s[i] != '/' && s[i] != '>' {
		i++
	}
	t.Name = l.lowerName(p, i)
	l.attrs = l.attrs[:0]
	var seen map[string]bool // the names so far, once there are many
	for {
		// Before attribute name.
		for i < len(s) && (isSpace(s[i]) || s[i] == '/' && (i+1 >= len(s) || s[i+1] != '>')) {
			i++
		}
		if i >= len(s) {
			l.pos = len(s)
			return Token{Kind: EOF, Start: len(s), End: len(s)}
		}
		if s[i] == '/' { // "/>"
			t.SelfClosing = true
			i++
		}
		if s[i] == '>' {
			l.pos = i + 1
			t.End = l.pos
			if kind == EndTag {
				t.SelfClosing = false
			} else {
				t.Attrs = l.keepAttrs()
			}
			return t
		}
		// Attribute name: an '=' at its start is part of it.
		ns := i
		i++
		for i < len(s) && !isSpace(s[i]) && s[i] != '/' && s[i] != '>' && s[i] != '=' {
			i++
		}
		name := l.lowerName(ns, i)
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		value := ""
		vs, ve := i, i
		if i < len(s) && s[i] == '=' {
			i++
			for i < len(s) && isSpace(s[i]) {
				i++
			}
			l.pos = i
			value = l.attrValue()
			vs, ve = i, l.pos
			if vs < len(s) && (s[vs] == '"' || s[vs] == '\'') {
				q := s[vs]
				vs++
				if ve > vs && s[ve-1] == q {
					ve--
				}
			}
			i = l.pos
		}
		a := Attr{Name: name, Value: value, ValStart: vs, ValEnd: max(ve, vs)}
		if len(l.attrs) == 16 && seen == nil {
			seen = make(map[string]bool, 32)
			for _, a := range l.attrs {
				seen[a.Name] = true
			}
		}
		switch {
		case seen != nil:
			if !seen[name] {
				seen[name] = true
				l.attrs = append(l.attrs, a)
			}
		case !hasAttr(l.attrs, name):
			l.attrs = append(l.attrs, a)
		}
	}
}

// attrValue reads an attribute value at pos, quoted or not.
func (l *Lexer) attrValue() string {
	s := l.src
	if l.pos >= len(s) {
		return ""
	}
	l.sb = l.sb[:0]
	if q := s[l.pos]; q == '"' || q == '\'' {
		l.pos++
		// A value with no character reference is a slice of the input.
		if end := bytes.IndexByte(s[l.pos:], q); end >= 0 && bytes.IndexByte(s[l.pos:l.pos+end], '&') < 0 {
			v := l.str[l.pos : l.pos+end]
			l.pos += end + 1
			return v
		}
		for l.pos < len(s) && s[l.pos] != q {
			if s[l.pos] == '&' {
				l.pos++
				l.sb = l.charRef(l.sb, true)
				continue
			}
			l.sb = append(l.sb, s[l.pos])
			l.pos++
		}
		if l.pos < len(s) {
			l.pos++
		}
		return string(l.sb)
	}
	for l.pos < len(s) && !isSpace(s[l.pos]) && s[l.pos] != '>' {
		if s[l.pos] == '&' {
			l.pos++
			l.sb = l.charRef(l.sb, true)
			continue
		}
		l.sb = append(l.sb, s[l.pos])
		l.pos++
	}
	return string(l.sb)
}

// charRef resolves a character reference whose '&' has been read, and
// appends what it stands for, or the '&' itself when there is none.
func (l *Lexer) charRef(dst []byte, inAttr bool) []byte {
	s := l.src
	p := l.pos
	if p < len(s) && s[p] == '#' {
		q := p + 1
		hex := q < len(s) && (s[q] == 'x' || s[q] == 'X')
		if hex {
			q++
		}
		ds := q
		var v int64
		for q < len(s) && (isDigit(s[q]) || hex && isHex(s[q])) {
			if v <= 0x10FFFF {
				if hex {
					v = v*16 + int64(hexVal(s[q]))
				} else {
					v = v*10 + int64(s[q]-'0')
				}
			}
			q++
		}
		if q == ds {
			return append(dst, '&') // "&#" or "&#x" with no digits is text
		}
		if q < len(s) && s[q] == ';' {
			q++
		}
		l.pos = q
		r := entity.Numeric(v)
		if r == utf8.RuneError && v != utf8.RuneError {
			l.Replaced++
		}
		return utf8.AppendRune(dst, r)
	}
	repl, n, semi, ok := entity.Named(s[p:])
	if !ok {
		return append(dst, '&')
	}
	if inAttr && !semi && p+n < len(s) && (s[p+n] == '=' || isAlnum(s[p+n])) {
		// For historical reasons, "&amp=" in an attribute is left as text.
		return append(dst, '&')
	}
	l.pos = p + n
	return append(dst, repl...)
}

// rawText reads RCDATA or RAWTEXT up to the end tag of rawTag.
func (l *Lexer) rawText(refs bool) Token {
	start := l.pos
	end := l.findEndTag(start)
	t := Token{Kind: Text, Start: start, End: end}
	if refs {
		l.sb = l.sb[:0]
		l.pos = start
		for l.pos < end {
			if l.src[l.pos] == '&' {
				l.pos++
				l.sb = l.charRef(l.sb, false)
				if l.pos > end { // a reference cannot run past the end tag
					l.pos = end
				}
				continue
			}
			l.sb = append(l.sb, l.src[l.pos])
			l.pos++
		}
		t.Data = string(l.sb)
	} else {
		t.Data = l.str[start:end]
	}
	l.pos = end
	l.raw = Data
	if end == start {
		return l.Next()
	}
	return t
}

// findEndTag returns the offset of the first appropriate end tag of
// rawTag at or after p, or the end of the input.
func (l *Lexer) findEndTag(p int) int {
	s := l.src
	for {
		i := bytes.Index(s[p:], []byte("</"))
		if i < 0 {
			return len(s)
		}
		i += p
		if l.isEndTag(i) {
			return i
		}
		p = i + 2
	}
}

// isEndTag reports whether "</rawTag" followed by whitespace, '/' or '>'
// starts at i.
func (l *Lexer) isEndTag(i int) bool {
	s := l.src
	n := len(l.rawTag)
	if i+2+n >= len(s) || s[i] != '<' || s[i+1] != '/' {
		return false
	}
	if !bytes.EqualFold(s[i+2:i+2+n], []byte(l.rawTag)) {
		return false
	}
	c := s[i+2+n]
	return isSpace(c) || c == '/' || c == '>'
}

// scriptData reads script data up to the end tag of rawTag, following the
// standard's escaped and double-escaped states: inside "<!--", a
// "<script>" starts a stretch in which "</script>" does not end the
// element, until "-->".
func (l *Lexer) scriptData() Token {
	s := l.src
	start := l.pos
	const (
		plain = iota
		escaped
		double
	)
	state := plain
	dashes := 0
	i := start
	end := len(s)
scan:
	for i < len(s) {
		c := s[i]
		switch state {
		case plain:
			if c == '<' {
				if l.isEndTag(i) {
					end = i
					break scan
				}
				if bytes.HasPrefix(s[i:], []byte("<!--")) {
					state, dashes = escaped, 2
					i += 4
					continue
				}
			}
		case escaped, double:
			switch {
			case c == '-':
				dashes++
				i++
				continue
			case c == '>' && dashes >= 2:
				state = plain
			case c == '<' && state == escaped && l.isEndTag(i):
				end = i
				break scan
			case c == '<' && state == escaped && isScriptTag(s, i+1):
				state = double
			case c == '<' && state == double && i+1 < len(s) && s[i+1] == '/' && isScriptTag(s, i+2):
				state = escaped
			}
		}
		dashes = 0
		i++
	}
	l.pos = end
	l.raw = Data
	if end == start {
		return l.Next()
	}
	return Token{Kind: Text, Data: l.str[start:end], Start: start, End: end}
}

// isScriptTag reports whether "script" followed by whitespace, '/' or '>'
// starts at i.
func isScriptTag(s []byte, i int) bool {
	return i+6 < len(s) && bytes.EqualFold(s[i:i+6], []byte("script")) &&
		(isSpace(s[i+6]) || s[i+6] == '/' || s[i+6] == '>')
}

func hasAttr(as []Attr, name string) bool {
	for _, a := range as {
		if a.Name == name {
			return true
		}
	}
	return false
}

// lowerName returns src[from:to] with ASCII letters lowercased and a NUL
// as U+FFFD: a slice of the input when it is so already.
func (l *Lexer) lowerName(from, to int) string {
	b := l.src[from:to]
	plain := true
	for _, c := range b {
		if c >= 'A' && c <= 'Z' || c == 0 {
			plain = false
			break
		}
	}
	if plain {
		return l.str[from:to]
	}
	return lowerName(b)
}

// lowerName lowercases ASCII letters; a NUL becomes U+FFFD.
func lowerName(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			sb.WriteByte(c + 32)
		case c == 0:
			sb.WriteRune(utf8.RuneError)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}
func isLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isHex(c byte) bool    { return isDigit(c) || c|0x20 >= 'a' && c|0x20 <= 'f' }
func isAlnum(c byte) bool  { return isDigit(c) || isLetter(c) }
func hexVal(c byte) byte {
	if isDigit(c) {
		return c - '0'
	}
	return c | 0x20 - 'a' + 10
}

// attrBlock is how many attributes are allocated at once.
const attrBlock = 512

// keepAttrs returns a copy of the attributes of the tag just read, nil if
// there are none, in room shared with the tags to come: one allocation
// for many tags. The copy's capacity is its length, so appending to it
// does not write into the next tag's.
func (l *Lexer) keepAttrs() []Attr {
	n := len(l.attrs)
	if n == 0 {
		return nil
	}
	if n > attrBlock {
		return append([]Attr(nil), l.attrs...)
	}
	if len(l.block) < n {
		p := attrPool.Get().(*[]Attr)
		l.taken = append(l.taken, p)
		l.block = *p
	}
	a := l.block[:n:n]
	copy(a, l.attrs)
	l.block = l.block[n:]
	return a
}

// attrPool holds blocks of attributes for reuse from one document to the
// next.
var attrPool = sync.Pool{New: func() any { b := make([]Attr, attrBlock); return &b }}

// Release gives the blocks of attributes back for the next document: the
// attributes of every token the lexer returned are no longer valid. The
// blocks are cleared, so that they keep nothing of this document.
func (l *Lexer) Release() {
	for _, p := range l.taken {
		clear(*p)
		attrPool.Put(p)
	}
	l.taken, l.block = nil, nil
}
