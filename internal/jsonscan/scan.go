// Package jsonscan reads the tokens of JSON text with their byte offsets,
// in one pass and without building values: the structure of a document
// (objects, arrays, members, scalars) and where each piece is.
//
// It reads RFC 8259 JSON and the two liberties of JSON with comments
// (JSONC, as tsconfig.json and VS Code's settings write it): comments
// (// to the end of the line, /* to */) and a trailing comma before a
// closing bracket. Raw control characters inside strings, which RFC 8259
// forbids, are accepted. Anything else that is not JSON is an error with
// its offset.
//
// A text may hold several values one after another (JSON Lines, or
// concatenated JSON): each is reported between its Begin and End of
// value, see Scanner.Next.
package jsonscan

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// Kind is the kind of a token.
type Kind uint8

const (
	ObjectStart Kind = iota + 1 // "{": Start at the brace
	ObjectEnd                   // "}": End just after the brace
	ArrayStart                  // "["
	ArrayEnd                    // "]"
	Key                         // a member's name: Start and End around its quotes
	String                      // a string value, quotes included
	Number
	True
	False
	Null
)

// Token is one token, with its span [Start, End) in the input.
type Token struct {
	Kind       Kind
	Start, End int
	// Depth is the number of containers the token is in: 0 for a value at
	// the top level, 1 for a member of the top-level object or an element
	// of the top-level array. A container's start and end tokens have the
	// depth of the container.
	Depth int
}

// Scalar reports whether the token is a value that is not a container.
func (t Token) Scalar() bool { return t.Kind >= String }

// Error is a syntax error at Offset.
type Error struct {
	Offset int
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("offset %d: %s", e.Offset, e.Msg) }

// ErrDepth is the message of an Error for nesting deeper than the bound.
const ErrDepth = "nesting deeper than the bound"

// DefaultMaxDepth bounds the nesting of containers when Scanner.MaxDepth
// is zero.
const DefaultMaxDepth = 512

// state is what the scanner expects next inside a container.
type state uint8

const (
	expValue      state = iota // a value (or, in an array, "]")
	expKey                     // a key (or "}")
	expColon                   // ":" after a key
	expCommaOrEnd              // "," or the closing bracket
)

// Scanner reads the tokens of src.
type Scanner struct {
	src []byte
	pos int
	// MaxDepth bounds how deeply containers nest; zero means
	// DefaultMaxDepth.
	MaxDepth int

	stack []bool // the open containers: true for an object
	st    state
	err   error
}

// New returns a scanner of src.
func New(src []byte) *Scanner { return &Scanner{src: src} }

// Pos is the offset the scanner has reached.
func (s *Scanner) Pos() int { return s.pos }

// Next returns the next token. At the top level, between values, it
// skips white space and comments; when the input ends there it returns
// ok false and a nil error. A syntax error is returned as *Error, and
// every later call returns it again.
func (s *Scanner) Next() (tok Token, ok bool, err error) {
	if s.err != nil {
		return Token{}, false, s.err
	}
	tok, ok, err = s.next()
	if err != nil {
		s.err = err
	}
	return tok, ok, err
}

// Reset makes the scanner continue at pos as at the top level, with no
// container open and no error: a reader of JSON Lines resumes at the next
// line after an error.
func (s *Scanner) Reset(pos int) {
	s.pos, s.stack, s.st, s.err = pos, s.stack[:0], expValue, nil
}

func (s *Scanner) fail(off int, format string, args ...any) (Token, bool, error) {
	return Token{}, false, &Error{Offset: off, Msg: fmt.Sprintf(format, args...)}
}

