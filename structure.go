package gofasthtml

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/charset"
)

// How JSON and YAML are cut by their structure.
//
// A document's top-level value is one block when it is at most
// dataUnitBytes long. A longer one is split into its members (an
// object's members, an array's elements), and consecutive members are
// packed into blocks of at most dataUnitBytes; a member longer than that
// is split in turn, so that no value is ever cut. Each block is the text
// of its members as the file writes them, with the comments just before
// a member and after it on its line, and Loc.Anchor is the path of its
// first member (Attrs["path_end"] that of its last).
//
// In an array that is split, a run of two or more records (objects whose
// members are all scalars) is a table: a header row of their keys and a
// row per record, a cell holding the value as written (a string in its
// quotes, its escapes decoded). A top-level array of records is a table
// whatever its length.
const (
	dataUnitBytes = 4 << 10
	// AttrPathEnd is the path of a block's last member when it holds
	// several: Loc.Anchor is the first's.
	AttrPathEnd = "path_end"
	// ExtraJSONSyntaxError and ExtraYAMLSyntaxError, in Metadata.Extra,
	// say where and why a JSON or YAML file is not JSON or YAML; it is
	// then read as lines.
	ExtraJSONSyntaxError = "json.syntax_error"
	ExtraYAMLSyntaxError = "yaml.syntax_error"
)

// The events a reader of JSON or YAML gives the splitter.
type dEvKind uint8

const (
	evOpen   dEvKind = iota + 1 // a container begins
	evClose                     // the innermost container ends at end
	evScalar                    // a scalar from vstart to end
	evRaw                       // text that is no value (a line of JSON Lines that is not JSON), start to end
	evDocEnd                    // a document ends; text up to end is its own
)

// keyRef is a member's key: s when the reader decoded it, else the span
// of a JSON string to decode.
type keyRef struct {
	start, end int
	s          string
	set        bool // s holds the key
}

// dEvent is one event.
type dEvent struct {
	kind   dEvKind
	obj    bool   // evOpen: an object or mapping
	start  int    // the member's start: its key's for a member of an object
	end    int    // evClose, evScalar, evRaw, evDocEnd: the end
	key    keyRef // a member of an object
	hasKey bool
	cell   dCell // evScalar: the value
}

// dCell is a scalar value as a table cell: text when the reader made it,
// else the JSON token from vstart to vend (quoted a string).
type dCell struct {
	key          keyRef
	vstart, vend int
	text         string
	set          bool
	quoted       bool // a JSON string
}

// dUnit is a member of a container: a scalar, a container that fits in
// a block, or a raw piece of text.
type dUnit struct {
	start, end int
	key        keyRef
	hasKey     bool
	index      int
	record     bool
	cells      []dCell
}

// dFrame is an open container.
type dFrame struct {
	obj     bool
	virtual bool // the sequence of values of JSON Lines
	unit    dUnit
	split   bool
	kids    []dUnit // the members closed while it is not split
	n       int     // members so far
	flat    bool    // an object whose members so far are all scalars
	cells   []dCell
	path    string
	pathSet bool

	group struct {
		first, last dUnit
		n           int
		start, end  int
	}
	run dRun
}

// dRun is a run of records in a split array, the table being made.
type dRun struct {
	rows   []dUnit
	cols   []string
	colOf  map[string]int
	pieces int // tables already made from the run
	start  int
}

// dSplitter makes the blocks of one JSON or YAML file.
type dSplitter struct {
	ctx      context.Context
	src      []byte // the decoded text
	m        *charset.Map
	format   string
	comment  func(line []byte) bool // a line that is only a comment
	keyText  func(keyRef) string
	cellText func(dCell) string
	limit    int
	text     *gofastcore.TextBudget
	part     *gofastcore.Part

	stack        []*dFrame
	free         []*dFrame
	firstUnsplit int
	lastEnd      int // the end of the last block made
	spent        bool
	controls     int
	// doc is the number of the YAML document being read, in a file of
	// several; empty otherwise.
	doc string
	// scanned is where trailing has looked for comments up to.
	scanned int
	// head is where the text of the outermost container split since the
	// last block begins (its key, the comments above it): the next block
	// begins there, so that no line of the file is left out. -1 if none.
	head int
}

