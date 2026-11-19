// Package md parses Markdown, following the block and inline structure of
// CommonMark 0.31 (with GFM tables), and renders it to HTML as a CommonMark
// renderer does, keeping a map from offsets in the HTML back to offsets in
// the Markdown. The extractor reads that HTML with the same tree builder
// and block walker as an HTML page, so raw HTML in Markdown gets the same
// hidden-text checks, and every block's location is in the Markdown source.
//
// The parser follows the reference implementation's line-by-line
// algorithm (commonmark.js): open containers are continued or closed, new
// block starts are tried, and lazy continuation lines join a paragraph.
package md

import (
	"context"
	"strings"
)

// MaxNest bounds the nesting of block quotes and lists. A marker past it
// is read as text.
const MaxNest = 100

type kind uint8

const (
	kDoc kind = iota
	kQuote
	kList
	kItem
	kPara
	kHeading
	kCode
	kHTML
	kHR
	kTable
)

type hline struct {
	text string
	off  int // offset of text in the source
}

type block struct {
	kind     kind
	parent   *block
	children []*block
	open     bool
	depth    int
	// start and end are offsets in the source; startLine and endLine the
	// first and last line with content, which decide whether a list is
	// tight.
	start, end         int
	startLine, endLine int

	// Lists and items.
	ordered               bool
	bullet, delim         byte
	startNum              int
	tight                 bool
	markerOffset, padding int

	content            string
	lines              []hline // of an HTML block
	level              int
	fenced             bool
	fence              byte
	fenceLen, fenceOff int
	info               string
	htmlType           int
	rows               [][]string // of a table: the header row first
}

// Ref is a link reference definition.
type Ref struct{ Dest, Title string }

// Doc is a parsed Markdown document.
type Doc struct {
	root *block
	refs map[string]Ref
}

type parser struct {
	doc, tip, oldtip, lastMatched *block

	line                 string
	lineStart, lineNum   int
	offset, column       int
	nextNonspace, nnCol  int
	indent               int
	indented, blank      bool
	partiallyConsumedTab bool
	allClosed            bool
	refs                 map[string]Ref
}

// Parse parses src, which must be UTF-8.
func Parse(ctx context.Context, src string) (*Doc, error) {
	p := &parser{refs: map[string]Ref{}}
	p.doc = &block{kind: kDoc, open: true, end: len(src)}
	p.tip, p.oldtip = p.doc, p.doc
	for pos, n := 0, 0; pos < len(src); n++ {
		if n&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		end := strings.IndexAny(src[pos:], "\r\n")
		next := len(src)
		if end < 0 {
			end = len(src)
		} else {
			end += pos
			next = end + 1
			if src[end] == '\r' && next < len(src) && src[next] == '\n' {
				next++
			}
		}
		p.lineStart = pos
		p.incorporate(src[pos:end])
		pos = next
	}
	for p.tip != nil {
		p.finalize(p.tip)
	}
	return &Doc{root: p.doc, refs: p.refs}, nil
}

func (p *parser) peek(i int) int {
	if i < len(p.line) {
		return int(p.line[i])
	}
	return -1
}

func (p *parser) findNextNonspace() {
	i, cols := p.offset, p.column
	for i < len(p.line) {
		switch p.line[i] {
		case ' ':
			cols++
		case '\t':
			cols += 4 - cols%4
		default:
			goto done
		}
		i++
	}
done:
	p.blank = i == len(p.line)
	p.nextNonspace, p.nnCol = i, cols
	p.indent = cols - p.column
	p.indented = p.indent >= 4
}

func (p *parser) advanceNextNonspace() {
	p.offset, p.column = p.nextNonspace, p.nnCol
	p.partiallyConsumedTab = false
}

func (p *parser) advanceOffset(count int, columns bool) {
	for count > 0 && p.offset < len(p.line) {
		if p.line[p.offset] == '\t' {
			toTab := 4 - p.column%4
			if columns {
				p.partiallyConsumedTab = toTab > count
				adv := min(count, toTab)
				p.column += adv
				if !p.partiallyConsumedTab {
					p.offset++
				}
				count -= adv
			} else {
				p.partiallyConsumedTab = false
				p.column += toTab
				p.offset++
				count--
			}
		} else {
			p.partiallyConsumedTab = false
			p.offset++
			p.column++
			count--
		}
	}
}

func lastChild(b *block) *block {
	if len(b.children) == 0 {
		return nil
	}
	return b.children[len(b.children)-1]
}

