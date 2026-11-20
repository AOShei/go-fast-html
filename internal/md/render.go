package md

import (
	"sort"
	"strconv"
	"strings"
)

// seg maps a stretch [h, hEnd) of the HTML to the Markdown. A linear
// stretch is Markdown copied as it is (raw HTML); any other maps to m,
// and its end to mEnd.
type seg struct {
	h, hEnd, m, mEnd int
	linear           bool
}

// HTML is a document rendered to HTML.
type HTML struct {
	B    []byte
	segs []seg
	// Cut reports that rendering stopped at the size limit.
	Cut bool
}

// Source returns the offset in the Markdown of offset h in the HTML.
func (o *HTML) Source(h int) int {
	i := sort.Search(len(o.segs), func(i int) bool { return o.segs[i].h > h }) - 1
	if i < 0 {
		return 0
	}
	s := o.segs[i]
	if h < s.hEnd {
		if s.linear {
			return s.m + h - s.h
		}
		return s.m
	}
	return s.mEnd
}

type renderer struct {
	o     *HTML
	refs  map[string]Ref
	limit int
	spec  bool
	gfm   bool
}

// Render writes the document as HTML, stopping before limit bytes. Link
// and image addresses are written as the source has them.
func (d *Doc) Render(limit int) *HTML {
	r := &renderer{o: &HTML{}, refs: d.refs, limit: limit, gfm: d.GFM}
	r.block(d.root, false)
	return r.o
}

// RenderSpec writes the document as the CommonMark reference renderer
// does, with addresses percent-encoded, for comparison with the spec.
func (d *Doc) RenderSpec() *HTML {
	r := &renderer{o: &HTML{}, refs: d.refs, limit: 1 << 30, spec: true}
	r.block(d.root, false)
	return r.o
}

// encodeURI is the reference renderer's normalizeURI (mdurl.encode):
// characters outside the URL-safe set are percent-encoded as UTF-8, and
// a % that starts a valid escape is kept.
func encodeURI(s string) string {
	const safe = ";/?:@&=+$,-_.!~*'()#"
	hex := func(c byte) bool { return strings.IndexByte("0123456789abcdefABCDEF", c) >= 0 }
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '%' && i+2 < len(s) && hex(s[i+1]) && hex(s[i+2]):
			b.WriteByte(c)
		case c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(safe, c) >= 0):
			b.WriteByte(c)
		default:
			b.WriteString("%" + strings.ToUpper(strconv.FormatUint(uint64(c)|0x100, 16)[1:]))
		}
	}
	return b.String()
}

func (r *renderer) dest(s string) string {
	if r.spec {
		return encodeURI(s)
	}
	return s
}

func (r *renderer) full() bool {
	if len(r.o.B) >= r.limit {
		r.o.Cut = true
		return true
	}
	return false
}

// gen writes generated markup standing for the Markdown at [m, mEnd).
func (r *renderer) gen(s string, m, mEnd int) {
	h := len(r.o.B)
	r.o.B = append(r.o.B, s...)
	r.o.segs = append(r.o.segs, seg{h, len(r.o.B), m, mEnd, false})
	r.o.B = append(r.o.B, '\n')
}

// span records that what was written since h stands for [m, mEnd).
func (r *renderer) span(h, m, mEnd int) {
	r.o.segs = append(r.o.segs, seg{h, len(r.o.B), m, mEnd, false})
	r.o.B = append(r.o.B, '\n')
}

