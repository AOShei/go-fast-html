// Package blocks turns an HTML document tree into the blocks of the
// shared model: headings, paragraphs, list items, tables, code, quotes,
// captions, images, comments and form values, with white space collapsed
// as CSS white-space: normal does, links and styles as spans, and the
// reasons text is hidden.
package blocks

import (
	"context"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/dom"
)

// Config says what to produce.
type Config struct {
	Spans  bool
	Images gofastcore.ImageMode
	Budget *gofastcore.TextBudget
	Decode *gofastcore.DecodeBudget
	// Source maps an offset in the decoded text to one in the source.
	Source func(int) int64
	// Image resolves the address of an image inside the container the
	// page is in; Internal reports an address inside it, which is not
	// counted as an external reference. Both are nil for a page alone.
	Image    func(src string) (ImageRef, bool)
	Internal func(ref string) bool
}

// Result is what a walk produced.
type Result struct {
	Blocks   []gofastcore.Block
	Meta     gofastcore.Metadata
	Title    bool // a <title> was found
	Cut      bool // the text budget ran out; the blocks stop there
	Active   int  // scripts, event handler attributes, javascript: links
	External int  // resources the page loads from elsewhere
	// ImageData counts images given as data: URIs whose bytes could not
	// be decoded.
	ImageData int
	// MetaCut reports a metadata value cut to MaxMeta.
	MetaCut bool
	// Images counts img elements, whether or not blocks were made.
	Images int
	// Stylesheets counts style elements and linked stylesheets, whose
	// rules are not evaluated.
	Stylesheets int
}

// MaxMeta bounds the length of one metadata value.
const MaxMeta = 64 << 10

// MaxAnchor bounds the length of an id used as Loc.Anchor; a longer id
// is not used.
const MaxAnchor = 1024

type listCtx struct {
	ordered  bool
	reversed bool
	n        int64
	typ      string
}

type liCtx struct {
	marker  string
	ordered bool
	level   int
	used    bool
}

type walker struct {
	ctx     context.Context
	cfg     Config
	res     *Result
	buf     *buf
	owner   *dom.Node // block element whose text buf holds, if one does
	pending []gofastcore.Block
	lists   []listCtx
	li      *liCtx
	heading int
	quote   int
	caption int
	cell    int  // inside a table cell: block boundaries are line breaks
	inHead  bool // inside <head>
	extra   gofastcore.Hidden
	steps   int
	stop    bool
}

// Walk produces the blocks of the tree under root.
func Walk(ctx context.Context, root *dom.Node, cfg Config) (*Result, error) {
	w := &walker{ctx: ctx, cfg: cfg, res: &Result{}, buf: newBuf()}
	w.children(root)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.flush()
	return w.res, nil
}

func (w *walker) children(n *dom.Node) {
	for c := n.FirstChild; c != nil && !w.stop; c = c.Next {
		w.node(c)
	}
}

func (w *walker) node(n *dom.Node) {
	w.steps++
	if w.steps&4095 == 0 && w.ctx.Err() != nil {
		w.stop = true
		return
	}
	switch n.Type {
	case dom.TextNode:
		w.buf.add(n.Text, n.WS, n.Hidden|w.extra, false)
		w.buf.cover(n.Start, n.End)
	case dom.CommentNode:
		w.comment(n)
	case dom.ElementNode:
		w.element(n)
	}
}