func (s *dSplitter) top() *dFrame {
	if len(s.stack) == 0 {
		return nil
	}
	return s.stack[len(s.stack)-1]
}

// event takes one event. It returns false when the text budget ran out.
func (s *dSplitter) event(ev dEvent) bool {
	if s.spent {
		return false
	}
	switch ev.kind {
	case evOpen:
		p := s.top()
		u := s.member(p, ev)
		if p != nil && p.obj {
			p.flat = false
		}
		f := s.newFrame()
		f.obj, f.unit, f.flat = ev.obj, u, ev.obj
		s.stack = append(s.stack, f)
	case evScalar, evRaw:
		s.check(ev.end)
		p := s.top()
		u := s.member(p, ev)
		u.end = ev.end
		if p == nil {
			s.emit(nil, u, u)
			break
		}
		if ev.kind == evRaw {
			p.flat = false
		} else if p.obj && p.flat {
			c := ev.cell
			c.key = ev.key
			p.cells = append(p.cells, c)
		}
		s.add(p, u)
	case evClose:
		s.check(ev.end)
		f := s.stack[len(s.stack)-1]
		f.unit.end = ev.end
		var p *dFrame
		if len(s.stack) > 1 {
			p = s.stack[len(s.stack)-2]
		}
		// What f holds is made into blocks while f is open, so that
		// paths can be written.
		switch {
		case f.split:
			s.flushRun(f)
			s.flushGroup(f)
		case p == nil && !f.obj && len(f.kids) >= 2 && allRecords(f.kids):
			// A top-level array of records is a table.
			s.becomeSplit(f, nil)
			s.flushRun(f)
			s.flushGroup(f)
		case p == nil:
			s.emit(nil, f.unit, f.unit)
		}
		s.stack = s.stack[:len(s.stack)-1]
		s.firstUnsplit = min(s.firstUnsplit, len(s.stack))
		if p != nil && !f.split {
			u := f.unit
			if f.obj && f.flat && f.n > 0 && f.n <= MaxCSVColumns && !p.obj {
				u.record = true
				u.cells = append([]dCell(nil), f.cells...)
			}
			s.add(p, u)
		}
		s.release(f)
	case evDocEnd:
		s.trailing(ev.end)
	}
	return !s.spent
}

// member makes the unit of a new member of p.
func (s *dSplitter) member(p *dFrame, ev dEvent) dUnit {
	u := dUnit{start: ev.start, key: ev.key, hasKey: ev.hasKey}
	if p != nil {
		u.index = p.n
		p.n++
	}
	return u
}

func (s *dSplitter) newFrame() *dFrame {
	if n := len(s.free); n > 0 {
		f := s.free[n-1]
		s.free = s.free[:n-1]
		return f
	}
	return &dFrame{}
}

func (s *dSplitter) release(f *dFrame) {
	kids, cells, rows := f.kids[:0], f.cells[:0], f.run.rows[:0]
	if cap(kids) > 1024 {
		kids = nil
	}
	if cap(cells) > 1024 {
		cells = nil
	}
	if cap(rows) > 1024 {
		rows = nil
	}
	*f = dFrame{kids: kids, cells: cells}
	f.run.rows = rows
	s.free = append(s.free, f)
}

func allRecords(us []dUnit) bool {
	for _, u := range us {
		if !u.record {
			return false
		}
	}
	return true
}

// check splits every open container that has grown past the limit by
// pos, the outermost first.
func (s *dSplitter) check(pos int) {
	for s.firstUnsplit < len(s.stack) {
		f := s.stack[s.firstUnsplit]
		if !f.split && pos-f.unit.start <= s.limit {
			return
		}
		var p *dFrame
		if s.firstUnsplit > 0 {
			p = s.stack[s.firstUnsplit-1]
		}
		if !f.split {
			s.becomeSplit(f, p)
		}
		s.firstUnsplit++
	}
}