func (s *Scanner) next() (Token, bool, error) {
	for {
		if err := s.skipSpace(); err != nil {
			return Token{}, false, err
		}
		depth := len(s.stack)
		if s.pos >= len(s.src) {
			if depth > 0 {
				return s.fail(s.pos, "the input ends inside %s", s.inside())
			}
			return Token{}, false, nil
		}
		c := s.src[s.pos]
		if depth > 0 {
			switch s.st {
			case expColon:
				if c != ':' {
					return s.fail(s.pos, "%s where ':' is expected after a key", s.describe())
				}
				s.pos++
				s.st = expValue
				continue
			case expCommaOrEnd:
				switch {
				case c == ',':
					s.pos++
					if s.stack[depth-1] {
						s.st = expKey
					} else {
						s.st = expValue
					}
					continue
				case c == '}' && s.stack[depth-1], c == ']' && !s.stack[depth-1]:
					return s.close(c)
				}
				return s.fail(s.pos, "%s where ',' or the end of %s is expected", s.describe(), s.inside())
			case expKey:
				switch c {
				case '"':
					start := s.pos
					if err := s.skipString(); err != nil {
						return Token{}, false, err
					}
					s.st = expColon
					return Token{Kind: Key, Start: start, End: s.pos, Depth: depth}, true, nil
				case '}':
					return s.close(c)
				}
				return s.fail(s.pos, "%s where a key in quotes is expected", s.describe())
			case expValue:
				if c == ']' && !s.stack[depth-1] {
					return s.close(c)
				}
			}
		}
		return s.value(c, depth)
	}
}

// value reads a value that starts with c at depth.
func (s *Scanner) value(c byte, depth int) (Token, bool, error) {
	start := s.pos
	switch {
	case c == '{' || c == '[':
		limit := s.MaxDepth
		if limit <= 0 {
			limit = DefaultMaxDepth
		}
		if depth >= limit {
			return s.fail(start, ErrDepth)
		}
		s.pos++
		s.stack = append(s.stack, c == '{')
		kind := ArrayStart
		if c == '{' {
			kind = ObjectStart
			s.st = expKey
		} else {
			s.st = expValue
		}
		return Token{Kind: kind, Start: start, End: s.pos, Depth: depth}, true, nil
	case c == '"':
		if err := s.skipString(); err != nil {
			return Token{}, false, err
		}
		return s.scalar(String, start, depth)
	case c == '-' || c >= '0' && c <= '9':
		if err := s.skipNumber(); err != nil {
			return Token{}, false, err
		}
		return s.scalar(Number, start, depth)
	}
	for _, w := range [...]struct {
		word string
		kind Kind
	}{{"true", True}, {"false", False}, {"null", Null}} {
		if bytes.HasPrefix(s.src[s.pos:], []byte(w.word)) {
			end := s.pos + len(w.word)
			if end < len(s.src) && isWordByte(s.src[end]) {
				break
			}
			s.pos = end
			return s.scalar(w.kind, start, depth)
		}
	}
	return s.fail(start, "%s where a value is expected", s.describe())
}

func (s *Scanner) scalar(k Kind, start, depth int) (Token, bool, error) {
	if depth > 0 {
		s.st = expCommaOrEnd
	}
	return Token{Kind: k, Start: start, End: s.pos, Depth: depth}, true, nil
}