func (r *renderer) block(b *block, tight bool) {
	if r.full() {
		return
	}
	switch b.kind {
	case kDoc:
		for _, c := range b.children {
			r.block(c, false)
		}
	case kQuote:
		r.gen("<blockquote>", b.start, b.start)
		for _, c := range b.children {
			r.block(c, false)
		}
		r.gen("</blockquote>", b.end, b.end)
	case kList:
		tag := "ul"
		open := "<ul>"
		if b.ordered {
			tag, open = "ol", "<ol>"
			if b.startNum != 1 {
				open = `<ol start="` + strconv.Itoa(b.startNum) + `">`
			}
		}
		r.gen(open, b.start, b.start)
		for _, it := range b.children {
			r.gen("<li>", it.start, it.start)
			if r.gfm && len(it.children) > 0 && it.children[0].kind == kPara {
				taskItem(it.children[0])
			}
			for _, c := range it.children {
				r.block(c, b.tight)
			}
			r.gen("</li>", it.end, it.end)
		}
		r.gen("</"+tag+">", b.end, b.end)
	case kPara:
		h := len(r.o.B)
		if !tight {
			r.o.B = append(r.o.B, "<p>"...)
		}
		switch b.task {
		case 1:
			r.o.B = append(r.o.B, `<input type="checkbox" disabled="" /> `...)
		case 2:
			r.o.B = append(r.o.B, `<input type="checkbox" checked="" disabled="" /> `...)
		}
		r.inline(strings.Trim(b.content, " \t\n"))
		if !tight {
			r.o.B = append(r.o.B, "</p>"...)
		}
		r.span(h, b.start, b.end)
	case kHeading:
		h := len(r.o.B)
		n := strconv.Itoa(b.level)
		r.o.B = append(r.o.B, "<h"+n+">"...)
		r.inline(strings.Trim(b.content, " \t\n"))
		r.o.B = append(r.o.B, "</h"+n+">"...)
		r.span(h, b.start, b.end)
	case kCode:
		h := len(r.o.B)
		r.o.B = append(r.o.B, "<pre><code"...)
		if lang, _, _ := strings.Cut(b.info, " "); lang != "" {
			r.o.B = append(r.o.B, ` class="language-`...)
			r.esc(lang, true)
			r.o.B = append(r.o.B, '"')
		}
		r.o.B = append(r.o.B, '>')
		r.esc(b.content, false)
		r.o.B = append(r.o.B, "</code></pre>"...)
		r.span(h, b.start, b.end)
	case kHTML:
		for _, l := range b.lines {
			h := len(r.o.B)
			r.o.B = append(r.o.B, l.text...)
			r.o.segs = append(r.o.segs, seg{h, len(r.o.B), l.off, l.off + len(l.text), true})
			r.o.B = append(r.o.B, '\n')
		}
	case kHR:
		r.gen("<hr />", b.start, b.end)
	case kTable:
		h := len(r.o.B)
		r.o.B = append(r.o.B, "<table><thead><tr>"...)
		for i, row := range b.rows {
			if i == 1 {
				r.o.B = append(r.o.B, "</tr></thead><tbody><tr>"...)
			} else if i > 1 {
				r.o.B = append(r.o.B, "</tr><tr>"...)
			}
			cell := "td"
			if i == 0 {
				cell = "th"
			}
			for _, c := range row {
				r.o.B = append(r.o.B, "<"+cell+">"...)
				r.inline(c)
				r.o.B = append(r.o.B, "</"+cell+">"...)
			}
			if r.spec {
				for range len(b.rows[0]) - len(row) {
					r.o.B = append(r.o.B, "<td></td>"...)
				}
			}
			if r.full() {
				break
			}
		}
		if len(b.rows) > 1 {
			r.o.B = append(r.o.B, "</tr></tbody></table>"...)
		} else {
			r.o.B = append(r.o.B, "</tr></thead></table>"...)
		}
		r.span(h, b.start, b.end)
	}
}

// taskItem makes the first paragraph of a list item that begins with
// "[ ]", "[x]" or "[X]" and white space a GFM task-list item: the marker
// becomes a checkbox.
func taskItem(p *block) {
	c := strings.TrimLeft(p.content, " \t")
	if len(c) < 4 || c[0] != '[' || c[2] != ']' || c[3] != ' ' && c[3] != '\t' && c[3] != '\n' {
		return
	}
	switch c[1] {
	case ' ':
		p.task = 1
	case 'x', 'X':
		p.task = 2
	default:
		return
	}
	p.content = c[4:]
}