// becomeSplit splits f, a member of p (nil at the top): what p holds
// before f is made into blocks, and f's members so far are taken one by
// one.
func (s *dSplitter) becomeSplit(f, p *dFrame) {
	if p != nil {
		s.flushRun(p)
		s.flushGroup(p)
	}
	f.split, f.flat = true, false
	if s.head < 0 && !f.virtual {
		s.head = max(s.extendStart(f.unit.start), s.lastEnd)
	}
	kids := f.kids
	f.kids = nil
	for _, u := range kids {
		s.add(f, u)
	}
}

// add takes the closed member u of p.
func (s *dSplitter) add(p *dFrame, u dUnit) {
	if !p.split {
		p.kids = append(p.kids, u)
		return
	}
	if !p.obj && u.record && s.fits(p, u) {
		s.addRow(p, u)
		return
	}
	s.flushRun(p)
	s.addGroup(p, u)
}

// fits reports whether the record u can join p's run without taking it
// past MaxCSVColumns columns.
func (s *dSplitter) fits(p *dFrame, u dUnit) bool {
	n := len(p.run.cols)
	for _, c := range u.cells {
		if _, ok := p.run.colOf[s.keyText(c.key)]; !ok {
			n++
		}
	}
	return n <= MaxCSVColumns
}

func (s *dSplitter) addGroup(p *dFrame, u dUnit) {
	g := &p.group
	if g.n > 0 && u.end-g.start > s.limit {
		s.flushGroup(p)
	}
	if u.end-u.start > s.limit {
		s.emit(p, u, u)
		return
	}
	if g.n == 0 {
		g.first, g.start = u, u.start
	}
	g.last, g.end = u, u.end
	g.n++
}

func (s *dSplitter) flushGroup(p *dFrame) {
	g := &p.group
	if g.n == 0 {
		return
	}
	first, last := g.first, g.last
	g.n = 0
	first.start, last.end = g.start, g.end
	s.emit(p, first, last)
}

// emit makes a block of the text from first's start to last's end,
// members of p (nil for a top-level value, whose path is empty).
func (s *dSplitter) emit(p *dFrame, first, last dUnit) {
	// After a cut by the text budget nothing more is made.
	if s.spent {
		return
	}
	start, end := s.extendStart(first.start), s.extendEnd(last.end)
	start = s.headed(max(start, s.lastEnd))
	if start >= end {
		return
	}
	text := s.clean(s.src[start:end])
	if strings.TrimSpace(text) == "" {
		s.lastEnd = end // nothing but control characters and white space
		return
	}
	if left := s.text.Left(); int64(gofastcore.BlockCost+len(text)) > left {
		// The text budget ends inside the block: it is cut there, at a
		// line end when there is one, and nothing more is read.
		room := int(left) - gofastcore.BlockCost
		s.spent = true
		if room <= 0 {
			s.text.Exhaust()
			return
		}
		raw := s.src[start:end]
		cut := room
		for cut > 0 && raw[cut]&0xC0 == 0x80 {
			cut--
		}
		if nl := bytes.LastIndexByte(raw[:cut], '\n'); nl > 0 {
			cut = nl
		}
		end = start + cut
		text = strings.TrimRight(s.clean(raw[:cut]), "\r")
		s.text.Take(gofastcore.BlockCost + len(text))
		s.text.Exhaust()
		if strings.TrimSpace(text) == "" {
			return
		}
	} else {
		s.text.Take(gofastcore.BlockCost + len(text))
	}
	off := s.m.Source(start)
	b := gofastcore.Block{Kind: gofastcore.KindCode, Text: text,
		Attrs: map[string]string{gofastcore.AttrLanguage: s.format},
		Loc:   gofastcore.Location{Offset: off, Length: s.m.Source(end) - off}}
	if s.doc != "" {
		b.Attrs[AttrDocument] = s.doc
	}
	if p != nil {
		b.Loc.Anchor = s.path(p, first)
		if first.start != last.start || first.index != last.index {
			b.Attrs[AttrPathEnd] = s.path(p, last)
		}
	}
	s.part.Blocks = append(s.part.Blocks, b)
	s.lastEnd = end
}

