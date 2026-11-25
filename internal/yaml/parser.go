package yaml

import "strings"

type pstate uint8

const (
	stStreamStart pstate = iota
	stImplicitDocStart
	stDocStart
	stDocContent
	stDocEnd
	stBlockNode
	stBlockSeqFirst
	stBlockSeqEntry
	stIndentlessEntry
	stBlockMapFirst
	stBlockMapKey
	stBlockMapValue
	stFlowSeqFirst
	stFlowSeqEntry
	stFlowSeqPairKey
	stFlowSeqPairValue
	stFlowSeqPairEnd
	stFlowMapFirst
	stFlowMapKey
	stFlowMapValue
	stFlowMapEmptyValue
	stEnd
)

func (p *Parser) peek() (*token, error) {
	t := p.s.peek()
	if t == nil {
		if p.s.err != nil {
			return nil, p.s.err
		}
		return nil, p.errAt(p.s.here(), "unexpected end of stream")
	}
	return t, nil
}

func (p *Parser) errAt(m mark, msg string) error {
	return &SyntaxError{Offset: m.pos, Line: m.line + 1, Column: m.col + 1, Msg: msg}
}

func (p *Parser) push(s pstate) { p.states = append(p.states, s) }

func (p *Parser) pop() {
	p.state = p.states[len(p.states)-1]
	p.states = p.states[:len(p.states)-1]
}

func (p *Parser) empty(pos int) (Event, error) {
	return Event{Kind: Scalar, Start: pos, End: pos, PropStart: pos}, nil
}

func (p *Parser) open(m mark) error {
	p.depth++
	if p.depth > p.s.maxDepth {
		return &SyntaxError{Offset: m.pos, Line: m.line + 1, Column: m.col + 1,
			Msg: "collections nested deeper than the limit", depth: true}
	}
	return nil
}

func (p *Parser) closeEvent(k EventKind, pos int) (Event, error) {
	p.depth--
	return Event{Kind: k, Start: pos, End: pos}, nil
}

func (p *Parser) parse() (Event, error) {
	switch p.state {
	case stStreamStart:
		t, err := p.peek()
		if err != nil {
			return Event{}, err
		}
		if t.kind != tokStreamStart {
			return Event{}, p.errAt(t.start, "did not find expected <stream-start>")
		}
		p.s.next()
		p.state = stImplicitDocStart
		return Event{Kind: StreamStart}, nil
	case stImplicitDocStart:
		return p.documentStart(true)
	case stDocStart:
		return p.documentStart(false)
	case stDocContent:
		t, err := p.peek()
		if err != nil {
			return Event{}, err
		}
		switch t.kind {
		case tokVersionDirective, tokTagDirective, tokReservedDirective, tokDocumentStart, tokDocumentEnd, tokStreamEnd:
			p.pop()
			return p.empty(p.lastEnd)
		}
		return p.node(true, false)
	case stDocEnd:
		return p.documentEnd()
	case stBlockNode:
		return p.node(true, false)
	case stBlockSeqFirst, stBlockSeqEntry:
		return p.blockSeqEntry(p.state == stBlockSeqFirst)
	case stIndentlessEntry:
		return p.indentlessEntry()
	case stBlockMapFirst, stBlockMapKey:
		return p.blockMapKey(p.state == stBlockMapFirst)
	case stBlockMapValue:
		return p.blockMapValue()
	case stFlowSeqFirst, stFlowSeqEntry:
		return p.flowSeqEntry(p.state == stFlowSeqFirst)
	case stFlowSeqPairKey:
		return p.flowSeqPairKey()
	case stFlowSeqPairValue:
		return p.flowSeqPairValue()
	case stFlowSeqPairEnd:
		p.state = stFlowSeqEntry
		return p.closeEvent(MappingEnd, p.lastEnd)
	case stFlowMapFirst, stFlowMapKey:
		return p.flowMapKey(p.state == stFlowMapFirst)
	case stFlowMapValue, stFlowMapEmptyValue:
		return p.flowMapValue(p.state == stFlowMapEmptyValue)
	}
	return Event{}, p.errAt(p.s.here(), "parser in an unknown state")
}

