package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/charset"
	"github.com/AOShei/go-fast-html/internal/yaml"
)

// AttrDocument is the number of the YAML document (from 1) a block is
// in, in a file of several documents.
const AttrDocument = "document"

// maxKeyLabel bounds the text of a complex YAML key (a collection used as
// a key) written in a path.
const maxKeyLabel = 256

// yamlStructure reads decoded YAML into part by its structure, as
// jsonStructure does JSON. It returns no splitter, having made nothing,
// when the text is not YAML; the caller reads it as lines.
func (e *Extractor) yamlStructure(ctx context.Context, src []byte, m *charset.Map,
	part *gofastcore.Part, text *gofastcore.TextBudget) (*dSplitter, *yaml.SyntaxError, error) {
	// A first reading: is it YAML, and how many documents.
	docs := 0
	p := yaml.NewParser(src, yaml.Limits{})
	for n := 0; ; n++ {
		if n&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		ev, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			var se *yaml.SyntaxError
			if errors.As(err, &se) {
				return nil, se, nil
			}
			return nil, &yaml.SyntaxError{Msg: err.Error()}, nil
		}
		if ev.Kind == yaml.DocumentStart {
			docs++
		}
	}
	s := &dSplitter{ctx: ctx, src: src, m: m, format: "yaml", limit: dataUnitBytes, text: text, part: part, head: -1,
		comment: yamlComment}
	s.keyText = func(k keyRef) string { return k.s }
	s.cellText = func(c dCell) string { return c.text }

	// The containers open: for a mapping, whether the next node is a key.
	type open struct {
		mapping bool
		atKey   bool
	}
	var (
		stack    []open
		keyStart int
		key      string
		skip     int // the depth inside a complex key being skipped
		skipFrom int
		doc      int
	)
	p = yaml.NewParser(src, yaml.Limits{})
	for n := 0; ; n++ {
		if n&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		ev, err := p.Next()
		if err != nil {
			break // io.EOF: the first reading found no error
		}
		if skip > 0 {
			// Inside a key that is a collection: its text is the key.
			switch ev.Kind {
			case yaml.MappingStart, yaml.SequenceStart:
				skip++
			case yaml.MappingEnd, yaml.SequenceEnd:
				skip--
				if skip == 0 {
					key = keyLabel(src[skipFrom:ev.End])
				}
			}
			continue
		}
		switch ev.Kind {
		case yaml.DocumentStart:
			// Comments before a "---" (or a directive) are the end of the
			// document before; those before an implicit document's content
			// are its own.
			if ev.Explicit {
				s.trailing(ev.Start)
			}
			doc++
			if docs > 1 {
				s.doc = strconv.Itoa(doc)
			}
			continue
		case yaml.StreamEnd:
			s.trailing(len(src))
			continue
		case yaml.StreamStart, yaml.DocumentEnd:
			continue
		}
		// A key of a mapping is not a member: it is remembered for its value.
		if k := len(stack) - 1; k >= 0 && stack[k].mapping && stack[k].atKey && ev.Kind != yaml.MappingEnd {
			stack[k].atKey = false
			keyStart = ev.PropStart
			switch ev.Kind {
			case yaml.MappingStart, yaml.SequenceStart:
				skip, skipFrom = 1, ev.PropStart
			case yaml.Alias:
				key = "*" + ev.Value
			default:
				key = ev.Value
			}
			continue
		}
		start, hasKey := ev.PropStart, false
		if k := len(stack) - 1; k >= 0 && stack[k].mapping {
			start, hasKey = keyStart, true
			stack[k].atKey = true
		}
		var dev dEvent
		switch ev.Kind {
		case yaml.MappingStart, yaml.SequenceStart:
			// A mapping with an anchor or a tag is not a record: a row
			// would not show them.
			dev = dEvent{kind: evOpen, obj: ev.Kind == yaml.MappingStart, start: start, props: ev.PropStart < ev.Start}
			stack = append(stack, open{mapping: ev.Kind == yaml.MappingStart, atKey: true})
		case yaml.MappingEnd, yaml.SequenceEnd:
			stack = stack[:len(stack)-1]
			if k := len(stack) - 1; k >= 0 && stack[k].mapping {
				stack[k].atKey = true
			}
			dev = dEvent{kind: evClose, end: ev.End}
			hasKey = false
		case yaml.Scalar, yaml.Alias:
			cell := yamlCell(ev)
			if ev.Kind == yaml.Scalar && ev.PropStart < ev.Start {
				// The anchor and tag as written, before the value.
				cell = strings.TrimSpace(string(src[ev.PropStart:ev.Start])) + " " + cell
			}
			dev = dEvent{kind: evScalar, start: start, end: ev.End, cell: dCell{text: cell, set: true}}
		}
		if hasKey {
			dev.key, dev.hasKey = keyRef{s: key, set: true}, true
		}
		if !s.event(dev) {
			break
		}
	}
	return s, nil, nil
}

// yamlCell writes a scalar as a table cell: a quoted one in its quotes,
// an alias as written.
func yamlCell(ev yaml.Event) string {
	switch {
	case ev.Kind == yaml.Alias:
		return "*" + ev.Value
	case ev.Style == yaml.SingleQuoted:
		return "'" + ev.Value + "'"
	case ev.Style == yaml.DoubleQuoted:
		return `"` + ev.Value + `"`
	}
	return ev.Value
}

// keyLabel writes a complex key's text on one line, bounded.
func keyLabel(b []byte) string {
	s := strings.Join(strings.Fields(string(b[:min(len(b), 4*maxKeyLabel)])), " ")
	if len(s) > maxKeyLabel {
		n := maxKeyLabel
		for n > 0 && s[n]&0xC0 == 0x80 {
			n--
		}
		s = s[:n]
	}
	return s
}

// yamlComment reports whether a line is only a comment.
func yamlComment(line []byte) bool {
	t := bytes.TrimLeft(line, " \t")
	return len(t) > 0 && t[0] == '#'
}
