package yaml

import "unicode/utf8"

// tokenKind is the kind of a token of the scanner.
type tokenKind uint8

const (
	tokNone    tokenKind = iota // a reserved slot that was not needed
	tokPending                  // a reserved slot not yet decided
	tokStreamStart
	tokStreamEnd
	tokVersionDirective
	tokTagDirective
	tokReservedDirective
	tokDocumentStart
	tokDocumentEnd
	tokBlockSequenceStart
	tokBlockMappingStart
	tokBlockEnd
	tokFlowSequenceStart
	tokFlowSequenceEnd
	tokFlowMappingStart
	tokFlowMappingEnd
	tokBlockEntry
	tokFlowEntry
	tokKey
	tokValue
	tokAlias
	tokAnchor
	tokTag
	tokScalar
)

// mark is a position in the input: byte offset, 0-based line and
// 0-based column counted in characters.
type mark struct{ pos, line, col int }

type token struct {
	kind       tokenKind
	style      ScalarStyle
	start, end mark
	value      string // scalar value, anchor or alias name, tag suffix, directive version or tag prefix
	handle     string // tag handle, %TAG handle
	level      int    // tokPending: the flow level of the simple key that reserved it
}

// simpleKey is a place where an implicit key may begin. Two token slots are
// reserved there when it is saved (one for a block mapping start, one for
// the key), so that resolving it never inserts into the token queue.
type simpleKey struct {
	possible bool
	required bool
	tab      bool // a tab stood in the indentation before it
	flowMap  bool // directly inside a flow mapping: may span lines
	num      int  // absolute number of the first reserved slot
	mark     mark
}

// maxKeyLength is the spec's bound on an implicit key, in characters.
const maxKeyLength = 1024

type scanner struct {
	src             []byte
	pos, line, col  int
	maxDepth        int
	tokens          []token
	head            int
	base            int // absolute number of tokens[0]
	started, ended  bool
	indent          int
	indents         []int
	allowed         bool // a simple key may start here
	keys            []simpleKey
	flowMap         []bool // kind of each open flow collection
	flowLevel       int
	err             error
	json            bool // the last token was a quoted scalar or a flow collection end
	lineStart       bool // no token yet on the current line
	lineSpaces      int  // spaces at the start of the current line before any tab
	leadTab         bool // a tab stood in the leading whitespace of this line
	tabIndent       bool // a tab stood in indentation position before the next token
	afterInd        bool // the last token was "-", "?" or ":" on this line
	curTab          bool // tabIndent as it was before the current token
	spaced          bool // whitespace or a line start precedes the position
	buf, ws, lb, tb []byte
}

func (s *scanner) here() mark { return mark{s.pos, s.line, s.col} }

func (s *scanner) reset(m mark) { s.pos, s.line, s.col = m.pos, m.line, m.col }

func (s *scanner) fail(m mark, msg string) {
	if s.err == nil {
		s.err = &SyntaxError{Offset: m.pos, Line: m.line + 1, Column: m.col + 1, Msg: msg}
	}
}

func (s *scanner) failDepth(m mark) {
	if s.err == nil {
		s.err = &SyntaxError{Offset: m.pos, Line: m.line + 1, Column: m.col + 1,
			Msg: "collections nested deeper than the limit", depth: true}
	}
}

// at returns the byte k bytes ahead, 0 past the end.
func (s *scanner) at(k int) byte {
	if s.pos+k < len(s.src) {
		return s.src[s.pos+k]
	}
	return 0
}

func (s *scanner) eof() bool { return s.pos >= len(s.src) }

func (s *scanner) isBlank(k int) bool { c := s.at(k); return c == ' ' || c == '\t' }

func (s *scanner) isBreak(k int) bool { c := s.at(k); return c == '\n' || c == '\r' }

func (s *scanner) isBreakz(k int) bool { return s.isBreak(k) || s.pos+k >= len(s.src) }

func (s *scanner) isBlankz(k int) bool {
	c := s.at(k)
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || s.pos+k >= len(s.src)
}

func isFlowInd(c byte) bool { return c == ',' || c == '[' || c == ']' || c == '{' || c == '}' }

// plainSafe reports whether the byte k ahead may continue a plain scalar
// after an indicator.
func (s *scanner) plainSafe(k int) bool {
	if s.isBlankz(k) {
		return false
	}
	return s.flowLevel == 0 || !isFlowInd(s.at(k))
}