// headed returns where a block that would begin at start begins: at
// the head of the containers split before it, if any.
func (s *dSplitter) headed(start int) int {
	if s.head >= 0 {
		start = min(start, s.head)
		s.head = -1
	}
	return start
}

// addRow adds the record u to p's run, making a table of the run's rows
// when it has csvRowsPerBlock of them.
func (s *dSplitter) addRow(p *dFrame, u dUnit) {
	r := &p.run
	if len(r.rows) == 0 && r.pieces == 0 {
		r.start = u.start
		if r.colOf == nil {
			r.colOf = map[string]int{}
		}
	}
	for _, c := range u.cells {
		k := s.keyText(c.key)
		if _, ok := r.colOf[k]; !ok {
			r.colOf[k] = len(r.cols)
			r.cols = append(r.cols, k)
		}
	}
	r.rows = append(r.rows, u)
	if len(r.rows) == 2 && r.pieces == 0 {
		// A run of two records is a table: what is before it is made
		// first. (A lone record joins the members around it.)
		s.flushGroup(p)
	}
	if len(r.rows) >= csvRowsPerBlock || len(r.rows)*len(r.cols) >= csvCellsPerBlock {
		s.table(p)
	}
}

// flushRun ends p's run: a lone record is a member like any other.
func (s *dSplitter) flushRun(p *dFrame) {
	r := &p.run
	switch {
	case len(r.rows) == 1 && r.pieces == 0:
		u := r.rows[0]
		*r = dRun{rows: r.rows[:0]}
		s.addGroup(p, u)
		return
	case len(r.rows) > 0:
		s.table(p)
	}
	*r = dRun{rows: r.rows[:0]}
}

// table makes a table of the rows of p's run; more says the run goes on.
func (s *dSplitter) table(p *dFrame) {
	r := &p.run
	if s.spent || len(r.rows) == 0 {
		return
	}
	width := len(r.cols)
	t := &gofastcore.Table{HeaderRows: 1, Rows: make([][]string, 0, len(r.rows)+1),
		RowLocs: make([]gofastcore.Location, 0, len(r.rows)+1)}
	header := make([]string, width)
	size := 0
	for i, k := range r.cols {
		header[i] = s.clean([]byte(k))
		size += len(header[i])
	}
	if !s.text.Take(gofastcore.BlockCost + gofastcore.CellCost*width + size) {
		s.spent = true
		return
	}
	t.Rows = append(t.Rows, header)
	t.RowLocs = append(t.RowLocs, gofastcore.Location{})
	start := s.headed(max(s.extendStart(r.rows[0].start), s.lastEnd))
	end := r.rows[len(r.rows)-1].end
	for _, u := range r.rows {
		row := make([]string, width)
		size := 0
		for _, c := range u.cells {
			v := s.cleanCell(s.cellText(c))
			row[r.colOf[s.keyText(c.key)]] = v
			size += len(v)
		}
		if !s.text.Take(gofastcore.CellCost*width + size) {
			s.spent = true
			break
		}
		t.Rows = append(t.Rows, row)
		off := s.m.Source(u.start)
		t.RowLocs = append(t.RowLocs, gofastcore.Location{Offset: off, Length: s.m.Source(u.end) - off, Anchor: s.path(p, u)})
		end = u.end
	}
	if len(t.Rows) > 1 {
		off := s.m.Source(start)
		b := gofastcore.Block{Kind: gofastcore.KindTable, Table: t, Continued: r.pieces > 0,
			Attrs: map[string]string{},
			Loc:   gofastcore.Location{Offset: off, Length: s.m.Source(end) - off, Anchor: s.path(p, r.rows[0])}}
		if len(t.Rows) > 2 {
			b.Attrs[AttrPathEnd] = s.path(p, r.rows[len(t.Rows)-2])
		}
		if s.doc != "" {
			b.Attrs[AttrDocument] = s.doc
		}
		s.part.Blocks = append(s.part.Blocks, b)
		s.lastEnd = end
	}
	r.pieces++
	r.rows = r.rows[:0]
}

