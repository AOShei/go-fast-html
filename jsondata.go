package gofasthtml

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/charset"
	"github.com/AOShei/go-fast-html/internal/jsonscan"
)

// jsonLinesName reports whether a file's name says it holds JSON Lines.
func jsonLinesName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jsonl", ".ndjson", ".jsonlines":
		return true
	}
	return false
}

// rawRegion is a stretch of JSON Lines that is not JSON: from the start
// of the line a value began on to the end of the line of its error. deep:
// the error is the nesting bound.
type rawRegion struct {
	start, end, next int
	deep             bool
}

// jsonPlan is what a first reading of a JSON file found.
type jsonPlan struct {
	values  int // top-level values read in full
	regions []rawRegion
	err     *jsonscan.Error // the first error
}

// planJSON reads src once without making anything: how many values, and
// where the text is not JSON. With lines, an error ends at its line and
// reading goes on at the next one.
//
// With oneLine (a file named as JSON Lines) a value must be on one line:
// a value whose error is past the end of the line it began on makes that
// line the region not read as JSON, and reading goes on at the next.
func planJSON(ctx context.Context, src []byte, lines, oneLine bool) (jsonPlan, error) {
	var pl jsonPlan
	sc := jsonscan.New(src)
	valueStart := -1
	for n := 0; ; n++ {
		if n&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return pl, err
			}
		}
		tok, ok, err := sc.Next()
		if err != nil {
			je := err.(*jsonscan.Error)
			if pl.err == nil {
				pl.err = je
			}
			if !lines {
				return pl, nil
			}
			at := je.Offset
			if valueStart >= 0 {
				at = min(at, valueStart)
			}
			end, next := lineAt(src, je.Offset)
			if oneLine {
				// In JSON Lines a value is on one line: when the error is
				// past it, only that line is not JSON.
				if le, ln := lineAt(src, at); je.Offset >= le {
					end, next = le, ln
				}
			}
			pl.regions = append(pl.regions, rawRegion{start: lineStart(src, at), end: end, next: next,
				deep: je.Msg == jsonscan.ErrDepth})
			sc.Reset(next)
			valueStart = -1
			continue
		}
		if !ok {
			return pl, nil
		}
		if tok.Depth == 0 {
			switch tok.Kind {
			case jsonscan.ObjectStart, jsonscan.ArrayStart:
				valueStart = tok.Start
			case jsonscan.ObjectEnd, jsonscan.ArrayEnd:
				pl.values++
				valueStart = -1
			default:
				pl.values++
			}
		}
	}
}