func runeLen(c byte) int {
	switch {
	case c < 0xC0:
		return 1
	case c < 0xE0:
		return 2
	case c < 0xF0:
		return 3
	}
	return 4
}

// skip advances over one character (not a line break).
func (s *scanner) skip() {
	n := runeLen(s.src[s.pos])
	s.pos += n
	if s.pos > len(s.src) {
		s.pos = len(s.src)
	}
	s.col++
}

// skipBreak advances over one line break.
func (s *scanner) skipBreak() {
	if s.src[s.pos] == '\r' && s.at(1) == '\n' {
		s.pos += 2
	} else {
		s.pos++
	}
	s.line++
	s.col = 0
}

// copyChar appends one character to b and advances.
func (s *scanner) copyChar(b []byte) []byte {
	p := s.pos
	s.skip()
	return append(b, s.src[p:s.pos]...)
}

func (s *scanner) docMarker() bool {
	if s.col != 0 || s.pos+3 > len(s.src) {
		return false
	}
	c := s.src[s.pos]
	if (c != '-' && c != '.') || s.src[s.pos+1] != c || s.src[s.pos+2] != c {
		return false
	}
	return s.isBlankz(3)
}

func (s *scanner) skipBOM() {
	if s.at(0) == 0xEF && s.at(1) == 0xBB && s.at(2) == 0xBF {
		s.pos += 3
	}
}

// add appends a token.
func (s *scanner) add(t token) { s.tokens = append(s.tokens, t) }

func (s *scanner) slot(num int) *token { return &s.tokens[num-s.base] }

// peek returns the next token, fetching as many as needed.
func (s *scanner) peek() *token {
	for {
		if s.err != nil {
			return nil
		}
		for s.head < len(s.tokens) && s.tokens[s.head].kind == tokNone {
			s.head++
		}
		if s.head < len(s.tokens) {
			t := &s.tokens[s.head]
			if t.kind != tokPending {
				return t
			}
			s.staleCheck(t.level)
			if s.err != nil {
				return nil
			}
			if t.kind != tokPending {
				continue
			}
		} else if s.ended {
			return nil
		}
		s.fetchNextToken()
	}
}

// next drops the current token.
func (s *scanner) next() {
	s.head++
	if s.head == len(s.tokens) {
		s.base += s.head
		s.tokens = s.tokens[:0]
		s.head = 0
	} else if s.head > 4096 && s.head*2 > len(s.tokens) {
		n := copy(s.tokens, s.tokens[s.head:])
		s.tokens = s.tokens[:n]
		s.base += s.head
		s.head = 0
	}
}

// ---- simple keys ----

func (s *scanner) stale(k *simpleKey) bool {
	if !k.possible || k.flowMap {
		return false
	}
	return k.mark.line < s.line || s.col-k.mark.col > maxKeyLength
}

func (s *scanner) staleCheck(level int) {
	if level < len(s.keys) {
		k := &s.keys[level]
		if s.stale(k) {
			s.dropKey(k)
		}
	}
}

// dropKey marks a key impossible and frees its slots.
func (s *scanner) dropKey(k *simpleKey) {
	if !k.possible {
		return
	}
	if k.required {
		s.fail(k.mark, "could not find expected ':'")
	}
	k.possible = false
	s.slot(k.num).kind = tokNone
	s.slot(k.num + 1).kind = tokNone
}

func (s *scanner) saveKey() {
	if !s.allowed {
		return
	}
	k := &s.keys[s.flowLevel]
	s.dropKey(k)
	if s.err != nil {
		return
	}
	m := s.here()
	*k = simpleKey{
		possible: true,
		required: s.flowLevel == 0 && s.indent == s.col,
		tab:      s.curTab,
		flowMap:  s.flowLevel > 0 && s.flowMap[s.flowLevel-1],
		num:      s.base + len(s.tokens),
		mark:     m,
	}
	s.add(token{kind: tokPending, start: m, end: m, level: s.flowLevel})
	s.add(token{kind: tokPending, start: m, end: m, level: s.flowLevel})
}

// ---- indentation ----

