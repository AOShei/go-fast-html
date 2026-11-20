// Package dom builds a document tree from HTML tokens, recovering from tag
// soup the way the WHATWG HTML standard's tree construction does for the
// cases that change what text belongs where: implied end tags (p, li, dd,
// dt, option, table cells and rows), misnested formatting elements (a
// simplified adoption agency algorithm), reopening of formatting elements
// closed by a block, foster parenting of content misplaced in tables,
// foreign content (SVG, MathML) and the elements that break out of it.
//
// Every node records what the page declares about its display, following
// the element and its ancestors: the hidden attribute, a template's inert
// content, SVG display and visibility attributes, and inline style
// display, visibility, opacity:0 and a fully transparent colour. Nothing
// is judged: no colour is compared, no size or position measured.
//
// Nesting is bounded: below MaxDepth elements are attached to the
// deepest allowed node instead of nesting further, and the stack of open
// elements has a bound of its own. The declared reasons are kept across
// both bounds.
package dom

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/lexer"
)

// Type is the type of a node.
type Type uint8

const (
	ElementNode Type = iota + 1
	TextNode
	CommentNode
	DocumentNode
)

// NS is an element's namespace.
type NS uint8

const (
	HTML NS = iota
	SVG
	MathML
)

// WS is the white-space handling in effect for text.
type WS uint8

const (
	// Normal collapses runs of white space to one space.
	Normal WS = iota
	// Pre keeps all white space.
	Pre
	// PreLine collapses spaces and keeps line breaks.
	PreLine
)

// Node is one node of the tree.
type Node struct {
	Type  Type
	Name  string // lowercased element name
	NS    NS
	Attrs []lexer.Attr
	// Text is the text of a text or comment node.
	Text []byte
	// Start and End are byte offsets of the node in the decoded input,
	// from its start tag to the end of its end tag or of the last thing
	// in it.
	Start, End int
	// Display holds the reasons the page declares that the node is not
	// displayed or has no ink, following inheritance as CSS does. For an
	// element it covers the element and everything in it.
	Display gofastcore.Display
	WS      WS

	Parent, FirstChild, LastChild, Prev, Next *Node

	depth int32
	open  bool
}

// Depth returns how deeply the node is nested in its tree.
func (n *Node) Depth() int { return int(n.depth) }

// Attr returns the value of the named attribute and whether it is
// present.
func (n *Node) Attr(name string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// insertBefore attaches c as a child of n before ref, or last if ref is
// nil.
func (n *Node) insertBefore(c, ref *Node) {
	c.Parent = n
	c.depth = n.depth + 1
	if ref == nil {
		c.Prev = n.LastChild
		if n.LastChild != nil {
			n.LastChild.Next = c
		} else {
			n.FirstChild = c
		}
		n.LastChild = c
		return
	}
	c.Next = ref
	c.Prev = ref.Prev
	if ref.Prev != nil {
		ref.Prev.Next = c
	} else {
		n.FirstChild = c
	}
	ref.Prev = c
}