func (w *walker) element(n *dom.Node) {
	w.activeAttrs(n)
	w.attrNotes(n)
	if n.NS != dom.HTML {
		// SVG and MathML: their text is shown where they stand.
		w.inline(n, 0)
		return
	}
	name := n.Name
	switch name {
	case "html":
		if lang, ok := n.Attr("lang"); ok && w.res.Meta.Language == "" {
			if l := cleanRef(lang); len(l) <= 64 {
				w.res.Meta.Language = l
			}
		}
	case "head":
		w.inHead = true
		w.children(n)
		w.inHead = false
		return
	case "title":
		w.title(n)
		return
	case "meta":
		w.meta(n)
		return
	case "link":
		rel, _ := n.Attr("rel")
		if strings.Contains(strings.ToLower(rel), "stylesheet") {
			w.res.Stylesheets++
		}
		if loadsResource(rel) {
			if href, _ := n.Attr("href"); strings.TrimSpace(href) != "" {
				w.external(href)
			}
		}
		return
	case "base", "basefont", "bgsound", "col", "colgroup", "param", "source", "track", "wbr", "area", "keygen":
		w.srcRef(n, "src")
		return
	case "script":
		w.script(n)
		return
	case "style":
		w.res.Stylesheets++
		w.code(n, "css")
		return
	case "br":
		w.buf.lineBreak()
		return
	case "hr":
		// A thematic break; in a table cell, only a line break.
		w.hardFlush()
		if w.cell == 0 {
			w.emit(gofastcore.Block{Kind: gofastcore.KindThematicBreak, Hidden: n.Hidden | w.extra,
				Loc: w.loc(n.Start, n.End, anchorOf(n))})
		}
		return
	case "img":
		w.image(n)
		return
	case "iframe", "frame", "embed":
		w.srcRef(n, "src")
		w.block(n)
		return
	case "object":
		w.srcRef(n, "data")
		w.block(n)
		return
	case "audio", "video":
		w.srcRef(n, "src")
		w.block(n)
		return
	case "pre", "listing", "xmp", "plaintext":
		if w.cell > 0 {
			w.inline(n, 0)
			return
		}
		w.code(n, codeLanguage(n))
		return
	case "table":
		w.table(n)
		return
	case "ul", "ol", "menu", "dir":
		w.list(n)
		return
	case "li":
		w.listItem(n)
		return
	case "h1", "h2", "h3", "h4", "h5", "h6":
		w.hardFlush()
		w.heading = int(name[1] - '0')
		w.own(n)
		w.children(n)
		w.hardFlush()
		w.heading = 0
		return
	case "blockquote":
		w.hardFlush()
		w.quote++
		w.own(n)
		w.children(n)
		w.hardFlush()
		w.quote--
		return
	case "figcaption", "caption", "legend":
		w.softFlush()
		w.caption++
		w.own(n)
		w.children(n)
		w.softFlush()
		w.caption--
		return
	case "select":
		w.choices(n)
		return
	case "textarea":
		w.field(n, string(textOf(n)), dom.Pre)
		return
	case "input":
		w.input(n)
		return
	case "rp":
		// Parentheses for browsers without ruby, which hide them.
		w.extra |= gofastcore.HiddenInvisible
		w.inline(n, 0)
		w.extra &^= gofastcore.HiddenInvisible
		return
	case "form":
		if a, _ := n.Attr("action"); strings.TrimSpace(a) != "" && !isScriptURL(a) {
			w.external(a)
		}
		w.block(n)
		return
	}
	if blockElements[name] {
		w.block(n)
		return
	}
	w.inline(n, inlineStyle[name])
}

// block walks a block element: what it holds is a block of its own.
func (w *walker) block(n *dom.Node) {
	w.softFlush()
	w.own(n)
	w.children(n)
	w.softFlush()
}

// own makes n the element whose block the buffer holds, so that the
// block's range and anchor are n's.
func (w *walker) own(n *dom.Node) {
	if w.cell > 0 {
		return
	}
	w.owner = n
	w.buf.cover(n.Start, n.End)
	if id := anchorOf(n); id != "" {
		w.buf.anchor = id
	}
}