func (s *scanner) rollIndent(col int, kind tokenKind, m mark) bool {
	if s.flowLevel > 0 || s.indent >= col {
		return false
	}
	s.indents = append(s.indents, s.indent)
	s.indent = col
	if len(s.indents) > s.maxDepth {
		s.failDepth(m)
	}
	s.add(token{kind: kind, start: m, end: m})
	return true
}

func (s *scanner) unrollIndent(col int) {
	if s.flowLevel > 0 {
		return
	}
	m := s.here()
	for s.indent > col {
		s.add(token{kind: tokBlockEnd, start: m, end: m})
		s.indent = s.indents[len(s.indents)-1]
		s.indents = s.indents[:len(s.indents)-1]
	}
}

// ---- whitespace ----

func (s *scanner) scanToNextToken() {
	for {
		if s.col == 0 && s.flowLevel == 0 {
			s.skipBOM()
		}
		for s.pos < len(s.src) {
			c := s.src[s.pos]
			if c == ' ' {
				if s.lineStart && !s.leadTab {
					s.lineSpaces++
				}
			} else if c == '\t' {
				if s.lineStart {
					s.leadTab = true
				}
				if s.lineStart || s.afterInd {
					s.tabIndent = true
				}
			} else {
				break
			}
			s.pos++
			s.col++
			s.spaced = true
		}
		if s.at(0) == '#' && !s.eof() {
			if !s.spaced {
				s.fail(s.here(), "a comment must be separated from other tokens by white space")
				return
			}
			for !s.isBreakz(0) {
				s.skip()
			}
		}
		if s.isBreak(0) {
			s.skipBreak()
			if s.flowLevel == 0 {
				s.allowed = true
			}
			s.lineStart = true
			s.lineSpaces = 0
			s.leadTab = false
			s.tabIndent = false
			s.afterInd = false
			s.spaced = true
			continue
		}
		return
	}
}

// ---- token dispatch ----

func (s *scanner) fetchNextToken() {
	if !s.started {
		s.started = true
		s.allowed = true
		s.indent = -1
		s.keys = append(s.keys, simpleKey{})
		s.lineStart = true
		s.spaced = true
		s.add(token{kind: tokStreamStart})
		s.skipBOM()
		return
	}
	s.scanToNextToken()
	if s.err != nil {
		return
	}
	s.staleCheck(s.flowLevel)
	if s.err != nil {
		return
	}
	s.unrollIndent(s.col)
	if s.eof() {
		s.fetchStreamEnd()
		return
	}
	lineStart, tab := s.lineStart, s.tabIndent
	s.curTab = tab
	s.lineStart, s.tabIndent, s.afterInd = false, false, false
	if lineStart {
		if s.flowLevel > 0 && s.lineSpaces <= s.indent && !s.docMarker() {
			s.fail(s.here(), "flow content not indented enough")
			return
		}
		if s.flowLevel == 0 && s.leadTab && s.lineSpaces <= s.indent {
			s.fail(s.here(), "a tab character must not be used as indentation")
			return
		}
	}
	defer func() { s.spaced = false }()
	c := s.src[s.pos]
	if s.col == 0 {
		if c == '%' {
			s.fetchDirective()
			return
		}
		if s.docMarker() {
			s.fetchDocIndicator(c == '-')
			return
		}
	}
	isJSON := s.json
	s.json = false
	switch c {
	case '[', '{':
		s.fetchFlowStart(c == '{')
		return
	case ']', '}':
		s.fetchFlowEnd(c == '}')
		return
	case ',':
		s.fetchFlowEntry()
		return
	case '-':
		if s.isBlankz(1) {
			s.blockTab(tab)
			s.fetchBlockEntry()
			return
		}
	case '?':
		if s.isBlankz(1) {
			s.blockTab(tab)
			s.fetchKey()
			return
		}
	case ':':
		if s.isBlankz(1) || s.flowLevel > 0 && (isJSON || isFlowInd(s.at(1))) {
			s.blockTab(tab)
			s.fetchValue()
			return
		}
	case '*':
		s.fetchAnchor(tokAlias)
		return
	case '&':
		s.fetchAnchor(tokAnchor)
		return
	case '!':
		s.fetchTag()
		return
	case '|', '>':
		if s.flowLevel == 0 {
			s.fetchBlockScalar(c == '|')
			return
		}
	case '\'', '"':
		s.fetchQuoted(c == '\'')
		return
	}
	switch c {
	case '-', '?', ':':
		if s.plainSafe(1) {
			s.fetchPlain()
			return
		}
	case ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
	default:
		if c != 0 {
			s.fetchPlain()
			return
		}
	}
	s.fail(s.here(), "found character that cannot start any token")
}