func (p *Parser) documentStart(implicit bool) (Event, error) {
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	for t.kind == tokDocumentEnd {
		p.s.next()
		implicit = true
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
	}
	switch t.kind {
	case tokVersionDirective, tokTagDirective, tokReservedDirective, tokDocumentStart, tokStreamEnd:
	default:
		if implicit {
			p.tagDirs = p.tagDirs[:0]
			p.version = false
			p.push(stDocEnd)
			p.state = stBlockNode
			return Event{Kind: DocumentStart, Start: t.start.pos, End: t.start.pos, PropStart: t.start.pos}, nil
		}
	}
	if t.kind == tokStreamEnd {
		p.state = stEnd
		p.s.next()
		n := len(p.s.src)
		return Event{Kind: StreamEnd, Start: n, End: n, PropStart: n}, nil
	}
	start := t.start.pos
	p.tagDirs = p.tagDirs[:0]
	p.version = false
	for {
		switch t.kind {
		case tokVersionDirective:
			if p.version {
				return Event{}, p.errAt(t.start, "found duplicate %YAML directive")
			}
			p.version = true
			if !strings.HasPrefix(t.value, "1.") {
				return Event{}, p.errAt(t.start, "found incompatible YAML document version")
			}
		case tokTagDirective:
			for _, d := range p.tagDirs {
				if d.handle == t.handle {
					return Event{}, p.errAt(t.start, "found duplicate %TAG directive")
				}
			}
			p.tagDirs = append(p.tagDirs, tagDir{t.handle, t.value})
		case tokReservedDirective:
		default:
			if t.kind != tokDocumentStart {
				return Event{}, p.errAt(t.start, "did not find expected <document start>")
			}
			p.push(stDocEnd)
			p.state = stDocContent
			p.s.next()
			return Event{Kind: DocumentStart, Start: start, End: t.end.pos, PropStart: start, Explicit: true}, nil
		}
		p.s.next()
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
	}
}

func (p *Parser) documentEnd() (Event, error) {
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	ev := Event{Kind: DocumentEnd, Start: p.lastEnd, End: p.lastEnd, PropStart: p.lastEnd}
	if t.kind == tokDocumentEnd {
		ev.Start, ev.End, ev.PropStart, ev.Explicit = t.start.pos, t.end.pos, t.start.pos, true
		p.s.next()
		p.state = stImplicitDocStart
	} else {
		switch t.kind {
		case tokVersionDirective, tokTagDirective, tokReservedDirective:
			return Event{}, p.errAt(t.start, "found a directive after a document without a \"...\" marker")
		}
		p.state = stDocStart
	}
	return ev, nil
}

func (p *Parser) resolveTag(handle, suffix string, m mark) (string, error) {
	if handle == "" {
		return suffix, nil
	}
	for _, d := range p.tagDirs {
		if d.handle == handle {
			return d.prefix + suffix, nil
		}
	}
	switch handle {
	case "!":
		return "!" + suffix, nil
	case "!!":
		return "tag:yaml.org,2002:" + suffix, nil
	}
	return "", p.errAt(m, "found undefined tag handle "+handle)
}

