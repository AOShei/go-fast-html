package dom

import (
	"context"
	"strings"

	"github.com/AOShei/go-fast-html/internal/css"
	"github.com/AOShei/go-fast-html/internal/lexer"
)

// Defaults for Options.
const (
	// DefaultMaxDepth is the deepest an element is nested in the tree.
	// Browsers stop near the same depth (Blink at 512).
	DefaultMaxDepth = 512
	// DefaultMaxStack bounds the stack of open elements. Past it an
	// element is attached but not kept open, and the reasons it hides
	// text are given to the element it is in, so that hidden text stays
	// flagged; text after it in that element may be flagged too.
	DefaultMaxStack = 4096
	// walkLimit bounds how far down the stack an end tag looks for its
	// element, which keeps the cost of each tag bounded.
	walkLimit = 512
)

// Options bounds the tree.
type Options struct {
	MaxDepth int
	MaxStack int
	// XHTML reads self-closing tags as empty elements (EPUB content).
	XHTML bool
	// NavDoc reads an EPUB navigation document: the hidden attribute on
	// a nav or ol element, which EPUB 3 provides to keep landmarks, the
	// page list or deeper levels out of view, is not a reason there.
	NavDoc bool
}

// Doc is a parsed document.
type Doc struct {
	Root *Node
	// Limited reports that a nesting bound was reached and the structure
	// below it was flattened.
	Limited bool
}

type entry struct {
	node *Node
	box  *Node // where content goes: node, or an ancestor past MaxDepth
	st   state
}

type afeItem struct {
	node *Node // nil for a marker
}

type builder struct {
	lx       *lexer.Lexer
	root     *Node
	stack    []entry
	afe      []afeItem
	maxDepth int32
	maxStack int
	clones   int
	limited  bool
	skipLF   bool
	lastEnd  int
	seenHTML bool
	seenHead bool
	seenBody bool
	styles   map[*lexer.Attr]*css.Style
	xhtml    bool
	navDoc   bool
}

// Parse builds the tree of src, which must be UTF-8. It returns ctx's
// error if ctx is cancelled.
func Parse(ctx context.Context, src []byte, opt Options) (*Doc, error) {
	b := &builder{
		lx:       lexer.New(src),
		root:     &Node{Type: DocumentNode, open: true},
		maxDepth: int32(opt.MaxDepth),
		maxStack: opt.MaxStack,
		clones:   len(src)/4 + 4096,
		xhtml:    opt.XHTML,
		navDoc:   opt.NavDoc,
	}
	if b.maxDepth <= 0 {
		b.maxDepth = DefaultMaxDepth
	}
	if b.maxStack <= 0 {
		b.maxStack = DefaultMaxStack
	}
	b.root.End = len(src)
	b.stack = append(b.stack, entry{node: b.root, box: b.root, st: rootState()})
	for n := 0; ; n++ {
		if n&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		t := b.lx.Next()
		if t.Kind == lexer.EOF {
			break
		}
		b.lastEnd = t.Start
		b.token(&t)
		top := b.top()
		b.lx.CDATA = top.node.NS != HTML
	}
	b.lastEnd = len(src)
	b.popTo(1)
	return &Doc{Root: b.root, Limited: b.limited}, nil
}

func (b *builder) top() *entry { return &b.stack[len(b.stack)-1] }

func (b *builder) token(t *lexer.Token) {
	switch t.Kind {
	case lexer.Text:
		b.text(t)
	case lexer.Comment:
		if b.xhtml && strings.HasPrefix(t.Data, "?") {
			// An XML declaration or processing instruction, not a
			// comment, in XHTML.
			return
		}
		top := b.top()
		n := &Node{Type: CommentNode, Text: []byte(t.Data), Start: t.Start, End: t.End, Hidden: top.st.textHidden()}
		b.attach(top.box, n, nil)
	case lexer.StartTag:
		if b.inForeign() {
			b.foreignStart(t)
			return
		}
		b.startTag(t)
	case lexer.EndTag:
		// End tags take the rules for foreign content whenever the
		// current node is foreign, integration point or not.
		if b.top().node.NS != HTML {
			b.foreignEnd(t)
			return
		}
		b.endTag(t)
	}
}