// trailing makes a block of the comments after the last block, up to end.
func (s *dSplitter) trailing(end int) {
	if s.spent || s.comment == nil {
		return
	}
	pos := s.lastEnd
	// The rest of the line of the last block is not a line of its own.
	if pos > 0 && pos < end && s.src[pos-1] != '\n' && s.src[pos-1] != '\r' {
		_, pos = lineAt(s.src, pos)
	}
	// What was looked at before, with no block made since, is not looked
	// at again.
	if s.scanned > pos {
		pos = s.scanned
	}
	s.scanned = max(s.scanned, end)
	first, last := -1, -1
	for pos < end {
		ls, next := lineAt(s.src, pos)
		line := s.src[pos:min(ls, end)]
		if s.comment(line) {
			if first < 0 {
				first = pos
			}
			last = min(ls, end)
		} else if !isBlank(line) {
			first, last = -1, -1 // only the comments after the last text
		}
		pos = next
	}
	if first >= 0 {
		s.emit(nil, dUnit{start: first, end: last}, dUnit{start: first, end: last})
	}
}

// notRead returns where the text not made into blocks begins: after the
// last block, at the next line when the last block ended at a line end.
func (s *dSplitter) notRead() int {
	from := s.lastEnd
	if from < len(s.src) && (s.src[from] == '\n' || s.src[from] == '\r') {
		_, from = lineAt(s.src, from)
	}
	return from
}

// lineAt returns the end of the line at pos (before its line end) and the
// start of the next line.
func lineAt(b []byte, pos int) (end, next int) {
	i := pos
	for i < len(b) && b[i] != '\n' && b[i] != '\r' {
		i++
	}
	switch {
	case i+1 < len(b) && b[i] == '\r' && b[i+1] == '\n':
		return i, i + 2
	case i < len(b):
		return i, i + 1
	}
	return i, i
}

func isBlank(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}

// lineStart returns the start of the line pos is on.
func lineStart(b []byte, pos int) int {
	for pos > 0 && b[pos-1] != '\n' && b[pos-1] != '\r' {
		pos--
	}
	return pos
}

// extendStart moves a member's start back to the start of its line when
// only white space (and, in YAML, the "-" of sequence entries) is before
// it there, and then over the comment lines just above it, blank lines
// between them included; never before the last block's end.
func (s *dSplitter) extendStart(pos int) int {
	ls := lineStart(s.src, pos)
	if ls < s.lastEnd {
		return pos
	}
	for i := ls; i < pos; i++ {
		c := s.src[i]
		if c == ' ' || c == '\t' || s.format == "yaml" && c == '-' && i+1 < len(s.src) && (s.src[i+1] == ' ' || s.src[i+1] == '\t') {
			continue
		}
		return pos
	}
	start := ls
	for at := ls; at > s.lastEnd; {
		prev := lineStart(s.src, at-1)
		if at-1 > 0 && s.src[at-1] == '\n' && s.src[at-2] == '\r' {
			prev = lineStart(s.src, at-2)
		}
		if prev < s.lastEnd {
			break
		}
		end, _ := lineAt(s.src, prev)
		line := s.src[prev:end]
		switch {
		case s.comment != nil && s.comment(line):
			start = prev
		case isBlank(line):
		default:
			return start
		}
		at = prev
	}
	return start
}