func (p *parser) incorporate(line string) {
	p.line = line
	p.lineNum++
	p.offset, p.column, p.blank, p.partiallyConsumedTab = 0, 0, false, false
	p.oldtip = p.tip
	container := p.doc
	allMatched := true
	for {
		last := lastChild(container)
		if last == nil || !last.open {
			break
		}
		container = last
		p.findNextNonspace()
		switch p.cont(container) {
		case 1:
			allMatched = false
		case 2:
			return // a closing code fence
		}
		if !allMatched {
			container = container.parent
			break
		}
	}
	p.allClosed = container == p.oldtip
	p.lastMatched = container
	matchedLeaf := container.kind == kCode || container.kind == kHTML
	for !matchedLeaf {
		p.findNextNonspace()
		if !p.indented && !maybeSpecial(p.peek(p.nextNonspace)) {
			p.advanceNextNonspace()
			break
		}
		res := 0
		for _, start := range starts {
			if res = start(p, container); res != 0 {
				break
			}
		}
		if res == 0 {
			p.advanceNextNonspace()
			break
		}
		container = p.tip
		matchedLeaf = res == 2
	}
	if !p.allClosed && !p.blank && p.tip.kind == kPara {
		p.addLine() // lazy continuation
		return
	}
	p.closeUnmatched()
	switch container.kind {
	case kCode, kHTML, kPara:
		p.addLine()
		if container.kind == kHTML && container.htmlType <= 5 && htmlEnds(container.htmlType, p.line[min(p.offset, len(p.line)):]) {
			p.finalize(container)
		}
	default:
		if p.offset < len(p.line) && !p.blank {
			p.addChild(kPara, p.offset)
			p.advanceNextNonspace()
			p.addLine()
		}
	}
}

func maybeSpecial(c int) bool {
	return c >= 0 && strings.IndexByte("#`~*+_=<>-0123456789", byte(c)) >= 0
}

// cont tries to continue an open block with the current line: 0 it
// continues, 1 it does not, 2 the line is consumed (a closing fence).
func (p *parser) cont(b *block) int {
	switch b.kind {
	case kQuote:
		if !p.indented && p.peek(p.nextNonspace) == '>' {
			p.advanceNextNonspace()
			p.advanceOffset(1, false)
			if c := p.peek(p.offset); c == ' ' || c == '\t' {
				p.advanceOffset(1, true)
			}
			return 0
		}
		return 1
	case kItem:
		switch {
		case p.blank:
			if len(b.children) == 0 {
				return 1
			}
			p.advanceNextNonspace()
		case p.indent >= b.markerOffset+b.padding:
			p.advanceOffset(b.markerOffset+b.padding, true)
		default:
			return 1
		}
		return 0
	case kHeading, kHR:
		return 1
	case kCode:
		if b.fenced {
			if p.indent <= 3 && p.peek(p.nextNonspace) == int(b.fence) {
				rest := p.line[p.nextNonspace:]
				n := run(rest, b.fence)
				if n >= b.fenceLen && strings.Trim(rest[n:], " \t") == "" {
					p.touch(b, p.lineStart+len(p.line))
					p.finalize(b)
					return 2
				}
			}
			for i := b.fenceOff; i > 0; i-- {
				if c := p.peek(p.offset); c != ' ' && c != '\t' {
					break
				}
				p.advanceOffset(1, true)
			}
			return 0
		}
		switch {
		case p.indent >= 4:
			p.advanceOffset(4, true)
		case p.blank:
			p.advanceNextNonspace()
		default:
			return 1
		}
		return 0
	case kHTML:
		if p.blank && (b.htmlType == 6 || b.htmlType == 7) {
			return 1
		}
		return 0
	case kPara:
		if p.blank {
			return 1
		}
		return 0
	}
	return 0
}

