package blocks

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/dom"
)

const (
	maxColSpan = 1000  // as the HTML standard clamps colspan
	maxRowSpan = 65534 // and rowspan
)

type tableRow struct {
	cells  []*dom.Node
	header bool // in a thead
}

type placed struct {
	row, col, rs, cs int
	text             string
	hidden           gofastcore.Hidden
	href             string
}

// gather collects the rows of a table, and the elements inside it that
// are not rows or cells (scripts, templates, hidden inputs), which are
// read after it.
func gather(n *dom.Node, header bool, rows *[]tableRow, captions, other *[]*dom.Node) {
	for c := n.FirstChild; c != nil; c = c.Next {
		if c.Type == dom.CommentNode {
			*other = append(*other, c)
			continue
		}
		if c.Type != dom.ElementNode {
			continue
		}
		if c.NS != dom.HTML {
			*other = append(*other, c)
			continue
		}
		switch c.Name {
		case "caption":
			*captions = append(*captions, c)
		case "thead":
			gather(c, true, rows, captions, other)
		case "tbody", "tfoot", "form":
			gather(c, header, rows, captions, other)
		case "tr":
			row := tableRow{header: header}
			for d := c.FirstChild; d != nil; d = d.Next {
				if d.Type == dom.ElementNode && d.NS == dom.HTML && (d.Name == "td" || d.Name == "th") {
					row.cells = append(row.cells, d)
				} else if d.Type != dom.TextNode {
					*other = append(*other, d)
				}
			}
			*rows = append(*rows, row)
		case "col", "colgroup":
		default:
			*other = append(*other, c)
		}
	}
}

func (w *walker) table(n *dom.Node) {
	var rows []tableRow
	var captions, other []*dom.Node
	gather(n, false, &rows, &captions, &other)
	if w.cell > 0 {
		w.flatTable(rows, captions, other)
		return
	}
	w.hardFlush()
	for _, c := range captions {
		w.element(c)
	}
	w.hardFlush()

	// Place the cells on the grid.
	var cells []placed
	var busy []int // per column, the row up to which it is covered
	for r, row := range rows {
		c := 0
		for _, td := range row.cells {
			if w.stop {
				return
			}
			for c < len(busy) && busy[c] > r {
				c++
			}
			cs := spanAttr(td, "colspan", 1, maxColSpan)
			rs := spanAttr(td, "rowspan", 0, maxRowSpan)
			if rs == 0 || rs > len(rows)-r {
				rs = len(rows) - r
			}
			// A grid wider than the budget can hold one row of is cut
			// here, before the columns are made (spans make it cheap).
			if int64(c+cs)*gofastcore.CellCost > w.cfg.Budget.Left()-gofastcore.BlockCost {
				w.res.Cut, w.stop = true, true
				return
			}
			for len(busy) < c+cs {
				busy = append(busy, 0)
			}
			for k := c; k < c+cs; k++ {
				busy[k] = r + rs
			}
			text, hidden, href := w.cellText(td)
			if !w.cfg.Budget.Take(len(text) + len(href)) {
				w.res.Cut, w.stop = true, true
				return
			}
			cells = append(cells, placed{r, c, rs, cs, text, hidden, href})
			c += cs
		}
	}
	width := len(busy)
	if width > 0 {
		w.emitTable(n, rows, cells, width)
	}
	for _, o := range other {
		w.node(o)
	}
	w.flush()
}

// spanAttr reads colspan or rowspan, clamped as the standard does.
func spanAttr(n *dom.Node, name string, lo, hi int) int {
	v, ok := n.Attr(name)
	if !ok {
		return 1
	}
	x, ok := parseInt(v)
	if !ok || x < int64(lo) {
		return 1
	}
	return int(min(x, int64(hi)))
}

// cellText reads a cell's text: block boundaries in it are line breaks.
func (w *walker) cellText(td *dom.Node) (string, gofastcore.Hidden, string) {
	saved := w.buf
	w.buf = newBuf()
	w.cell++
	w.children(td)
	w.cell--
	text, hidden, _ := w.buf.finish()
	href := w.buf.href
	w.buf = saved
	if !w.cfg.Spans {
		href = ""
	}
	return text, hidden, href
}

func (w *walker) emitTable(n *dom.Node, rows []tableRow, cells []placed, width int) {
	height := len(rows)
	// Rows of the grid that fit in the budget, at CellCost a cell.
	left := w.cfg.Budget.Left() - gofastcore.BlockCost
	fit := height
	if per := int64(gofastcore.CellCost * width); left < per*int64(height) {
		fit = int(max(left/per, 0))
	}
	if fit == 0 {
		w.res.Cut, w.stop = true, true
		return
	}
	w.cfg.Budget.Take(gofastcore.BlockCost + gofastcore.CellCost*width*fit)
	if fit < height {
		w.res.Cut, w.stop = true, true
	}
	grid := make([][]string, fit)
	for i := range grid {
		grid[i] = make([]string, width)
	}
	t := &gofastcore.Table{Rows: grid}
	for t.HeaderRows < fit && headerRow(rows[t.HeaderRows]) {
		t.HeaderRows++
	}
	whole, any, seen := gofastcore.HiddenAny, n.Hidden, false
	for _, p := range cells {
		if p.row >= fit || p.text == "" {
			continue
		}
		whole &= p.hidden
		any |= p.hidden
		seen = true
	}
	if !seen {
		whole = n.Hidden
	}
	for _, p := range cells {
		if p.row >= fit {
			continue
		}
		grid[p.row][p.col] = p.text
		rs := min(p.rs, fit-p.row)
		if rs > 1 || p.cs > 1 {
			t.Merges = append(t.Merges, gofastcore.Merge{Row: p.row, Col: p.col, RowSpan: rs, ColSpan: p.cs})
		}
		if p.href != "" {
			t.Cells = append(t.Cells, gofastcore.CellDetail{Row: p.row, Col: p.col, Href: p.href})
		}
		if part := p.hidden &^ whole; part != 0 && p.text != "" {
			t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: p.row, Col: p.col, RowSpan: rs, ColSpan: p.cs, Hidden: part})
		}
	}
	w.res.Blocks = append(w.res.Blocks, gofastcore.Block{Kind: gofastcore.KindTable, Table: t, Hidden: any,
		Loc: w.loc(n.Start, n.End, anchorOf(n))})
}

// headerRow reports a row of a thead, or one whose cells are all th.
func headerRow(r tableRow) bool {
	if r.header {
		return true
	}
	if len(r.cells) == 0 {
		return false
	}
	for _, c := range r.cells {
		if c.Name != "th" {
			return false
		}
	}
	return true
}

// flatTable writes a table inside a table cell into the cell's text: a
// line for each row, its cells separated by spaces.
func (w *walker) flatTable(rows []tableRow, captions, other []*dom.Node) {
	w.buf.softBreak()
	for _, c := range captions {
		w.children(c)
		w.buf.softBreak()
	}
	for _, r := range rows {
		for _, td := range r.cells {
			w.buf.pending = true
			w.children(td)
			w.buf.pending = true
		}
		w.buf.softBreak()
	}
	for _, o := range other {
		w.node(o)
	}
}