// extendEnd moves a member's end to the end of its line when the rest of
// the line is a comment, after white space, commas and closing brackets.
func (s *dSplitter) extendEnd(pos int) int {
	if s.comment == nil {
		return pos
	}
	end, _ := lineAt(s.src, pos)
	i := pos
	for i < end {
		c := s.src[i]
		if c == ' ' || c == '\t' || c == ',' || c == ']' || c == '}' {
			i++
			continue
		}
		break
	}
	if i < end && s.comment(s.src[i:end]) {
		return end
	}
	return pos
}

// clean returns the text of a block, as cleanCode.
func (s *dSplitter) clean(b []byte) string { return cleanCode(b, &s.controls) }

// cleanCode returns text as a code block holds it: line ends made "\n",
// control characters other than tab dropped (counted in *controls), line
// ends at its end left out.
func cleanCode(b []byte, controls *int) string {
	var out []byte
	for pos := 0; pos < len(b); {
		end, next := lineAt(b, pos)
		if pos > 0 {
			out = append(out, '\n')
		}
		out = dropControls(out, b[pos:end], controls)
		if out == nil {
			out = []byte{}
		}
		pos = next
	}
	return strings.TrimRight(string(out), "\n")
}

// cleanCell drops a cell's control characters other than tab and line
// feed.
func (s *dSplitter) cleanCell(v string) string {
	if !hasControl(v) {
		return v
	}
	parts := strings.Split(v, "\n")
	for i, p := range parts {
		parts[i] = string(dropControls(nil, []byte(p), &s.controls))
	}
	return strings.Join(parts, "\n")
}

func hasControl(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < 0x20 && c != '\t' && c != '\n' || c == 0x7F || c == 0xC2 && i+1 < len(v) && v[i+1] >= 0x80 && v[i+1] <= 0x9F {
			return true
		}
	}
	return false
}

// maxPath bounds a path used as Loc.Anchor: a longer one is left out, as
// an HTML id over 1,024 bytes is.
const maxPath = 1024

// path returns the path of u, a member of p; empty when it is longer than
// maxPath.
func (s *dSplitter) path(p *dFrame, u dUnit) string {
	fp := s.framePath(p)
	if len(fp) > maxPath {
		return ""
	}
	if path := fp + s.segment(p, u); len(path) <= maxPath {
		return path
	}
	return ""
}

func (s *dSplitter) framePath(f *dFrame) string {
	if f.pathSet {
		return f.path
	}
	for i, g := range s.stack {
		if g != f {
			continue
		}
		if i > 0 {
			p := s.stack[i-1]
			f.path = s.framePath(p) + s.segment(p, f.unit)
		}
		f.pathSet = true
		return f.path
	}
	return ""
}

// segment writes u's step in a path: key (.key after another step),
// ["odd key"] or [index].
func (s *dSplitter) segment(p *dFrame, u dUnit) string {
	if !p.obj || !u.hasKey {
		return "[" + strconv.Itoa(u.index) + "]"
	}
	k := s.keyText(u.key)
	if len(k) > maxPath {
		k = k[:maxPath+1] // longer than a path may be: it is left out
	}
	if !plainKey(k) {
		return "[" + strconv.Quote(k) + "]"
	}
	if s.framePath(p) == "" {
		return k
	}
	return "." + k
}

// openVirtual begins the sequence of the values of JSON Lines, split from
// the start: each value is a member, its path [n].
func (s *dSplitter) openVirtual() {
	f := s.newFrame()
	f.virtual, f.split, f.pathSet = true, true, true
	s.stack = append(s.stack, f)
	s.firstUnsplit = len(s.stack)
}

// closeVirtual ends it at end.
func (s *dSplitter) closeVirtual(end int) {
	if len(s.stack) == 0 {
		return
	}
	f := s.stack[0]
	s.flushRun(f)
	s.flushGroup(f)
	s.stack = s.stack[:0]
	s.firstUnsplit = 0
	s.release(f)
}

// plainKey reports whether a key can be written after a dot.
func plainKey(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		switch {
		case c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '-'):
		default:
			return false
		}
	}
	return true
}