// attach adds n under parent, before ref.
func (b *builder) attach(parent, n, ref *Node) {
	parent.insertBefore(n, ref)
}

func (b *builder) text(t *lexer.Token) {
	data := t.Data
	if b.skipLF {
		b.skipLF = false
		switch {
		case strings.HasPrefix(data, "\r\n"):
			data = data[2:]
		case strings.HasPrefix(data, "\n"), strings.HasPrefix(data, "\r"):
			data = data[1:]
		}
	}
	if data == "" {
		return
	}
	space := allSpace(data)
	top := b.top()
	if !space && top.node.NS == HTML && top.node.Name == "head" {
		b.closeHead()
		top = b.top()
	}
	parent, ref, st := top.box, (*Node)(nil), &top.st
	if !space && isTableContext(top.node) {
		parent, ref, st = b.fosterPlace()
	} else if !space {
		b.reconstruct()
		top = b.top()
		parent, st = top.box, &top.st
	}
	h := st.textHidden()
	// Join to the text before it when nothing differs.
	prev := parent.LastChild
	if ref != nil {
		prev = ref.Prev
	}
	if prev != nil && prev.Type == TextNode && prev.Hidden == h && prev.WS == st.ws && prev.End == t.Start {
		prev.Text = append(prev.Text, data...)
		prev.End = t.End
		return
	}
	n := &Node{Type: TextNode, Text: []byte(data), Start: t.Start, End: t.End, Hidden: h, WS: st.ws}
	b.attach(parent, n, ref)
}

// fosterPlace returns where content misplaced in a table goes: before the
// table, in the table's parent.
func (b *builder) fosterPlace() (*Node, *Node, *state) {
	for i := len(b.stack) - 1; i > 0; i-- {
		e := &b.stack[i]
		if e.node.NS == HTML && e.node.Name == "table" {
			below := &b.stack[i-1]
			ref := e.node
			if ref.Parent != below.box {
				ref = nil
			}
			return below.box, ref, &below.st
		}
	}
	top := b.top()
	return top.box, nil, &top.st
}

// insert creates the element of t, attaches it and, unless void, pushes
// it. It returns the node.
func (b *builder) insert(t *lexer.Token, ns NS, void bool) *Node {
	top := b.top()
	parent, ref, pst := top.box, (*Node)(nil), &top.st
	if isTableContext(top.node) && ns == HTML && !tableContent(t) {
		parent, ref, pst = b.fosterPlace()
	}
	return b.insertAt(t, ns, void, parent, ref, pst, true)
}

func (b *builder) insertAt(t *lexer.Token, ns NS, void bool, parent, ref *Node, pst *state, attach bool) *Node {
	attrs := t.Attrs
	if b.navDoc && ns == HTML && (t.Name == "nav" || t.Name == "ol") {
		// The hidden attribute of EPUB navigation is not a reason.
		attrs = make([]lexer.Attr, 0, len(t.Attrs))
		for _, a := range t.Attrs {
			if a.Name != "hidden" {
				attrs = append(attrs, a)
			}
		}
	}
	st := pst.child(t.Name, ns, attrs, b.style(t.Attrs))
	n := &Node{Type: ElementNode, Name: t.Name, NS: ns, Attrs: t.Attrs, Start: t.Start, End: t.End, Hidden: st.hidden(), WS: st.ws}
	box := parent
	if attach {
		b.attach(parent, n, ref)
		if n.depth < b.maxDepth {
			box = n
		} else {
			b.limited = true
		}
	}
	if void {
		return n
	}
	if len(b.stack) >= b.maxStack {
		// Not kept open: what it hides, it hides in the element it is in.
		b.limited = true
		b.top().st.sticky |= st.hidden()
		return n
	}
	n.open = true
	b.stack = append(b.stack, entry{node: n, box: box, st: st})
	return n
}