// inline renders the inline content s, walking the tree without
// recursion, since emphasis can nest as deep as the text is long.
func (r *renderer) inline(s string) {
	root := inlines(s, r.refs, r.gfm)
	n := root.first
	for n != nil {
		switch n.kind {
		case iText:
			r.esc(n.text, false)
		case iSoft:
			r.o.B = append(r.o.B, '\n')
		case iHard:
			r.o.B = append(r.o.B, "<br />\n"...)
		case iCode:
			r.o.B = append(r.o.B, "<code>"...)
			r.esc(n.text, false)
			r.o.B = append(r.o.B, "</code>"...)
		case iHTML:
			r.o.B = append(r.o.B, n.text...)
		case iEmph:
			r.o.B = append(r.o.B, "<em>"...)
		case iStrong:
			r.o.B = append(r.o.B, "<strong>"...)
		case iStrike:
			r.o.B = append(r.o.B, "<del>"...)
		case iLink:
			r.o.B = append(r.o.B, `<a href="`...)
			r.esc(r.dest(n.dest), true)
			r.o.B = append(r.o.B, '"')
			if n.title != "" {
				r.o.B = append(r.o.B, ` title="`...)
				r.esc(n.title, true)
				r.o.B = append(r.o.B, '"')
			}
			r.o.B = append(r.o.B, '>')
		case iImage:
			r.o.B = append(r.o.B, `<img src="`...)
			r.esc(r.dest(n.dest), true)
			r.o.B = append(r.o.B, `" alt="`...)
			r.esc(plain(n), true)
			r.o.B = append(r.o.B, '"')
			if n.title != "" {
				r.o.B = append(r.o.B, ` title="`...)
				r.esc(n.title, true)
				r.o.B = append(r.o.B, '"')
			}
			r.o.B = append(r.o.B, " />"...)
		}
		// Down into the children (not of an image, whose text is its
		// alt), else on, closing what ends.
		if n.first != nil && n.kind != iImage {
			n = n.first
			continue
		}
		r.close(n) // an element with nothing in it
		for n != nil && n.next == nil {
			n = n.parent
			if n == nil || n == root {
				n = nil
				break
			}
			r.close(n)
		}
		if n != nil {
			n = n.next
		}
	}
}

func (r *renderer) close(n *inode) {
	switch n.kind {
	case iEmph:
		r.o.B = append(r.o.B, "</em>"...)
	case iStrong:
		r.o.B = append(r.o.B, "</strong>"...)
	case iStrike:
		r.o.B = append(r.o.B, "</del>"...)
	case iLink:
		r.o.B = append(r.o.B, "</a>"...)
	}
}

// plain returns the text under n, as image alt text.
func plain(root *inode) string {
	var b strings.Builder
	n := root.first
	for n != nil {
		switch n.kind {
		case iText, iCode:
			b.WriteString(n.text)
		case iSoft, iHard:
			b.WriteByte(' ')
		}
		if n.first != nil {
			n = n.first
			continue
		}
		for n != nil && n != root && n.next == nil {
			n = n.parent
		}
		if n == nil || n == root {
			break
		}
		n = n.next
	}
	return b.String()
}

// esc writes s escaped for HTML text or an attribute value. NUL is
// U+FFFD, as CommonMark has it.
func (r *renderer) esc(s string, attr bool) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&':
			r.o.B = append(r.o.B, "&amp;"...)
		case '<':
			r.o.B = append(r.o.B, "&lt;"...)
		case '>':
			r.o.B = append(r.o.B, "&gt;"...)
		case '"':
			if attr {
				r.o.B = append(r.o.B, "&quot;"...)
			} else {
				r.o.B = append(r.o.B, c)
			}
		case 0:
			r.o.B = append(r.o.B, "\xEF\xBF\xBD"...)
		default:
			r.o.B = append(r.o.B, c)
		}
	}
}