// jsonStructure reads decoded JSON into part by its structure. It
// returns no splitter, having made nothing, when the text is not JSON:
// one value with a syntax error (returned), or no value; the caller reads
// it as lines.
// A file named as JSON Lines, or holding several values, is a sequence
// of values whose lines that are not JSON are blocks as written.
func (e *Extractor) jsonStructure(ctx context.Context, src []byte, m *charset.Map, name string,
	part *gofastcore.Part, text *gofastcore.TextBudget) (*dSplitter, *jsonscan.Error, error) {
	lines := jsonLinesName(name)
	pl, err := planJSON(ctx, src, lines, lines)
	if err != nil {
		return nil, nil, err
	}
	if !lines {
		if pl.err != nil {
			// Several values one after another: JSON Lines in a .json.
			if pl.values < 2 {
				return nil, pl.err, nil
			}
			lines = true
			if pl, err = planJSON(ctx, src, true, false); err != nil {
				return nil, nil, err
			}
		} else if pl.values > 1 {
			lines = true
		}
	}
	if pl.values == 0 && (!lines || len(pl.regions) == 0) {
		return nil, pl.err, nil
	}
	s := &dSplitter{ctx: ctx, src: src, m: m, format: "json", limit: dataUnitBytes, text: text, part: part, head: -1,
		comment: jsonComment}
	s.keyText = func(k keyRef) string {
		if k.set {
			return k.s
		}
		v, err := jsonscan.Unquote(src[k.start:k.end])
		if err != nil {
			return string(src[k.start:k.end])
		}
		return v
	}
	s.cellText = func(c dCell) string {
		if c.set {
			return c.text
		}
		raw := src[c.vstart:c.vend]
		if c.quoted {
			if v, err := jsonscan.Unquote(raw); err == nil {
				return `"` + v + `"`
			}
		}
		return string(raw)
	}
	if lines {
		s.openVirtual()
	}
	sc := jsonscan.New(src)
	var (
		objs   []bool // the open containers: true for an object
		key    keyRef
		hasKey bool
		ri     int
		// deep counts the lines kept as written for their nesting, from
		// the first at deepAt.
		deep, deepAt int
		// gap is where the text after the last token begins; comment
		// says a comment was in it since the last event, in an object.
		gap     int
		comment bool
	)
	for n := 0; ; n++ {
		if n&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		tok, ok, err := sc.Next()
		if lines && len(objs) == 0 && ri < len(pl.regions) {
			// A line that is not JSON: as written.
			r := pl.regions[ri]
			if err != nil || !ok || tok.Start >= r.start {
				ri++
				if !s.event(dEvent{kind: evRaw, start: r.start, end: r.end}) {
					break
				}
				if r.deep {
					if deep == 0 {
						deepAt = r.start
					}
					deep++
				}
				sc.Reset(r.next)
				gap, comment = r.next, false
				continue
			}
		}
		if err != nil || !ok {
			break
		}
		start := tok.Start
		inObj := len(objs) > 0 && objs[len(objs)-1]
		if inObj && start > gap && s.commentIn(gap, start) >= 0 {
			comment = true
		}
		gap = tok.End
		member := func() dEvent {
			ev := dEvent{start: start}
			if inObj && hasKey {
				ev.start, ev.key, ev.hasKey = key.start, key, true
				hasKey = false
			}
			return ev
		}
		var ev dEvent
		switch tok.Kind {
		case jsonscan.Key:
			key, hasKey = keyRef{start: tok.Start, end: tok.End}, true
			continue
		case jsonscan.ObjectStart, jsonscan.ArrayStart:
			ev = member()
			ev.kind, ev.obj = evOpen, tok.Kind == jsonscan.ObjectStart
			objs = append(objs, ev.obj)
		case jsonscan.ObjectEnd, jsonscan.ArrayEnd:
			objs = objs[:len(objs)-1]
			ev = dEvent{kind: evClose, end: tok.End}
		default:
			ev = member()
			ev.kind, ev.end = evScalar, tok.End
			ev.cell = dCell{vstart: tok.Start, vend: tok.End, quoted: tok.Kind == jsonscan.String}
		}
		ev.comment, comment = comment, false
		if !s.event(ev) {
			break
		}
	}
	if lines {
		s.closeVirtual(len(src))
	}
	if deep > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("%s nested deeper than %d: kept as written, not read by structure (the first at %s)",
				count(deep, "value", "values"), jsonscan.DefaultMaxDepth, syntaxPlace(src, deepAt)))
	}
	s.event(dEvent{kind: evDocEnd, end: len(src)})
	return s, nil, nil
}

// jsonComment reports whether a line is only a comment (JSON with
// comments): it begins, after white space, with "//", "/*" or "*".
func jsonComment(line []byte) bool {
	t := bytes.TrimLeft(line, " \t")
	return bytes.HasPrefix(t, []byte("//")) || bytes.HasPrefix(t, []byte("/*")) || bytes.HasPrefix(t, []byte("*"))
}

// syntaxPlace writes where an offset of src is: "line L, column C", the
// column in characters from 1.
func syntaxPlace(src []byte, off int) string {
	off = min(off, len(src))
	line, ls := 1, 0
	for i := 0; i < off; i++ {
		if src[i] == '\n' || src[i] == '\r' && (i+1 >= len(src) || src[i+1] != '\n') {
			line++
			ls = i + 1
		}
	}
	return fmt.Sprintf("line %d, column %d", line, utf8.RuneCount(src[ls:off])+1)
}