// style parses the style attribute among attrs. A formatting element
// reopened many times shares its attributes with the original, so the
// parse is kept for it.
func (b *builder) style(attrs []lexer.Attr) *css.Style {
	for i := range attrs {
		if attrs[i].Name != "style" {
			continue
		}
		key := &attrs[i]
		if st, ok := b.styles[key]; ok {
			return st
		}
		st := css.Parse(attrs[i].Value)
		if b.styles == nil {
			b.styles = map[*lexer.Attr]*css.Style{}
		}
		b.styles[key] = &st
		return &st
	}
	return nil
}

// popTo pops the open elements from index i up.
func (b *builder) popTo(i int) {
	if i < 1 {
		i = 1
	}
	for len(b.stack) > i {
		e := b.stack[len(b.stack)-1]
		b.stack = b.stack[:len(b.stack)-1]
		e.node.open = false
		if b.lastEnd > e.node.End {
			e.node.End = b.lastEnd
		}
		if e.node.NS == HTML && pushesMarker[e.node.Name] {
			b.clearToMarker()
		}
	}
}

// find returns the stack index of the nearest open HTML element named
// name, looking no further than a boundary of the given scope; -1 if
// there is none.
func (b *builder) find(name string, scope map[string]bool) int {
	for i := len(b.stack) - 1; i > 0 && i >= len(b.stack)-walkLimit; i-- {
		n := b.stack[i].node
		if n.NS == HTML && n.Name == name {
			return i
		}
		if scope != nil && isBoundary(n, scope) {
			return -1
		}
	}
	return -1
}

func isBoundary(n *Node, scope map[string]bool) bool {
	switch n.NS {
	case HTML:
		return scope[n.Name]
	case MathML:
		switch n.Name {
		case "mi", "mo", "mn", "ms", "mtext", "annotation-xml":
			return true
		}
	case SVG:
		switch n.Name {
		case "foreignobject", "desc", "title":
			return true
		}
	}
	return false
}

func (b *builder) closeP() {
	if i := b.find("p", buttonScope); i >= 0 {
		b.popTo(i)
	}
}

func (b *builder) closeHead() {
	if i := b.find("head", nil); i >= 0 {
		b.popTo(i)
	}
}

func (b *builder) currentIs(names ...string) bool {
	n := b.top().node
	if n.NS != HTML {
		return false
	}
	for _, name := range names {
		if n.Name == name {
			return true
		}
	}
	return false
}

// startTag handles a start tag. In XHTML a self-closing tag is an empty
// element, as XML has it, where HTML would leave the element open.
func (b *builder) startTag(t *lexer.Token) {
	if !b.xhtml || !t.SelfClosing || void[t.Name] {
		b.startTagHTML(t)
		return
	}
	n := len(b.stack)
	b.startTagHTML(t)
	b.lx.SetRaw(lexer.Data, "")
	b.skipLF = false
	if len(b.stack) > n {
		top := b.top().node
		if top.Name == t.Name || t.Name == "image" && top.Name == "img" {
			b.popTo(len(b.stack) - 1)
			b.removeAfe(top)
		}
	}
}

