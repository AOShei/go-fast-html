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
// Extractor.DataBlockBytes long (dataUnitBytes by default). A longer one is split into its members (an
// object's members, an array's elements), and consecutive members are
// packed into blocks whose members span at most dataUnitBytes (a block
// adds the indentation, head and comments around them); a member longer
// than that is split in turn, so that no value is ever cut. Each block is the text
// of its members as the file writes them, with the comments just before
// a member and after it on its line, and Loc.Anchor is the path of its
// first member (Attrs["path_end"] that of its last).
//
// In an array that is split, a run of two or more records (objects whose
// members are all scalars, no key twice, in YAML with no anchor or tag)
// is a table: a header row of their keys and a row per record, a cell
// holding the value as written (a string in its quotes, its escapes
// decoded). A top-level array of records is a table whatever its length.
// The text before a table that is in no block (a key line, comments) is
// a code block before it; a comment between two records ends the run.
const (
	dataUnitBytes = DefaultDataBlockBytes
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
	props  bool   // evOpen: a YAML node with an anchor or a tag
	start  int    // the member's start: its key's for a member of an object
	end    int    // evClose, evScalar, evRaw, evDocEnd: the end
	key    keyRef // a member of an object
	hasKey bool
	cell   dCell // evScalar: the value
	// comment: the text between the event before and this one, in the
	// innermost object, holds a comment. An object with a comment is no
	// record: a row would not show it.
	comment bool
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
	// the start and end of the last member taken while split
	lastStart, last int
	path            string
	pathSet         bool
	// pathLong: the path is longer than maxPath; its members have none.
	pathLong bool

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
	end    int // the end of the last row
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
	// noComment: the text from lastEnd to it has no comment (gapComment).
	noComment int
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
		f.obj, f.unit, f.flat = ev.obj, u, ev.obj && !ev.props
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
		if ev.kind == evRaw || ev.comment {
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
		if ev.comment {
			f.flat = false
		}
		var p *dFrame
		if len(s.stack) > 1 {
			p = s.stack[len(s.stack)-2]
		}
		// What f holds is made into blocks while f is open, so that
		// paths can be written.
		switch {
		case f.split:
			s.closeSplit(f, ev.end)
		case p == nil && !f.obj && len(f.kids) >= 2 && allRecords(f.kids):
			// A top-level array of records is a table.
			s.becomeSplit(f, nil)
			s.closeSplit(f, ev.end)
		case p == nil:
			s.emit(nil, f.unit, f.unit)
		}
		s.stack = s.stack[:len(s.stack)-1]
		s.firstUnsplit = min(s.firstUnsplit, len(s.stack))
		if p != nil && !f.split {
			u := f.unit
			if f.obj && f.flat && f.n > 0 && f.n <= MaxCSVColumns && !p.obj && s.distinctKeys(f.cells) {
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

// distinctKeys reports whether no key is in cells twice: an object with a
// key twice is not a record, as a row would show one of its values only.
func (s *dSplitter) distinctKeys(cells []dCell) bool {
	if len(cells) <= 16 {
		var keys [16]string
		for i := range cells {
			keys[i] = s.keyText(cells[i].key)
			for j := range i {
				if keys[i] == keys[j] {
					return false
				}
			}
		}
		return true
	}
	seen := make(map[string]struct{}, len(cells))
	for _, c := range cells {
		k := s.keyText(c.key)
		if _, ok := seen[k]; ok {
			return false
		}
		seen[k] = struct{}{}
	}
	return true
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
	p.lastStart, p.last = u.start, u.end
	r := &p.run
	if (len(r.rows) > 0 || r.pieces > 0) && s.comment != nil && s.commentIn(r.end, u.start) >= 0 {
		// A comment between two records ends the run: a table holds
		// values only, and the comment is the text before the next one.
		s.flushRun(p)
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
	start = s.gapComment(s.headed(max(start, s.lastEnd)))
	if start >= end {
		return
	}
	anchor, pathEnd := "", ""
	if p != nil {
		anchor = s.path(p, first)
		if first.start != last.start || first.index != last.index {
			pathEnd = s.path(p, last)
		}
	}
	s.block(start, end, anchor, pathEnd)
}

// block makes a code block of the text from start to end.
func (s *dSplitter) block(start, end int, anchor, pathEnd string) {
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
	b.Loc.Anchor = anchor
	if pathEnd != "" {
		b.Attrs[AttrPathEnd] = pathEnd
	}
	s.part.Blocks = append(s.part.Blocks, b)
	s.lastEnd = end
}

// gapComment returns where the first comment in the text between the
// last block's end and start begins, if there is one; else start. Text
// there is in no block: only a comment may be, and it is kept.
func (s *dSplitter) gapComment(start int) int {
	if s.comment == nil {
		return start
	}
	// What was looked at before, with no comment in it, is not looked at
	// again: with no block made since, every look would start at lastEnd.
	from := max(s.lastEnd, s.noComment)
	if i := s.commentIn(from, start); i >= 0 {
		return i
	}
	s.noComment = max(s.noComment, start)
	return start
}

// commentIn returns the offset of the first comment between from and to,
// text that is no value, or -1.
func (s *dSplitter) commentIn(from, to int) int {
	for i := from; i < to; i++ {
		switch c := s.src[i]; {
		case s.format == "json" && c == '/' && i+1 < to && (s.src[i+1] == '/' || s.src[i+1] == '*'):
			return i
		case s.format == "yaml" && c == '#' && (i == 0 || s.src[i-1] == ' ' || s.src[i-1] == '\t' || s.src[i-1] == '\n' || s.src[i-1] == '\r'):
			return i
		}
	}
	return -1
}

// onlyPunct reports whether b holds only white space, commas and
// brackets.
func onlyPunct(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n', ',', '[', ']', '{', '}':
		default:
			return false
		}
	}
	return true
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
	r.end = u.end
	if len(r.rows) == 2 && r.pieces == 0 {
		// A run of two records is a table: what is before it is made
		// first. (A lone record joins the members around it.)
		s.flushGroup(p)
	}
	if len(r.rows) >= csvRowsPerBlock || len(r.rows)*len(r.cols) >= csvCellsPerBlock {
		s.table(p)
	}
}

// closeSplit makes the last blocks of the split container f, which ends
// at end. A comment after its last member (before its closing bracket,
// or its dedent) joins the last group when that is still to be made and
// has room; else it is a block of its own, at f's path.
func (s *dSplitter) closeSplit(f *dFrame, end int) {
	cs, ce := -1, -1
	if s.comment != nil && f.n > 0 {
		if cs = s.commentIn(f.last, end); cs >= 0 {
			// To the end of the comment's last line before end.
			ce = end
			for ce > cs && onlyPunct(s.src[ce-1:ce]) {
				ce--
			}
			// A line comment runs to its line's end, brackets and all.
			ls := ce
			for ls > cs && s.src[ls-1] != '\n' && s.src[ls-1] != '\r' {
				ls--
			}
			if i := s.commentIn(ls, ce); i >= 0 && (s.format == "yaml" || s.src[i+1] == '/') {
				le, _ := lineAt(s.src, i)
				ce = min(le, end)
			}
		} else if s.format == "yaml" && end > 0 && s.src[end-1] != ']' && s.src[end-1] != '}' {
			cs, ce = s.dedentComments(f, end)
		}
	}
	g := &f.group
	if cs >= 0 && len(f.run.rows) == 0 && g.n > 0 && ce-g.start <= s.limit {
		g.end = ce
		cs = -1
	}
	s.flushRun(f)
	s.flushGroup(f)
	if cs >= 0 && !s.spent {
		if start := max(cs, s.lastEnd); start < ce {
			s.block(start, ce, s.framePath(f), "")
		}
	}
}

// dedentComments returns the comment lines after the YAML block
// collection f, which ends at end, that are indented at least as far as
// its last member's line: they are its own, not the next member's. -1
// when there are none.
func (s *dSplitter) dedentComments(f *dFrame, end int) (cs, ce int) {
	ind := 0
	for i := lineStart(s.src, f.lastStart); i < f.lastStart && s.src[i] == ' '; i++ {
		ind++
	}
	pos := max(end, s.lastEnd)
	cs, ce = -1, -1
	if pos > 0 && pos < len(s.src) && s.src[pos-1] != '\n' && s.src[pos-1] != '\r' {
		// The rest of end's line is extendEnd's, but for a row of a
		// table, which holds values only.
		le, next := lineAt(s.src, pos)
		if len(f.run.rows) > 0 {
			if i := s.commentIn(pos, le); i >= 0 {
				cs, ce = i, le
			}
		}
		pos = next
	}
	for pos < len(s.src) {
		// Only the start of a line is looked at before it is taken.
		i := pos
		for i < len(s.src) && (s.src[i] == ' ' || s.src[i] == '\t') {
			i++
		}
		le, next := 0, 0
		switch {
		case i == len(s.src) || s.src[i] == '\n' || s.src[i] == '\r':
			_, next = lineAt(s.src, i)
		case s.src[i] == '#' && i-pos >= ind:
			le, next = lineAt(s.src, i)
			if cs < 0 {
				cs = pos
			}
			ce = le
		default:
			return cs, ce
		}
		pos = next
	}
	return cs, ce
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
	// The text before the first row that is in no block (the key of the
	// container, the comments above) is a code block of its own: a table
	// holds values only.
	start := s.gapComment(s.headed(max(s.extendStart(r.rows[0].start), s.lastEnd)))
	if pre := s.rowLine(r.rows[0].start); start < pre && !onlyPunct(s.src[start:pre]) {
		s.block(start, pre, s.framePath(p), "")
		if s.spent {
			return
		}
		start = r.rows[0].start
	}
	width := len(r.cols)
	t := &gofastcore.Table{HeaderRows: 1, Rows: make([][]string, 0, len(r.rows)+1),
		RowLocs: make([]gofastcore.Location, 0, len(r.rows)+1)}
	header := make([]string, width)
	size := 0
	for i, k := range r.cols {
		header[i] = gofastcore.ExpandLigatures(s.clean([]byte(k)))
		size += len(header[i])
	}
	if !s.text.Take(gofastcore.BlockCost + gofastcore.CellCost*width + size) {
		s.spent = true
		return
	}
	t.Rows = append(t.Rows, header)
	t.RowLocs = append(t.RowLocs, gofastcore.Location{})
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

// trailing makes a block of the comments after the last block, up to
// end: from the first to the end of the last, with what is between them
// (closing brackets, a YAML "..."), as written.
func (s *dSplitter) trailing(end int) {
	if s.spent || s.comment == nil {
		return
	}
	// What was looked at before, with no block made since, is not looked
	// at again.
	pos := max(s.lastEnd, s.scanned)
	s.scanned = max(s.scanned, end)
	first, last := -1, -1
	for pos < end {
		i := s.commentIn(pos, end)
		if i < 0 {
			break
		}
		if first < 0 {
			first = i
		}
		last = s.commentEnd(i, end)
		pos = last
	}
	if first >= 0 {
		s.emit(nil, dUnit{start: first, end: last}, dUnit{start: first, end: last})
	}
}

// commentEnd returns the end of the comment at i, not past end: a block
// comment's "*/", else the end of its line.
func (s *dSplitter) commentEnd(i, end int) int {
	if s.format == "json" && s.src[i+1] == '*' {
		if j := bytes.Index(s.src[i+2:end], []byte("*/")); j >= 0 {
			return i + 2 + j + 2
		}
		return end
	}
	le, _ := lineAt(s.src, i)
	return min(le, end)
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
	// Back over the white space before pos only: a long line is not
	// looked at again for each member on it.
	ls := s.rowLine(pos)
	if ls == pos && pos > 0 && s.src[pos-1] != '\n' && s.src[pos-1] != '\r' {
		return pos
	}
	if ls < s.lastEnd {
		return pos
	}
	start := ls
	for at := ls; at > s.lastEnd; {
		end := at - 1 // the line end before at
		if end > 0 && s.src[end] == '\n' && s.src[end-1] == '\r' {
			end--
		}
		prev := lineStartAbove(s.src, end, s.lastEnd)
		if prev < 0 {
			break
		}
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
	// Only the bytes up to the first other one are looked at, so that a
	// long line is not read again for each member on it.
	i := pos
	for i < len(s.src) {
		c := s.src[i]
		if c == ' ' || c == '\t' || c == ',' || c == ']' || c == '}' {
			i++
			continue
		}
		break
	}
	if i >= len(s.src) {
		return pos
	}
	switch {
	case s.format == "yaml" && s.src[i] == '#',
		s.format == "json" && i+1 < len(s.src) && s.src[i] == '/' && s.src[i+1] == '/':
		end, _ := lineAt(s.src, i)
		return end
	case s.format == "json" && i+1 < len(s.src) && s.src[i] == '/' && s.src[i+1] == '*':
		// A block comment: the end of the line when nothing follows it
		// there, or when it runs on past the line.
		j := bytes.Index(s.src[i+2:], []byte("*/"))
		if j < 0 {
			end, _ := lineAt(s.src, i)
			return end
		}
		close := i + 2 + j + 2
		if k := bytes.IndexAny(s.src[i:close], "\r\n"); k >= 0 {
			return i + k
		}
		for close < len(s.src) && (s.src[close] == ' ' || s.src[close] == '\t') {
			close++
		}
		if close == len(s.src) || s.src[close] == '\n' || s.src[close] == '\r' {
			return close
		}
	}
	return pos
}

// rowLine returns the start of pos's line when only white space (and, in
// YAML, the "-" of sequence entries) is before pos there; else pos.
func (s *dSplitter) rowLine(pos int) int {
	ls := pos
	for ls > 0 && s.src[ls-1] != '\n' && s.src[ls-1] != '\r' {
		c := s.src[ls-1]
		if c == ' ' || c == '\t' || s.format == "yaml" && c == '-' && ls < len(s.src) && (s.src[ls] == ' ' || s.src[ls] == '\t') {
			ls--
			continue
		}
		return pos
	}
	return ls
}

// lineStartAbove returns the start of the line that ends at pos, or -1
// when it begins before floor (which is not looked past).
func lineStartAbove(b []byte, pos, floor int) int {
	for pos > 0 && b[pos-1] != '\n' && b[pos-1] != '\r' {
		if pos <= floor {
			return -1
		}
		pos--
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
	v = gofastcore.ExpandLigatures(v)
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
	if p.pathLong {
		return ""
	}
	if path := fp + s.segment(p, u); len(path) <= maxPath {
		return path
	}
	return ""
}

// framePath returns the path of the open container f; empty, and
// f.pathLong set, when it is longer than maxPath: no longer path is built
// below it.
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
			pp := s.framePath(p)
			if p.pathLong {
				f.pathLong = true
			} else if path := pp + s.segment(p, f.unit); len(path) > maxPath {
				f.pathLong = true
			} else {
				f.path = path
			}
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