func run(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

func (p *parser) closeUnmatched() {
	if p.allClosed {
		return
	}
	for p.oldtip != p.lastMatched {
		parent := p.oldtip.parent
		p.finalize(p.oldtip)
		p.oldtip = parent
	}
	p.allClosed = true
}

func canContain(parent, child kind) bool {
	switch parent {
	case kDoc, kQuote, kItem:
		return child != kItem
	case kList:
		return child == kItem
	}
	return false
}

func (p *parser) addChild(k kind, offset int) *block {
	for !canContain(p.tip.kind, k) {
		p.finalize(p.tip)
	}
	b := &block{kind: k, parent: p.tip, open: true, depth: p.tip.depth + 1,
		start: p.lineStart + offset, startLine: p.lineNum}
	p.tip.children = append(p.tip.children, b)
	p.tip = b
	p.touch(b, p.lineStart+len(p.line))
	return b
}

// touch records that b and its ancestors reach to the current line.
func (p *parser) touch(b *block, end int) {
	for ; b != nil; b = b.parent {
		b.end = max(b.end, end)
		b.endLine = p.lineNum
	}
}

func (p *parser) addLine() {
	t := p.tip
	var sb strings.Builder
	if p.partiallyConsumedTab {
		p.offset++
		sb.WriteString(strings.Repeat(" ", 4-p.column%4))
	}
	text := p.line[min(p.offset, len(p.line)):]
	sb.WriteString(text)
	if t.kind == kHTML {
		t.lines = append(t.lines, hline{text, p.lineStart + min(p.offset, len(p.line))})
	}
	t.content += sb.String() + "\n"
	if !p.blank || t.kind == kHTML || t.kind == kCode && t.fenced {
		p.touch(t, p.lineStart+len(p.line))
	}
}

func (p *parser) finalize(b *block) {
	if !b.open {
		p.tip = b.parent
		return
	}
	b.open = false
	switch b.kind {
	case kPara:
		b.content = p.takeRefdefs(b.content)
		if strings.Trim(b.content, " \t\n") == "" {
			remove(b)
		} else {
			p.table(b)
		}
	case kCode:
		if b.fenced {
			first, rest, _ := strings.Cut(b.content, "\n")
			b.info = unescape(strings.Trim(first, " \t"))
			b.content = rest
		} else {
			lines := strings.Split(b.content, "\n")
			for len(lines) > 0 && strings.Trim(lines[len(lines)-1], " \t") == "" {
				lines = lines[:len(lines)-1]
			}
			b.content = strings.Join(lines, "\n")
			if b.content != "" {
				b.content += "\n"
			}
		}
	case kList:
		b.tight = true
		for i, it := range b.children {
			if i+1 < len(b.children) && b.children[i+1].startLine > it.endLine+1 {
				b.tight = false
			}
			for j, c := range it.children {
				if j+1 < len(it.children) && it.children[j+1].startLine > c.endLine+1 {
					b.tight = false
				}
			}
		}
	}
	p.tip = b.parent
}

func remove(b *block) {
	par := b.parent
	for i, c := range par.children {
		if c == b {
			par.children = append(par.children[:i], par.children[i+1:]...)
			return
		}
	}
}

// takeRefdefs removes the link reference definitions at the start of a
// paragraph's content and records them.
func (p *parser) takeRefdefs(s string) string {
	for len(s) > 0 && s[0] == '[' {
		n := p.refdef(s)
		if n == 0 {
			break
		}
		s = s[n:]
	}
	return s
}

func (p *parser) refdef(s string) int {
	n := linkLabel(s, 0)
	if n <= 2 || n >= len(s) || s[n] != ':' {
		return 0
	}
	label := s[1 : n-1]
	i := spnl(s, n+1)
	dest, j, ok := linkDest(s, i, false)
	if !ok {
		return 0
	}
	i = j
	before := i
	title, titled := "", false
	if k := spnl(s, i); k != i {
		if t, m, ok := linkTitle(s, k); ok {
			title, titled, i = t, true, m
		}
	}
	e := skipSpaceTab(s, i)
	if e < len(s) && s[e] != '\n' {
		if !titled {
			return 0
		}
		title, i = "", before
		e = skipSpaceTab(s, i)
		if e < len(s) && s[e] != '\n' {
			return 0
		}
	}
	if e < len(s) {
		e++
	}
	key := normLabel(label)
	if key == "" {
		return 0
	}
	if _, dup := p.refs[key]; !dup {
		p.refs[key] = Ref{dest, title}
	}
	return e
}

func skipSpaceTab(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// spnl skips spaces and tabs and at most one line ending.
func spnl(s string, i int) int {
	i = skipSpaceTab(s, i)
	if i < len(s) && s[i] == '\n' {
		i = skipSpaceTab(s, i+1)
	}
	return i
}

// normLabel is CommonMark's label normalization: white space collapsed,
// case folded (as the reference implementation does it, by lowering and
// then raising case).
func normLabel(s string) string {
	return strings.ToUpper(strings.ToLower(strings.Join(strings.Fields(s), " ")))
}