func (b *builder) startTagHTML(t *lexer.Token) {
	name := t.Name
	if name == "image" {
		name = "img"
		t.Name = name
	}
	if b.currentIs("head") && !headContent[name] {
		b.closeHead()
	}
	switch {
	case name == "html":
		if !b.seenHTML && len(b.stack) == 1 {
			b.seenHTML = true
			b.insert(t, HTML, false)
		}
	case name == "head":
		if !b.seenHead && !b.seenBody {
			b.seenHead = true
			b.insert(t, HTML, false)
		}
	case name == "body":
		if !b.seenBody {
			b.seenBody = true
			b.insert(t, HTML, false)
		}
	case name == "frameset":
	case closesP[name]:
		b.closeP()
		if headings[name] && b.currentIs("h1", "h2", "h3", "h4", "h5", "h6") {
			b.popTo(len(b.stack) - 1)
		}
		switch name {
		case "pre", "listing":
			b.insert(t, HTML, false)
			b.skipLF = true
		case "plaintext":
			b.insert(t, HTML, false)
			b.lx.SetRaw(lexer.Plaintext, name)
		case "xmp":
			b.reconstruct()
			b.insert(t, HTML, false)
			b.lx.SetRaw(lexer.RAWTEXT, name)
		case "table":
			if isTableContext(b.top().node) {
				if i := b.find("table", tableScope); i >= 0 {
					b.popTo(i)
				}
			}
			b.insert(t, HTML, false)
		case "hr":
			b.insert(t, HTML, true)
		default:
			b.insert(t, HTML, false)
		}
	case name == "li" || name == "dd" || name == "dt":
		for i := len(b.stack) - 1; i > 0 && i >= len(b.stack)-walkLimit; i-- {
			n := b.stack[i].node
			if n.NS == HTML && (n.Name == name || name != "li" && (n.Name == "dd" || n.Name == "dt")) {
				b.popTo(i)
				break
			}
			if isSpecial(n) && n.Name != "address" && n.Name != "div" && n.Name != "p" {
				break
			}
		}
		b.closeP()
		b.insert(t, HTML, false)
	case name == "button":
		if i := b.find("button", defaultScope); i >= 0 {
			b.popTo(i)
		}
		b.reconstruct()
		b.insert(t, HTML, false)
	case name == "a":
		for i := len(b.afe) - 1; i >= 0 && b.afe[i].node != nil; i-- {
			if b.afe[i].node.Name == "a" {
				old := b.afe[i].node
				b.adoption("a", t.Start)
				b.removeAfe(old)
				b.removeFromStack(old)
				break
			}
		}
		b.reconstruct()
		b.pushAfe(b.insert(t, HTML, false))
	case formatting[name]:
		if name == "nobr" {
			b.reconstruct()
			if b.find("nobr", defaultScope) >= 0 {
				b.adoption("nobr", t.Start)
			}
		}
		b.reconstruct()
		b.pushAfe(b.insert(t, HTML, false))
	case name == "applet" || name == "marquee" || name == "object":
		b.reconstruct()
		b.insert(t, HTML, false)
		b.afe = append(b.afe, afeItem{})
	case name == "template":
		b.insert(t, HTML, false)
		b.afe = append(b.afe, afeItem{})
	case name == "textarea":
		b.insert(t, HTML, false)
		b.lx.SetRaw(lexer.RCDATA, name)
		b.skipLF = true
	case name == "title":
		b.insert(t, HTML, false)
		b.lx.SetRaw(lexer.RCDATA, name)
	case name == "style" || name == "iframe" || name == "noembed" || name == "noframes":
		b.insert(t, HTML, false)
		b.lx.SetRaw(lexer.RAWTEXT, name)
	case name == "script":
		b.insert(t, HTML, false)
		b.lx.SetRaw(lexer.ScriptData, name)
	case name == "option":
		if b.currentIs("option") {
			b.popTo(len(b.stack) - 1)
		}
		b.reconstruct()
		b.insert(t, HTML, false)
	case name == "optgroup":
		if b.currentIs("option") {
			b.popTo(len(b.stack) - 1)
		}
		if b.currentIs("optgroup") {
			b.popTo(len(b.stack) - 1)
		}
		b.insert(t, HTML, false)
	case name == "svg" || name == "math":
		b.reconstruct()
		ns := SVG
		if name == "math" {
			ns = MathML
		}
		b.insert(t, ns, t.SelfClosing)
	case tableParts[name]:
		b.tablePart(t)
	case void[name]:
		if name != "meta" && name != "link" && name != "base" {
			b.reconstruct()
		}
		b.insert(t, HTML, true)
	default:
		b.reconstruct()
		b.insert(t, HTML, false)
	}
}