func (p *Parser) node(block, indentless bool) (Event, error) {
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	if t.kind == tokAlias {
		p.pop()
		ev := Event{Kind: Alias, Start: t.start.pos, End: t.end.pos, PropStart: t.start.pos, Value: t.value}
		p.s.next()
		return ev, nil
	}
	propStart, propEnd := -1, -1
	var ev Event
	hasAnchor, hasTag := false, false
	for t.kind == tokAnchor || t.kind == tokTag {
		if t.kind == tokAnchor {
			if hasAnchor {
				return Event{}, p.errAt(t.start, "found more than one anchor on a node")
			}
			hasAnchor = true
			ev.Anchor = t.value
		} else {
			if hasTag {
				return Event{}, p.errAt(t.start, "found more than one tag on a node")
			}
			hasTag = true
			if ev.Tag, err = p.resolveTag(t.handle, t.value, t.start); err != nil {
				return Event{}, err
			}
		}
		if propStart < 0 {
			propStart = t.start.pos
		}
		propEnd = t.end.pos
		p.s.next()
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
	}
	ev.Start, ev.End = t.start.pos, t.start.pos
	ev.PropStart = ev.Start
	if propStart >= 0 {
		ev.PropStart = propStart
	}
	switch {
	case indentless && t.kind == tokBlockEntry:
		p.state = stIndentlessEntry
		ev.Kind = SequenceStart
	case t.kind == tokScalar:
		p.pop()
		ev.Kind, ev.End, ev.Value, ev.Style = Scalar, t.end.pos, t.value, t.style
		p.s.next()
		return ev, nil
	case t.kind == tokFlowSequenceStart:
		p.state = stFlowSeqFirst
		ev.Kind, ev.Flow = SequenceStart, true
	case t.kind == tokFlowMappingStart:
		p.state = stFlowMapFirst
		ev.Kind, ev.Flow = MappingStart, true
	case block && t.kind == tokBlockSequenceStart:
		p.state = stBlockSeqFirst
		ev.Kind = SequenceStart
	case block && t.kind == tokBlockMappingStart:
		p.state = stBlockMapFirst
		ev.Kind = MappingStart
	case propStart >= 0:
		p.pop()
		ev.Kind, ev.Start, ev.End = Scalar, propEnd, propEnd
		return ev, nil
	default:
		return Event{}, p.errAt(t.start, "did not find expected node content")
	}
	if err := p.open(t.start); err != nil {
		return Event{}, err
	}
	return ev, nil
}

func (p *Parser) blockSeqEntry(first bool) (Event, error) {
	if first {
		p.s.next()
	}
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	switch t.kind {
	case tokBlockEntry:
		m := t.end.pos
		p.s.next()
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
		if t.kind != tokBlockEntry && t.kind != tokBlockEnd {
			p.push(stBlockSeqEntry)
			return p.node(true, false)
		}
		p.state = stBlockSeqEntry
		return p.empty(m)
	case tokBlockEnd:
		p.pop()
		p.s.next()
		return p.closeEvent(SequenceEnd, p.lastEnd)
	}
	return Event{}, p.errAt(t.start, "did not find expected '-' indicator in a block sequence")
}

func (p *Parser) indentlessEntry() (Event, error) {
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	if t.kind == tokBlockEntry {
		m := t.end.pos
		p.s.next()
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
		switch t.kind {
		case tokBlockEntry, tokKey, tokValue, tokBlockEnd:
			p.state = stIndentlessEntry
			return p.empty(m)
		}
		p.push(stIndentlessEntry)
		return p.node(true, false)
	}
	p.pop()
	return p.closeEvent(SequenceEnd, p.lastEnd)
}

func (p *Parser) blockMapKey(first bool) (Event, error) {
	if first {
		p.s.next()
	}
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	switch t.kind {
	case tokKey:
		m := t.end.pos
		p.s.next()
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
		switch t.kind {
		case tokKey, tokValue, tokBlockEnd:
			p.state = stBlockMapValue
			return p.empty(m)
		}
		p.push(stBlockMapValue)
		return p.node(true, true)
	case tokValue:
		p.state = stBlockMapValue
		return p.empty(t.start.pos)
	case tokBlockEnd:
		p.pop()
		p.s.next()
		return p.closeEvent(MappingEnd, p.lastEnd)
	}
	return Event{}, p.errAt(t.start, "did not find expected key in a block mapping")
}

func (p *Parser) blockMapValue() (Event, error) {
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	if t.kind == tokValue {
		m := t.end.pos
		p.s.next()
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
		switch t.kind {
		case tokKey, tokValue, tokBlockEnd:
			p.state = stBlockMapKey
			return p.empty(m)
		}
		p.push(stBlockMapKey)
		return p.node(true, true)
	}
	p.state = stBlockMapKey
	return p.empty(p.lastEnd)
}