// close reads the closing bracket c of the innermost container.
func (s *Scanner) close(c byte) (Token, bool, error) {
	start := s.pos
	s.pos++
	s.stack = s.stack[:len(s.stack)-1]
	depth := len(s.stack)
	if depth > 0 {
		s.st = expCommaOrEnd
	}
	kind := ArrayEnd
	if c == '}' {
		kind = ObjectEnd
	}
	return Token{Kind: kind, Start: start, End: s.pos, Depth: depth}, true, nil
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// skipSpace skips white space and comments.
func (s *Scanner) skipSpace() error {
	for s.pos < len(s.src) {
		switch c := s.src[s.pos]; c {
		case ' ', '\t', '\n', '\r':
			s.pos++
		case '/':
			if s.pos+1 >= len(s.src) {
				return &Error{Offset: s.pos, Msg: "a lone '/'"}
			}
			switch s.src[s.pos+1] {
			case '/':
				for s.pos < len(s.src) && s.src[s.pos] != '\n' && s.src[s.pos] != '\r' {
					s.pos++
				}
			case '*':
				end := indexFrom(s.src, s.pos+2, "*/")
				if end < 0 {
					return &Error{Offset: s.pos, Msg: "a comment that is not closed"}
				}
				s.pos = end + 2
			default:
				return &Error{Offset: s.pos, Msg: "a lone '/'"}
			}
		default:
			if c == 0xEF && s.pos == 0 && len(s.src) >= 3 && s.src[1] == 0xBB && s.src[2] == 0xBF {
				s.pos += 3 // a byte order mark
				continue
			}
			return nil
		}
	}
	return nil
}

func indexFrom(b []byte, from int, sub string) int {
	i := bytes.Index(b[from:], []byte(sub))
	if i < 0 {
		return -1
	}
	return from + i
}

// skipString moves past the string that starts at s.pos.
func (s *Scanner) skipString() error {
	start := s.pos
	i := s.pos + 1
	for i < len(s.src) {
		switch s.src[i] {
		case '"':
			s.pos = i + 1
			return nil
		case '\\':
			if i+1 >= len(s.src) {
				return &Error{Offset: start, Msg: "a string that is not closed"}
			}
			switch s.src[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if i+6 > len(s.src) || !isHex4(s.src[i+2:i+6]) {
					return &Error{Offset: i, Msg: `a \u escape without four hexadecimal digits`}
				}
				i += 6
			default:
				return &Error{Offset: i, Msg: fmt.Sprintf("the escape \\%c", s.src[i+1])}
			}
		default:
			i++
		}
	}
	return &Error{Offset: start, Msg: "a string that is not closed"}
}

func isHex4(b []byte) bool {
	for _, c := range b {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// skipNumber moves past the number at s.pos (RFC 8259's grammar).
func (s *Scanner) skipNumber() error {
	start, i := s.pos, s.pos
	b := s.src
	digits := func() int {
		n := 0
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
			n++
		}
		return n
	}
	if b[i] == '-' {
		i++
	}
	switch {
	case i < len(b) && b[i] == '0':
		i++
	case digits() == 0:
		return &Error{Offset: start, Msg: "a number without digits"}
	}
	if i < len(b) && b[i] == '.' {
		i++
		if digits() == 0 {
			return &Error{Offset: start, Msg: "a number without digits after its point"}
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		if digits() == 0 {
			return &Error{Offset: start, Msg: "a number without digits in its exponent"}
		}
	}
	if i < len(b) && (isWordByte(b[i]) || b[i] == '.') {
		return &Error{Offset: start, Msg: "a number followed by " + strconv.QuoteRune(rune(b[i]))}
	}
	s.pos = i
	return nil
}

// describe names what is at s.pos, for an error message.
func (s *Scanner) describe() string {
	r, _ := utf8.DecodeRune(s.src[s.pos:])
	return strconv.QuoteRune(r)
}

func (s *Scanner) inside() string {
	if s.stack[len(s.stack)-1] {
		return "an object"
	}
	return "an array"
}

// errInvalid is returned by Unquote for text that is not a JSON string.
var errInvalid = errors.New("jsonscan: not a JSON string")

// Unquote returns the value of a JSON string token (quotes included),
// its escapes decoded. A lone surrogate escape becomes U+FFFD.
func Unquote(b []byte) (string, error) {
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return "", errInvalid
	}
	b = b[1 : len(b)-1]
	i := 0
	for i < len(b) && b[i] != '\\' {
		i++
	}
	if i == len(b) {
		return string(b), nil
	}
	out := make([]byte, 0, len(b))
	out = append(out, b[:i]...)
	for i < len(b) {
		c := b[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		if i+1 >= len(b) {
			return "", errInvalid
		}
		e := b[i+1]
		i += 2
		switch e {
		case '"', '\\', '/':
			out = append(out, e)
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, ok := hex4(b, i)
			if !ok {
				return "", errInvalid
			}
			i += 4
			if utf16.IsSurrogate(r) {
				if r2, ok := hex4(b, i+2); ok && i+1 < len(b) && b[i] == '\\' && b[i+1] == 'u' {
					if d := utf16.DecodeRune(r, r2); d != utf8.RuneError {
						out = utf8.AppendRune(out, d)
						i += 6
						continue
					}
				}
				r = utf8.RuneError
			}
			out = utf8.AppendRune(out, r)
		default:
			return "", errInvalid
		}
	}
	return string(out), nil
}

func hex4(b []byte, i int) (rune, bool) {
	if i < 0 || i+4 > len(b) || !isHex4(b[i:i+4]) {
		return 0, false
	}
	n, _ := strconv.ParseUint(string(b[i:i+4]), 16, 32)
	return rune(n), true
}