// tablePart handles caption, colgroup, col, tbody, thead, tfoot, tr, td
// and th. Outside a table they are ignored, as the standard does.
func (b *builder) tablePart(t *lexer.Token) {
	if b.find("table", tableScope) < 0 {
		return
	}
	name := t.Name
	closeCell := func() {
		for _, c := range []string{"td", "th"} {
			if i := b.find(c, tableScope); i >= 0 {
				b.popTo(i)
			}
		}
	}
	clearTo := func(stops ...string) {
		for len(b.stack) > 1 && !b.currentIs(stops...) {
			b.popTo(len(b.stack) - 1)
		}
	}
	switch name {
	case "caption", "colgroup", "tbody", "thead", "tfoot":
		closeCell()
		clearTo("table", "template")
		b.insert(t, HTML, false)
		if name == "caption" {
			b.afe = append(b.afe, afeItem{})
		}
	case "col":
		closeCell()
		if !b.currentIs("colgroup") {
			clearTo("table", "template")
		}
		b.insert(t, HTML, true)
	case "tr":
		closeCell()
		if i := b.find("tr", tableScope); i >= 0 {
			b.popTo(i)
		}
		clearTo("tbody", "thead", "tfoot", "table", "template")
		b.insert(t, HTML, false)
	case "td", "th":
		closeCell()
		if i := b.find("tr", tableScope); i >= 0 {
			b.popTo(i + 1)
		} else {
			clearTo("tbody", "thead", "tfoot", "table", "template")
			b.insert(&lexer.Token{Kind: lexer.StartTag, Name: "tr", Start: t.Start, End: t.Start}, HTML, false)
		}
		b.insert(t, HTML, false)
		b.afe = append(b.afe, afeItem{})
	}
}

func (b *builder) endTag(t *lexer.Token) {
	name := t.Name
	switch {
	case name == "body" || name == "html":
	case name == "head":
		b.closeHead()
	case name == "br":
		b.reconstruct()
		b.insert(&lexer.Token{Kind: lexer.StartTag, Name: "br", Start: t.Start, End: t.End}, HTML, true)
	case name == "p":
		if i := b.find("p", buttonScope); i >= 0 {
			b.popEnd(i, t)
		}
	case name == "li":
		if i := b.find("li", listItemScope); i >= 0 {
			b.popEnd(i, t)
		}
	case name == "dd" || name == "dt":
		if i := b.find(name, defaultScope); i >= 0 {
			b.popEnd(i, t)
		}
	case headings[name]:
		for i := len(b.stack) - 1; i > 0 && i >= len(b.stack)-walkLimit; i-- {
			n := b.stack[i].node
			if n.NS == HTML && headings[n.Name] {
				b.popEnd(i, t)
				return
			}
			if isBoundary(n, defaultScope) {
				return
			}
		}
	case formatting[name] || name == "a":
		b.lastEnd = t.End
		b.adoption(name, t.End)
	case name == "table" || tableParts[name]:
		if i := b.find(name, tableScope); i >= 0 {
			b.popEnd(i, t)
		}
	default:
		b.anyOtherEnd(t)
	}
}

func (b *builder) popEnd(i int, t *lexer.Token) {
	b.lastEnd = t.End
	b.popTo(i)
}

func (b *builder) anyOtherEnd(t *lexer.Token) {
	for i := len(b.stack) - 1; i > 0 && i >= len(b.stack)-walkLimit; i-- {
		n := b.stack[i].node
		if n.NS == HTML && n.Name == t.Name {
			b.popEnd(i, t)
			return
		}
		if isSpecial(n) {
			return
		}
	}
}

// adoption is a simplified adoption agency algorithm for the end tag of
// a formatting element. When no block was opened inside the element, the
// element is closed and the formatting elements inside it are reopened
// for what follows. When one was, the element is only taken off the stack
// and the block stays open, so that its text stays where it is.
func (b *builder) adoption(name string, end int) {
	fi := -1
	for i := len(b.afe) - 1; i >= 0 && b.afe[i].node != nil; i-- {
		if b.afe[i].node.Name == name {
			fi = i
			break
		}
	}
	if fi < 0 {
		b.anyOtherEnd(&lexer.Token{Name: name, End: end})
		return
	}
	fe := b.afe[fi].node
	si := -1
	for i := len(b.stack) - 1; i > 0 && i >= len(b.stack)-walkLimit; i-- {
		if b.stack[i].node == fe {
			si = i
			break
		}
		if isBoundary(b.stack[i].node, defaultScope) {
			return // not in scope: ignored
		}
	}
	if si < 0 {
		b.removeAfe(fe)
		return
	}
	for i := si + 1; i < len(b.stack); i++ {
		if isSpecial(b.stack[i].node) {
			b.removeAfe(fe)
			b.removeFromStack(fe)
			if end > fe.End {
				fe.End = end
			}
			return
		}
	}
	b.removeAfe(fe)
	b.popTo(si)
}