// inline walks an inline element, recording its style, link and anchor.
func (w *walker) inline(n *dom.Node, style gofastcore.Style) {
	b := w.buf
	if b.anchor == "" {
		b.anchor = anchorOf(n)
	}
	href := ""
	if n.NS == dom.HTML && n.Name == "a" {
		href, _ = n.Attr("href")
		href = strings.TrimSpace(href)
		if isScriptURL(href) {
			w.res.Active++
		}
		if href != "" && b.href == "" {
			b.href = cleanRef(href)
		}
	}
	start := len(b.b)
	w.children(n)
	b.cover(n.Start, n.End)
	if !w.cfg.Spans || len(b.b) <= start {
		return
	}
	if style != 0 {
		b.spans = append(b.spans, gofastcore.Span{Start: start, End: len(b.b), Style: style})
	}
	if href != "" {
		b.spans = append(b.spans, gofastcore.Span{Start: start, End: len(b.b), Href: cleanRef(href)})
	}
}

// softFlush ends a block at a block boundary. Inside a list item or a
// table cell the boundary is a line break instead.
func (w *walker) softFlush() {
	if w.cell > 0 || w.li != nil && w.heading == 0 && w.quote == 0 && w.caption == 0 && !w.li.used {
		w.buf.softBreak()
		return
	}
	w.flush()
}

// hardFlush ends a block before a list, table, heading or code block.
// Inside a table cell it is a line break.
func (w *walker) hardFlush() {
	if w.cell > 0 {
		w.buf.softBreak()
		return
	}
	w.flush()
}

// flush emits the block the buffer holds, then what waited for it.
func (w *walker) flush() {
	if w.cell > 0 {
		w.buf.softBreak()
		return
	}
	text, hidden, spans := w.buf.finish()
	if text != "" {
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text, Hidden: hidden, Spans: spans}
		switch {
		case w.heading > 0:
			b.Kind, b.Level = gofastcore.KindHeading, uint8(w.heading)
		case w.li != nil && !w.li.used:
			b.Kind, b.Level, b.Ordered, b.Marker = gofastcore.KindListItem, uint8(min(w.li.level, 255)), w.li.ordered, w.li.marker
			w.li.used = true
		case w.caption > 0:
			b.Kind = gofastcore.KindCaption
		case w.quote > 0:
			b.Kind, b.Level = gofastcore.KindQuote, uint8(min(w.quote, 255))
		}
		b.Loc = w.loc(w.buf.start, w.buf.end, w.buf.anchor)
		w.emit(b)
	}
	w.buf.reset()
	w.owner = nil
	w.emitPending()
}

// emit appends a block if the budget allows; otherwise the walk stops.
func (w *walker) emit(b gofastcore.Block) {
	if w.stop {
		return
	}
	cost := gofastcore.BlockCost + len(b.Text) + len(b.Marker)
	for _, s := range b.Spans {
		cost += len(s.Href)
	}
	if b.Image != nil {
		cost += len(b.Image.ID) + len(b.Image.Data)
	}
	if !w.cfg.Budget.Take(cost) {
		w.res.Cut = true
		w.stop = true
		return
	}
	w.res.Blocks = append(w.res.Blocks, b)
}

func (w *walker) emitPending() {
	if w.cell > 0 {
		return
	}
	for i := range w.pending {
		w.emit(w.pending[i])
	}
	w.pending = w.pending[:0]
}

// later queues a block to follow the block being read, or emits it now
// when there is none.
func (w *walker) later(b gofastcore.Block) {
	if w.cell == 0 && len(w.buf.b) == 0 {
		w.emit(b)
		return
	}
	w.pending = append(w.pending, b)
}

func (w *walker) loc(start, end int, anchor string) gofastcore.Location {
	var l gofastcore.Location
	if start >= 0 && end > start && w.cfg.Source != nil {
		l.Offset = w.cfg.Source(start)
		l.Length = w.cfg.Source(end) - l.Offset
	}
	l.Anchor = anchor
	return l
}

// list walks ul, ol, menu or dir.
func (w *walker) list(n *dom.Node) {
	w.hardFlush()
	lc := listCtx{ordered: n.Name == "ol"}
	lc.typ, _ = n.Attr("type")
	if _, ok := n.Attr("reversed"); ok && lc.ordered {
		lc.reversed = true
	}
	lc.n = 1
	if lc.reversed {
		lc.n = 0
		for c := n.FirstChild; c != nil; c = c.Next {
			if c.Type == dom.ElementNode && c.Name == "li" {
				lc.n++
			}
		}
	}
	if s, ok := n.Attr("start"); ok && lc.ordered {
		if v, ok := parseInt(s); ok {
			lc.n = v
		}
	}
	w.lists = append(w.lists, lc)
	w.children(n)
	w.hardFlush()
	w.lists = w.lists[:len(w.lists)-1]
}