func (s *scanner) blockTab(tab bool) {
	if tab && s.flowLevel == 0 {
		s.fail(s.here(), "a tab character must not be used as indentation")
	}
}

func (s *scanner) fetchStreamEnd() {
	s.unrollIndent(-1)
	for i := len(s.keys) - 1; i >= 0; i-- {
		s.dropKey(&s.keys[i])
	}
	s.allowed = false
	m := s.here()
	s.add(token{kind: tokStreamEnd, start: m, end: m})
	s.ended = true
}

func (s *scanner) fetchDocIndicator(start bool) {
	s.unrollIndent(-1)
	s.dropKey(&s.keys[s.flowLevel])
	s.allowed = false
	m := s.here()
	s.pos += 3
	s.col += 3
	k := tokDocumentEnd
	if start {
		k = tokDocumentStart
	}
	s.add(token{kind: k, start: m, end: s.here()})
}

func (s *scanner) fetchFlowStart(mapping bool) {
	s.saveKey()
	s.flowLevel++
	if s.flowLevel > s.maxDepth {
		s.failDepth(s.here())
		return
	}
	s.keys = append(s.keys, simpleKey{})
	s.flowMap = append(s.flowMap, mapping)
	s.allowed = true
	m := s.here()
	s.skip()
	k := tokFlowSequenceStart
	if mapping {
		k = tokFlowMappingStart
	}
	s.add(token{kind: k, start: m, end: s.here()})
}

func (s *scanner) fetchFlowEnd(mapping bool) {
	s.dropKey(&s.keys[s.flowLevel])
	if s.flowLevel > 0 {
		s.flowLevel--
		s.keys = s.keys[:len(s.keys)-1]
		s.flowMap = s.flowMap[:len(s.flowMap)-1]
	}
	s.allowed = false
	s.json = true
	m := s.here()
	s.skip()
	k := tokFlowSequenceEnd
	if mapping {
		k = tokFlowMappingEnd
	}
	s.add(token{kind: k, start: m, end: s.here()})
}

func (s *scanner) fetchFlowEntry() {
	s.dropKey(&s.keys[s.flowLevel])
	s.allowed = true
	m := s.here()
	s.skip()
	s.add(token{kind: tokFlowEntry, start: m, end: s.here()})
}

func (s *scanner) fetchBlockEntry() {
	m := s.here()
	if s.flowLevel == 0 {
		if !s.allowed {
			s.fail(m, "block sequence entries are not allowed in this context")
			return
		}
		s.rollIndent(s.col, tokBlockSequenceStart, m)
	}
	s.dropKey(&s.keys[s.flowLevel])
	s.allowed = true
	s.skip()
	s.afterInd = true
	s.add(token{kind: tokBlockEntry, start: m, end: s.here()})
}

func (s *scanner) fetchKey() {
	m := s.here()
	if s.flowLevel == 0 {
		if !s.allowed {
			s.fail(m, "mapping keys are not allowed in this context")
			return
		}
		s.rollIndent(s.col, tokBlockMappingStart, m)
	}
	s.dropKey(&s.keys[s.flowLevel])
	s.allowed = s.flowLevel == 0
	s.skip()
	s.afterInd = true
	s.add(token{kind: tokKey, start: m, end: s.here()})
}

func (s *scanner) fetchValue() {
	m := s.here()
	k := &s.keys[s.flowLevel]
	if s.stale(k) {
		s.dropKey(k)
		if s.err != nil {
			return
		}
	}
	if k.possible {
		if k.tab && s.flowLevel == 0 {
			s.fail(k.mark, "a tab character must not be used as indentation")
			return
		}
		key := s.slot(k.num + 1)
		*key = token{kind: tokKey, start: k.mark, end: k.mark}
		bms := s.slot(k.num)
		if s.flowLevel == 0 && s.indent < k.mark.col {
			s.indents = append(s.indents, s.indent)
			s.indent = k.mark.col
			if len(s.indents) > s.maxDepth {
				s.failDepth(k.mark)
			}
			*bms = token{kind: tokBlockMappingStart, start: k.mark, end: k.mark}
		} else {
			bms.kind = tokNone
		}
		k.possible = false
		// After an implicit key's ":" a block collection may not start on
		// the same line.
		s.allowed = false
	} else {
		if s.flowLevel == 0 {
			if !s.allowed {
				s.fail(m, "mapping values are not allowed in this context")
				return
			}
			s.rollIndent(s.col, tokBlockMappingStart, m)
		}
		s.allowed = s.flowLevel == 0
	}
	s.skip()
	s.afterInd = true
	s.add(token{kind: tokValue, start: m, end: s.here()})
}