func (p *Parser) flowSeqEntry(first bool) (Event, error) {
	if first {
		p.s.next()
	}
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	if t.kind != tokFlowSequenceEnd {
		if !first {
			if t.kind != tokFlowEntry {
				return Event{}, p.errAt(t.start, "did not find expected ',' or ']'")
			}
			p.s.next()
			if t, err = p.peek(); err != nil {
				return Event{}, err
			}
		}
		switch t.kind {
		case tokKey, tokValue:
			ev := Event{Kind: MappingStart, Start: t.start.pos, End: t.start.pos, PropStart: t.start.pos, Flow: true}
			if err := p.open(t.start); err != nil {
				return Event{}, err
			}
			if t.kind == tokKey {
				p.s.next()
			}
			p.state = stFlowSeqPairKey
			return ev, nil
		case tokFlowSequenceEnd:
		default:
			p.push(stFlowSeqEntry)
			return p.node(false, false)
		}
	}
	p.pop()
	pos := t.end.pos
	p.s.next()
	return p.closeEvent(SequenceEnd, pos)
}

func (p *Parser) flowSeqPairKey() (Event, error) {
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	switch t.kind {
	case tokValue:
		p.state = stFlowSeqPairValue
		return p.empty(t.start.pos)
	case tokFlowEntry, tokFlowSequenceEnd:
		p.state = stFlowSeqPairValue
		return p.empty(p.lastEnd)
	}
	p.push(stFlowSeqPairValue)
	return p.node(false, false)
}

func (p *Parser) flowSeqPairValue() (Event, error) {
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	if t.kind == tokValue {
		m := t.end.pos
		p.s.next()
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
		if t.kind != tokFlowEntry && t.kind != tokFlowSequenceEnd {
			p.push(stFlowSeqPairEnd)
			return p.node(false, false)
		}
		p.state = stFlowSeqPairEnd
		return p.empty(m)
	}
	p.state = stFlowSeqPairEnd
	return p.empty(p.lastEnd)
}

func (p *Parser) flowMapKey(first bool) (Event, error) {
	if first {
		p.s.next()
	}
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	if t.kind != tokFlowMappingEnd {
		if !first {
			if t.kind != tokFlowEntry {
				return Event{}, p.errAt(t.start, "did not find expected ',' or '}'")
			}
			p.s.next()
			if t, err = p.peek(); err != nil {
				return Event{}, err
			}
		}
		switch t.kind {
		case tokKey:
			m := t.end.pos
			p.s.next()
			if t, err = p.peek(); err != nil {
				return Event{}, err
			}
			switch t.kind {
			case tokValue, tokFlowEntry, tokFlowMappingEnd:
				p.state = stFlowMapValue
				return p.empty(m)
			}
			p.push(stFlowMapValue)
			return p.node(false, false)
		case tokValue:
			p.state = stFlowMapValue
			return p.empty(t.start.pos)
		case tokFlowMappingEnd:
		default:
			p.push(stFlowMapEmptyValue)
			return p.node(false, false)
		}
	}
	p.pop()
	pos := t.end.pos
	p.s.next()
	return p.closeEvent(MappingEnd, pos)
}

func (p *Parser) flowMapValue(empty bool) (Event, error) {
	if empty {
		p.state = stFlowMapKey
		return p.empty(p.lastEnd)
	}
	t, err := p.peek()
	if err != nil {
		return Event{}, err
	}
	if t.kind == tokValue {
		m := t.end.pos
		p.s.next()
		if t, err = p.peek(); err != nil {
			return Event{}, err
		}
		if t.kind != tokFlowEntry && t.kind != tokFlowMappingEnd {
			p.push(stFlowMapKey)
			return p.node(false, false)
		}
		p.state = stFlowMapKey
		return p.empty(m)
	}
	p.state = stFlowMapKey
	return p.empty(p.lastEnd)
}
