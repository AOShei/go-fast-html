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
}

// Render writes the document as HTML, stopping before limit bytes.
func (d *Doc) Render(limit int) *HTML {
	r := &renderer{o: &HTML{}, refs: d.refs, limit: limit}
	r.block(d.root, false)
	return r.o
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

// inline renders the inline content s, walking the tree without
// recursion, since emphasis can nest as deep as the text is long.
func (r *renderer) inline(s string) {
	root := inlines(s, r.refs)
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
		case iLink:
			r.o.B = append(r.o.B, `<a href="`...)
			r.esc(n.dest, true)
			r.o.B = append(r.o.B, '"')
			if n.title != "" {
				r.o.B = append(r.o.B, ` title="`...)
				r.esc(n.title, true)
				r.o.B = append(r.o.B, '"')
			}
			r.o.B = append(r.o.B, '>')
		case iImage:
			r.o.B = append(r.o.B, `<img src="`...)
			r.esc(n.dest, true)
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