// listItem walks an li. Its text up to the first nested list, table,
// heading or code block is the list item; block boundaries within it
// are line breaks.
func (w *walker) listItem(n *dom.Node) {
	w.hardFlush()
	li := &liCtx{level: max(len(w.lists), 1)}
	if len(w.lists) > 0 {
		lc := &w.lists[len(w.lists)-1]
		li.ordered = lc.ordered
		if v, ok := n.Attr("value"); ok && lc.ordered {
			if x, ok := parseInt(v); ok {
				lc.n = x
			}
		}
		li.marker = marker(lc, len(w.lists))
		if lc.reversed {
			lc.n--
		} else {
			lc.n++
		}
	} else {
		li.marker = "•"
	}
	saved, savedCell := w.li, w.cell
	if w.cell > 0 {
		w.children(n)
		w.buf.softBreak()
		return
	}
	w.li = li
	w.own(n)
	w.children(n)
	w.hardFlush()
	w.li, w.cell = saved, savedCell
}

// code emits the text of a pre, script or style element as a code block,
// verbatim.
func (w *walker) code(n *dom.Node, lang string) {
	w.hardFlush()
	if w.stop {
		return
	}
	saved := w.buf
	w.buf = newBuf()
	w.verbatim(n)
	b := w.buf
	w.buf = saved
	text, hidden, spans := b.verbatimText()
	if text == "" {
		return
	}
	blk := gofastcore.Block{Kind: gofastcore.KindCode, Text: text, Hidden: hidden, Spans: spans,
		Loc: w.loc(n.Start, n.End, anchorOf(n))}
	if lang != "" {
		blk.Attrs = map[string]string{gofastcore.AttrLanguage: lang}
	}
	if w.cell > 0 {
		w.pending = append(w.pending, blk)
		return
	}
	w.emit(blk)
}

// verbatim adds the text under n as it stands, a <br> as a line break.
func (w *walker) verbatim(n *dom.Node) {
	for c := n.FirstChild; c != nil; c = c.Next {
		switch c.Type {
		case dom.TextNode:
			w.buf.add(c.Text, dom.Pre, c.Hidden|w.extra, true)
		case dom.ElementNode:
			w.activeAttrs(c)
			if c.Name == "br" {
				w.buf.b = append(w.buf.b, '\n')
				continue
			}
			if c.Name == "img" {
				w.image(c)
				continue
			}
			w.verbatim(c)
		case dom.CommentNode:
			w.comment(c)
		}
	}
}

// verbatimText returns code text as it stands; only a text of nothing
// but white space is dropped.
func (b *buf) verbatimText() (string, gofastcore.Hidden, []gofastcore.Span) {
	if isBlank(b.b) {
		return "", 0, nil
	}
	var spans []gofastcore.Span
	h := b.hiddenSpans(0, len(b.b), &spans)
	return string(b.b), h, spans
}

func (w *walker) script(n *dom.Node) {
	typ, _ := n.Attr("type")
	typ = strings.ToLower(strings.TrimSpace(typ))
	lang := "javascript"
	switch {
	case typ == "" || strings.Contains(typ, "javascript") || strings.Contains(typ, "ecmascript") || typ == "module" || typ == "text/jscript" || typ == "text/livescript":
		w.res.Active++
	case strings.Contains(typ, "json"):
		lang = "json"
	default:
		lang = ""
	}
	w.srcRef(n, "src")
	w.code(n, lang)
}

