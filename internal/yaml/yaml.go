package yaml

import (
	"errors"
	"fmt"
	"io"
)

// EventKind is the kind of an Event.
type EventKind uint8

const (
	StreamStart EventKind = iota
	StreamEnd
	DocumentStart
	DocumentEnd
	MappingStart
	MappingEnd
	SequenceStart
	SequenceEnd
	Scalar
	Alias
)

var kindNames = [...]string{"StreamStart", "StreamEnd", "DocumentStart", "DocumentEnd",
	"MappingStart", "MappingEnd", "SequenceStart", "SequenceEnd", "Scalar", "Alias"}

func (k EventKind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("EventKind(%d)", k)
}

// ScalarStyle is how a scalar was written.
type ScalarStyle uint8

const (
	Plain ScalarStyle = iota
	SingleQuoted
	DoubleQuoted
	Literal
	Folded
)

// Event is one event of the stream. Start and End are byte offsets into the
// input, half-open; see the package documentation for what they cover.
type Event struct {
	Kind       EventKind
	Start, End int
	// PropStart is the offset of the node's first property (anchor or
	// tag), or Start when it has none. Set on Scalar, MappingStart and
	// SequenceStart.
	PropStart int
	// Value is a scalar's value after folding, escapes and chomping, or an
	// alias's anchor name.
	Value string
	Style ScalarStyle
	// Flow is set on a MappingStart or SequenceStart written in flow style.
	Flow   bool
	Anchor string // without the "&"
	// Tag is the node's tag in full form, "" when it has none, "!" for
	// the non-specific tag.
	Tag string
	// Explicit: DocumentStart had a "---", DocumentEnd had a "...".
	Explicit bool
}

// Limits bounds the parser.
type Limits struct {
	// MaxDepth bounds the nesting of collections, flow and block; 0 means
	// DefaultMaxDepth.
	MaxDepth int
}

// DefaultMaxDepth is the nesting bound when Limits.MaxDepth is 0.
const DefaultMaxDepth = 512

// ErrDepth is matched by errors.Is when collections nest deeper than the
// limit.
var ErrDepth = errors.New("yaml: collections nested deeper than the limit")

// SyntaxError is the error for input the parser cannot accept. Line and
// Column are 1-based; Column counts characters.
type SyntaxError struct {
	Offset, Line, Column int
	Msg                  string
	depth                bool
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("yaml: line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

// Unwrap returns ErrDepth for an error of nesting.
func (e *SyntaxError) Unwrap() error {
	if e.depth {
		return ErrDepth
	}
	return nil
}

// Parser reads events from a YAML stream.
type Parser struct {
	s        scanner
	state    pstate
	states   []pstate
	depth    int
	lastEnd  int
	tagDirs  []tagDir
	version  bool
	docStart int // start of the pending document's directives, -1 when none
	err      error
}

type tagDir struct{ handle, prefix string }

// NewParser returns a parser of src, which must be UTF-8.
func NewParser(src []byte, lim Limits) *Parser {
	if lim.MaxDepth <= 0 {
		lim.MaxDepth = DefaultMaxDepth
	}
	p := &Parser{docStart: -1}
	p.s.src = src
	p.s.maxDepth = lim.MaxDepth
	return p
}

// Next returns the next event. After StreamEnd it returns io.EOF; after an
// error it returns the same error again.
func (p *Parser) Next() (Event, error) {
	if p.err != nil {
		return Event{}, p.err
	}
	if p.state == stEnd {
		return Event{}, io.EOF
	}
	ev, err := p.parse()
	if err == nil && p.s.err != nil {
		err = p.s.err
	}
	if err != nil {
		p.err = err
		return Event{}, err
	}
	switch ev.Kind {
	case Scalar, Alias, MappingEnd, SequenceEnd, DocumentStart:
		p.lastEnd = ev.End
	}
	return ev, nil
}