func (b *builder) removeAfe(n *Node) {
	for i := len(b.afe) - 1; i >= 0; i-- {
		if b.afe[i].node == n {
			b.afe = append(b.afe[:i], b.afe[i+1:]...)
			return
		}
	}
}

func (b *builder) removeFromStack(n *Node) {
	for i := len(b.stack) - 1; i > 0; i-- {
		if b.stack[i].node == n {
			n.open = false
			b.stack = append(b.stack[:i], b.stack[i+1:]...)
			return
		}
	}
}

const maxAfe = 64

func (b *builder) pushAfe(n *Node) {
	if !n.open {
		return
	}
	// Noah's ark: no more than three equal elements since the last marker.
	same, first := 0, -1
	for i := len(b.afe) - 1; i >= 0 && b.afe[i].node != nil; i-- {
		if o := b.afe[i].node; o.Name == n.Name && equalAttrs(o.Attrs, n.Attrs) {
			same++
			first = i
		}
	}
	if same >= 3 {
		b.afe = append(b.afe[:first], b.afe[first+1:]...)
	}
	if len(b.afe) >= maxAfe {
		b.afe = append(b.afe[:0], b.afe[1:]...)
	}
	b.afe = append(b.afe, afeItem{node: n})
}

func (b *builder) clearToMarker() {
	for len(b.afe) > 0 {
		last := b.afe[len(b.afe)-1]
		b.afe = b.afe[:len(b.afe)-1]
		if last.node == nil {
			return
		}
	}
}

// reconstruct reopens the formatting elements that a block closed, so
// that they apply to what follows, as browsers do.
func (b *builder) reconstruct() {
	n := len(b.afe)
	if n == 0 || b.afe[n-1].node == nil || b.afe[n-1].node.open {
		return
	}
	i := n - 1
	for i > 0 && b.afe[i-1].node != nil && !b.afe[i-1].node.open {
		i--
	}
	for ; i < n; i++ {
		old := b.afe[i].node
		t := &lexer.Token{Kind: lexer.StartTag, Name: old.Name, Attrs: old.Attrs, Start: b.lastEnd, End: b.lastEnd}
		top := b.top()
		parent, ref, pst := top.box, (*Node)(nil), &top.st
		if isTableContext(top.node) {
			parent, ref, pst = b.fosterPlace()
		}
		// Past the bound on clones, the element still applies to what
		// follows but is not put in the tree.
		attach := b.clones > 0
		b.clones--
		b.afe[i].node = b.insertAt(t, HTML, false, parent, ref, pst, attach)
	}
}

func (b *builder) inForeign() bool {
	n := b.top().node
	switch n.NS {
	case SVG:
		return n.Name != "foreignobject" && n.Name != "desc" && n.Name != "title"
	case MathML:
		switch n.Name {
		case "mi", "mo", "mn", "ms", "mtext":
			return false
		case "annotation-xml":
			enc, _ := n.Attr("encoding")
			enc = strings.ToLower(enc)
			return enc != "text/html" && enc != "application/xhtml+xml"
		}
		return true
	}
	return false
}

func (b *builder) foreignStart(t *lexer.Token) {
	breakout := breakoutTags[t.Name]
	if t.Name == "font" {
		for _, a := range t.Attrs {
			if a.Name == "color" || a.Name == "face" || a.Name == "size" {
				breakout = true
			}
		}
	}
	if breakout {
		for len(b.stack) > 1 && b.inForeign() {
			b.popTo(len(b.stack) - 1)
		}
		b.startTag(t)
		return
	}
	ns := b.top().node.NS
	if t.Name == "svg" {
		ns = SVG
	} else if t.Name == "math" {
		ns = MathML
	}
	b.insert(t, ns, t.SelfClosing)
}