func (w *walker) comment(n *dom.Node) {
	text := cleanProse(string(n.Text), false)
	if text == "" {
		return
	}
	w.later(gofastcore.Block{Kind: gofastcore.KindNote, Text: text,
		Hidden: n.Hidden | gofastcore.HiddenInvisible,
		Attrs:  map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment},
		Loc:    w.loc(n.Start, n.End, "")})
}

// choices emits the options of a select as a note of type choices.
func (w *walker) choices(n *dom.Node) {
	var lines []string
	var walk func(*dom.Node)
	walk = func(p *dom.Node) {
		for c := p.FirstChild; c != nil; c = c.Next {
			if c.Type != dom.ElementNode {
				continue
			}
			w.activeAttrs(c)
			if c.Name == "option" {
				t := cleanProse(string(textOf(c)), true)
				if t == "" {
					t, _ = c.Attr("label")
					t = cleanProse(t, true)
				}
				if t != "" {
					lines = append(lines, t)
				}
				continue
			}
			walk(c)
		}
	}
	walk(n)
	if len(lines) == 0 {
		return
	}
	b := gofastcore.Block{Kind: gofastcore.KindNote, Text: strings.Join(lines, "\n"), Hidden: n.Hidden,
		Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteChoices},
		Loc:   w.loc(n.Start, n.End, anchorOf(n))}
	if name, _ := n.Attr("name"); cleanRef(name) != "" {
		b.Attrs[gofastcore.AttrField] = cleanRef(name)
	}
	w.later(b)
}

// input reads the value of a form control.
func (w *walker) input(n *dom.Node) {
	typ, _ := n.Attr("type")
	typ = strings.ToLower(strings.TrimSpace(typ))
	switch typ {
	case "image":
		w.srcRef(n, "src")
		if alt, _ := n.Attr("alt"); alt != "" {
			w.buf.add([]byte(alt), dom.Normal, n.Hidden|w.extra, false)
		}
		return
	case "submit", "button", "reset":
		// The value is the button's label, shown where it stands.
		if v, ok := n.Attr("value"); ok {
			w.buf.pending = true
			w.buf.add([]byte(v), dom.Normal, n.Hidden|w.extra, false)
			w.buf.pending = true
		}
		return
	case "checkbox", "radio", "file", "range", "color":
		return
	}
	v, ok := n.Attr("value")
	if !ok || strings.TrimSpace(v) == "" {
		return
	}
	w.field(n, v, dom.Normal)
}

// field emits the value of a form control as a paragraph that names the
// control.
func (w *walker) field(n *dom.Node, value string, ws dom.WS) {
	b := newBuf()
	b.add([]byte(value), ws, n.Hidden|w.extra, false)
	text, hidden, spans := b.finish()
	if text == "" {
		return
	}
	blk := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text, Hidden: hidden, Spans: spans,
		Loc: w.loc(n.Start, n.End, anchorOf(n))}
	if name, _ := n.Attr("name"); cleanRef(name) != "" {
		blk.Attrs = map[string]string{gofastcore.AttrField: cleanRef(name)}
	}
	w.later(blk)
}

// activeAttrs counts event handler attributes and javascript: addresses.
func (w *walker) activeAttrs(n *dom.Node) {
	for _, a := range n.Attrs {
		if strings.HasPrefix(a.Name, "on") && len(a.Name) > 2 {
			w.res.Active++
			continue
		}
		switch a.Name {
		case "src", "action", "formaction", "data":
			if isScriptURL(a.Value) {
				w.res.Active++
			}
		}
	}
}

// srcRef counts a resource the element loads from elsewhere.
func (w *walker) srcRef(n *dom.Node, attr string) {
	v, ok := n.Attr(attr)
	v = strings.TrimSpace(v)
	if !ok || v == "" || isScriptURL(v) || hasScheme(v, "data") {
		return
	}
	w.external(v)
}

// external counts a reference to a resource, unless it is inside the
// container the page is in.
func (w *walker) external(ref string) {
	if w.cfg.Internal != nil && w.cfg.Internal(strings.TrimSpace(ref)) {
		return
	}
	w.res.External++
}