func (s *scanner) fetchAnchor(kind tokenKind) {
	s.saveKey()
	s.allowed = false
	m := s.here()
	s.skip()
	p := s.pos
	for !s.isBlankz(0) && !isFlowInd(s.at(0)) {
		s.skip()
	}
	if s.pos == p {
		s.fail(m, "found an anchor or alias without a name")
		return
	}
	s.add(token{kind: kind, start: m, end: s.here(), value: string(s.src[p:s.pos])})
}

// ---- tags ----

func isWord(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-'
}

// isURIChar reports whether c may stand in a tag URI; tagChar excludes
// "!" and the flow indicators, as in a tag shorthand's suffix.
func isURIChar(c byte, tagChar bool) bool {
	if isWord(c) || c >= 0x80 {
		return true
	}
	switch c {
	case '#', ';', '/', '?', ':', '@', '&', '=', '+', '$', '_', '.', '~', '*', '\'', '(', ')', '%':
		return true
	case '!', ',', '[', ']':
		return !tagChar
	}
	return false
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// scanURI reads URI characters, decoding %-escapes, onto b.
func (s *scanner) scanURI(b []byte, tagChar bool, m mark) []byte {
	for s.pos < len(s.src) && isURIChar(s.src[s.pos], tagChar) {
		c := s.src[s.pos]
		if c == '%' {
			h, l := unhex(s.at(1)), unhex(s.at(2))
			if h < 0 || l < 0 {
				s.fail(m, "found an invalid %-escape in a tag")
				return b
			}
			b = append(b, byte(h<<4|l))
			s.pos += 3
			s.col += 3
			continue
		}
		b = s.copyChar(b)
	}
	return b
}

func (s *scanner) fetchTag() {
	s.saveKey()
	s.allowed = false
	m := s.here()
	var handle, suffix string
	if s.at(1) == '<' {
		s.skip()
		s.skip()
		b := s.scanURI(s.buf[:0], false, m)
		s.buf = b
		if s.at(0) != '>' || len(b) == 0 {
			s.fail(m, "did not find the expected '>' of a verbatim tag")
			return
		}
		s.skip()
		suffix = string(b)
	} else {
		s.skip()
		p := s.pos
		for isWord(s.at(0)) {
			s.skip()
		}
		if s.at(0) == '!' && s.pos > p || s.at(0) == '!' && s.pos == p {
			s.skip()
			handle = string(s.src[p-1 : s.pos])
			b := s.scanURI(s.buf[:0], true, m)
			s.buf = b
			if len(b) == 0 && handle != "!!" {
				s.fail(m, "did not find the expected tag suffix")
				return
			}
			suffix = string(b)
			if len(b) == 0 {
				s.fail(m, "did not find the expected tag suffix")
				return
			}
		} else {
			s.reset(mark{p, s.line, m.col + 1})
			b := s.scanURI(s.buf[:0], true, m)
			s.buf = b
			if len(b) == 0 {
				handle, suffix = "", "!"
			} else {
				handle, suffix = "!", string(b)
			}
		}
	}
	if s.err != nil {
		return
	}
	if !s.isBlankz(0) && !(s.flowLevel > 0 && isFlowInd(s.at(0))) {
		s.fail(m, "did not find expected whitespace or line break after a tag")
		return
	}
	s.add(token{kind: tokTag, start: m, end: s.here(), handle: handle, value: suffix})
}

// ---- directives ----

func (s *scanner) skipBlanks() bool {
	p := s.pos
	for s.isBlank(0) {
		s.skip()
	}
	return s.pos > p
}

func (s *scanner) fetchDirective() {
	s.unrollIndent(-1)
	s.dropKey(&s.keys[s.flowLevel])
	s.allowed = false
	m := s.here()
	s.skip()
	p := s.pos
	for !s.isBlankz(0) {
		s.skip()
	}
	name := string(s.src[p:s.pos])
	t := token{kind: tokReservedDirective, start: m}
	switch name {
	case "YAML":
		if !s.skipBlanks() {
			s.fail(m, "did not find expected version number")
			return
		}
		q := s.pos
		n := 0
		for s.at(0) >= '0' && s.at(0) <= '9' {
			s.skip()
			n++
		}
		if n == 0 || s.at(0) != '.' {
			s.fail(m, "did not find expected version number")
			return
		}
		s.skip()
		n = 0
		for s.at(0) >= '0' && s.at(0) <= '9' {
			s.skip()
			n++
		}
		if n == 0 {
			s.fail(m, "did not find expected version number")
			return
		}
		t.kind = tokVersionDirective
		t.value = string(s.src[q:s.pos])
	case "TAG":
		if !s.skipBlanks() || s.at(0) != '!' {
			s.fail(m, "did not find expected tag handle")
			return
		}
		q := s.pos
		s.skip()
		for isWord(s.at(0)) {
			s.skip()
		}
		if s.at(0) == '!' {
			s.skip()
		} else if s.pos-q > 1 {
			s.fail(m, "did not find expected '!' ending a tag handle")
			return
		}
		t.handle = string(s.src[q:s.pos])
		if !s.skipBlanks() {
			s.fail(m, "did not find expected tag prefix")
			return
		}
		b := s.scanURI(s.buf[:0], false, m)
		s.buf = b
		if len(b) == 0 {
			s.fail(m, "did not find expected tag prefix")
			return
		}
		t.kind = tokTagDirective
		t.value = string(b)
	default:
		if name == "" {
			s.fail(m, "did not find expected directive name")
			return
		}
		for !s.isBreakz(0) && !(s.at(0) == '#' && s.isBlank(-1)) {
			s.skip()
		}
		for s.pos > m.pos && s.isBlank(-1) {
			s.pos--
			s.col--
		}
	}
	if s.err != nil {
		return
	}
	t.end = s.here()
	s.endOfLine(m)
	s.add(t)
}

// endOfLine accepts blanks and a comment up to a line break.
func (s *scanner) endOfLine(m mark) {
	sp := s.skipBlanks()
	if s.at(0) == '#' && !s.eof() {
		if !sp {
			s.fail(s.here(), "a comment must be separated from other tokens by white space")
			return
		}
		for !s.isBreakz(0) {
			s.skip()
		}
	}
	if !s.isBreakz(0) {
		s.fail(s.here(), "did not find expected comment or line break")
	}
}

// ---- scalars ----

func (s *scanner) fetchPlain() {
	s.saveKey()
	s.allowed = false
	start := s.here()
	end := start
	indent := s.indent + 1
	b := s.buf[:0]
	ws, lb, tb := s.ws[:0], s.lb[:0], s.tb[:0]
	leading := false
	simple := true // the value is the input text from start to end
outer:
	for {
		if s.docMarker() || s.at(0) == '#' {
			break
		}
		for !s.isBlankz(0) {
			c := s.src[s.pos]
			if c == ':' && (s.isBlankz(1) || s.flowLevel > 0 && isFlowInd(s.at(1))) {
				break
			}
			if s.flowLevel > 0 && isFlowInd(c) {
				break
			}
			if leading || len(ws) > 0 {
				if simple {
					b = append(b, s.src[start.pos:end.pos]...)
					simple = false
				}
				if leading {
					if len(lb) > 0 && lb[0] == '\n' && len(tb) == 0 {
						b = append(b, ' ')
					} else {
						b = append(b, tb...)
					}
					lb, tb = lb[:0], tb[:0]
					leading = false
				} else {
					b = append(b, ws...)
					ws = ws[:0]
				}
			}
			if simple {
				s.skip()
			} else {
				b = s.copyChar(b)
			}
			end = s.here()
		}
		if !s.isBlank(0) && !s.isBreak(0) {
			break
		}
		for s.isBlank(0) || s.isBreak(0) {
			if s.isBlank(0) {
				if leading && s.col < indent && s.at(0) == '\t' {
					break outer
				}
				if !leading {
					ws = append(ws, s.src[s.pos])
				}
				s.skip()
			} else {
				if !leading {
					ws = ws[:0]
					lb = append(lb, '\n')
					leading = true
				} else {
					tb = append(tb, '\n')
				}
				s.skipBreak()
			}
		}
		if s.flowLevel == 0 && s.col < indent {
			break
		}
	}
	s.reset(end)
	s.ws, s.lb, s.tb = ws, lb, tb
	var v string
	if simple {
		v = string(s.src[start.pos:end.pos])
	} else {
		v = string(b)
	}
	s.buf = b
	s.add(token{kind: tokScalar, style: Plain, start: start, end: end, value: v})
}

var escapes = [256]rune{
	'0': 0, 'a': 7, 'b': 8, 't': 9, '\t': 9, 'n': 10, 'v': 11, 'f': 12, 'r': 13,
	'e': 0x1B, ' ': ' ', '"': '"', '/': '/', '\\': '\\', 'N': 0x85, '_': 0xA0,
	'L': 0x2028, 'P': 0x2029,
}

func validEscape(c byte) bool {
	switch c {
	case '0', 'a', 'b', 't', '\t', 'n', 'v', 'f', 'r', 'e', ' ', '"', '/', '\\', 'N', '_', 'L', 'P':
		return true
	}
	return false
}

func (s *scanner) fetchQuoted(single bool) {
	s.saveKey()
	s.allowed = false
	s.json = true
	start := s.here()
	q := s.src[s.pos]
	s.skip()
	b := s.buf[:0]
	ws, lb, tb := s.ws[:0], s.lb[:0], s.tb[:0]
	for {
		if s.docMarker() {
			s.fail(s.here(), "found unexpected document indicator in a quoted scalar")
			return
		}
		if s.eof() {
			s.fail(start, "found unexpected end of stream in a quoted scalar")
			return
		}
		leading := false
		for !s.isBlankz(0) {
			c := s.src[s.pos]
			if single && c == '\'' && s.at(1) == '\'' {
				b = append(b, '\'')
				s.pos += 2
				s.col += 2
				continue
			}
			if c == q {
				break
			}
			if !single && c == '\\' && s.isBreak(1) {
				s.skip()
				s.skipBreak()
				leading = true
				break
			}
			if !single && c == '\\' {
				e := s.at(1)
				n := 0
				switch e {
				case 'x':
					n = 2
				case 'u':
					n = 4
				case 'U':
					n = 8
				default:
					if !validEscape(e) || s.pos+1 >= len(s.src) {
						s.fail(s.here(), "found unknown escape character")
						return
					}
					b = utf8.AppendRune(b, escapes[e])
					s.pos += 2
					s.col += 2
					continue
				}
				r := 0
				for i := 0; i < n; i++ {
					h := unhex(s.at(2 + i))
					if h < 0 {
						s.fail(s.here(), "did not find expected hexadecimal number")
						return
					}
					r = r<<4 | h
				}
				if r >= 0xD800 && r <= 0xDFFF || r > 0x10FFFF {
					s.fail(s.here(), "found invalid Unicode character escape code")
					return
				}
				b = utf8.AppendRune(b, rune(r))
				s.pos += 2 + n
				s.col += 2 + n
				continue
			}
			b = s.copyChar(b)
		}
		if s.eof() {
			s.fail(start, "found unexpected end of stream in a quoted scalar")
			return
		}
		if s.src[s.pos] == q {
			break
		}
		ws, lb, tb = ws[:0], lb[:0], tb[:0]
		spaces, tabbed := 0, false
		broke := leading
		for s.isBlank(0) || s.isBreak(0) {
			if s.isBlank(0) {
				if s.at(0) == '\t' {
					tabbed = true
				} else if !tabbed {
					spaces++
				}
				if !leading {
					ws = append(ws, s.src[s.pos])
				}
				s.skip()
			} else {
				if !leading {
					ws = ws[:0]
					lb = append(lb, '\n')
					leading = true
				} else {
					tb = append(tb, '\n')
				}
				s.skipBreak()
				broke = true
				spaces, tabbed = 0, false
			}
		}
		if broke && !s.eof() && !s.docMarker() && spaces <= s.indent {
			s.fail(s.here(), "a quoted scalar's continuation line is not indented enough")
			return
		}
		if leading {
			if len(lb) > 0 && lb[0] == '\n' && len(tb) == 0 {
				b = append(b, ' ')
			} else {
				b = append(b, tb...)
			}
		} else {
			b = append(b, ws...)
		}
	}
	s.skip()
	s.buf, s.ws, s.lb, s.tb = b, ws, lb, tb
	st := DoubleQuoted
	if single {
		st = SingleQuoted
	}
	s.add(token{kind: tokScalar, style: st, start: start, end: s.here(), value: string(b)})
}

func (s *scanner) fetchBlockScalar(literal bool) {
	s.dropKey(&s.keys[s.flowLevel])
	s.allowed = true
	start := s.here()
	s.skip()
	chomp, incr := 0, 0
	for i := 0; i < 2; i++ {
		c := s.at(0)
		if (c == '+' || c == '-') && chomp == 0 {
			chomp = 1
			if c == '-' {
				chomp = -1
			}
			s.skip()
		} else if c >= '0' && c <= '9' && incr == 0 {
			if c == '0' {
				s.fail(s.here(), "found an indentation indicator equal to 0")
				return
			}
			incr = int(c - '0')
			s.skip()
		}
	}
	end := s.here()
	s.endOfLine(start)
	if s.err != nil {
		return
	}
	if s.isBreak(0) {
		s.skipBreak()
	}
	minIndent := s.indent + 1
	indent := -1
	if incr > 0 {
		indent = s.indent + incr
		if indent < 0 {
			indent = 0
		}
	}
	b := s.buf[:0]
	lb, tb := s.lb[:0], s.tb[:0]
	lineStart := s.here()
	indent, tb = s.blockBreaks(indent, minIndent, tb, &lineStart)
	if s.err != nil {
		return
	}
	leadingBlank := false
	for s.col == indent && !s.eof() && !s.docMarker() {
		trailingBlank := s.isBlank(0)
		if !literal && len(lb) > 0 && lb[0] == '\n' && !leadingBlank && !trailingBlank {
			if len(tb) == 0 {
				b = append(b, ' ')
			}
			lb = lb[:0]
		} else {
			b = append(b, lb...)
			lb = lb[:0]
		}
		b = append(b, tb...)
		tb = tb[:0]
		leadingBlank = s.isBlank(0)
		p := s.pos
		for !s.isBreakz(0) {
			s.skip()
		}
		b = append(b, s.src[p:s.pos]...)
		end = s.here()
		if s.eof() {
			lineStart = end
			break
		}
		lb = append(lb, '\n')
		s.skipBreak()
		lineStart = s.here()
		indent, tb = s.blockBreaks(indent, minIndent, tb, &lineStart)
		if s.err != nil {
			return
		}
	}
	if chomp != -1 {
		b = append(b, lb...)
	}
	if chomp == 1 {
		b = append(b, tb...)
	}
	s.reset(lineStart)
	s.buf, s.lb, s.tb = b, lb, tb
	st := Folded
	if literal {
		st = Literal
	}
	s.add(token{kind: tokScalar, style: st, start: start, end: end, value: string(b)})
}

// blockBreaks reads the empty lines of a block scalar, settling its
// indentation if it is not yet known (-1). It leaves the position at the
// first character after the indentation of a non-empty line, with *ls the
// start of that line.
func (s *scanner) blockBreaks(indent, minIndent int, tb []byte, ls *mark) (int, []byte) {
	maxEmpty := 0
	for {
		for (indent < 0 || s.col < indent) && s.at(0) == ' ' {
			s.skip()
		}
		if !s.isBreak(0) {
			break
		}
		if s.col > maxEmpty {
			maxEmpty = s.col
		}
		tb = append(tb, '\n')
		s.skipBreak()
		*ls = s.here()
	}
	if indent < 0 {
		if !s.eof() && s.col >= minIndent && !s.docMarker() {
			indent = s.col
			if maxEmpty > indent {
				s.fail(s.here(), "a leading empty line of a block scalar has more spaces than its first content line")
			}
		} else {
			indent = max(maxEmpty, minIndent)
			if indent == 0 && s.col == 0 && !s.eof() {
				// Nothing at column 0 is content when no line was.
				indent = 1
			}
		}
	}
	return indent, tb
}