func (b *builder) foreignEnd(t *lexer.Token) {
	for i := len(b.stack) - 1; i > 0 && i >= len(b.stack)-walkLimit; i-- {
		n := b.stack[i].node
		if n.NS == HTML {
			b.endTag(t)
			return
		}
		if n.Name == t.Name {
			b.popEnd(i, t)
			return
		}
	}
}

func isTableContext(n *Node) bool {
	if n.NS != HTML {
		return false
	}
	switch n.Name {
	case "table", "tbody", "thead", "tfoot", "tr":
		return true
	}
	return false
}

// tableContent reports whether a start tag belongs inside a table rather
// than being fostered out of it.
func tableContent(t *lexer.Token) bool {
	switch t.Name {
	case "caption", "colgroup", "col", "tbody", "thead", "tfoot", "tr", "td", "th",
		"table", "style", "script", "template", "form":
		return true
	case "input":
		v, _ := t.Attr("type")
		return strings.EqualFold(strings.TrimSpace(v), "hidden")
	}
	return false
}

func isSpecial(n *Node) bool {
	if n.NS != HTML {
		return isBoundary(n, nil)
	}
	return special[n.Name]
}

func allSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\f', '\r':
		default:
			return false
		}
	}
	return true
}

func equalAttrs(a, b []lexer.Attr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var (
	defaultScope  = set("applet", "caption", "html", "table", "td", "th", "marquee", "object", "template")
	listItemScope = set("applet", "caption", "html", "table", "td", "th", "marquee", "object", "template", "ol", "ul")
	buttonScope   = set("applet", "caption", "html", "table", "td", "th", "marquee", "object", "template", "button")
	tableScope    = set("html", "table", "template")

	pushesMarker = set("applet", "marquee", "object", "template", "td", "th", "caption")

	headings = set("h1", "h2", "h3", "h4", "h5", "h6")

	closesP = set("address", "article", "aside", "blockquote", "center", "details", "dialog",
		"dir", "div", "dl", "fieldset", "figcaption", "figure", "footer", "header", "hgroup",
		"main", "menu", "nav", "ol", "p", "search", "section", "summary", "ul",
		"h1", "h2", "h3", "h4", "h5", "h6", "pre", "listing", "form", "plaintext", "xmp",
		"table", "hr")

	formatting = set("b", "big", "code", "em", "font", "i", "nobr", "s", "small", "strike",
		"strong", "tt", "u")

	tableParts = set("caption", "colgroup", "col", "tbody", "thead", "tfoot", "tr", "td", "th")

	void = set("area", "base", "basefont", "bgsound", "br", "col", "embed", "frame", "img",
		"input", "keygen", "link", "meta", "param", "source", "track", "wbr")

	headContent = set("base", "basefont", "bgsound", "link", "meta", "title", "noscript",
		"noframes", "style", "script", "template", "head")

	special = set("address", "applet", "area", "article", "aside", "base", "basefont",
		"bgsound", "blockquote", "body", "br", "button", "caption", "center", "col", "colgroup",
		"dd", "details", "dir", "div", "dl", "dt", "embed", "fieldset", "figcaption", "figure",
		"footer", "form", "frame", "frameset", "h1", "h2", "h3", "h4", "h5", "h6", "head",
		"header", "hgroup", "hr", "html", "iframe", "img", "input", "keygen", "li", "link",
		"listing", "main", "marquee", "menu", "meta", "nav", "noembed", "noframes", "noscript",
		"object", "ol", "p", "param", "plaintext", "pre", "script", "search", "section", "select",
		"source", "style", "summary", "table", "tbody", "td", "template", "textarea", "tfoot",
		"th", "thead", "title", "tr", "track", "ul", "wbr", "xmp")

	breakoutTags = set("b", "big", "blockquote", "body", "br", "center", "code", "dd", "div",
		"dl", "dt", "em", "embed", "h1", "h2", "h3", "h4", "h5", "h6", "head", "hr", "i", "img",
		"li", "listing", "menu", "meta", "nobr", "ol", "p", "pre", "ruby", "s", "small", "span",
		"strong", "strike", "sub", "sup", "table", "tt", "u", "ul", "var")
)
