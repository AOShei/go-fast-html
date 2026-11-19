package md

import (
	"strconv"
	"strings"
)

// starts are the block starts, tried in this order: 0 no match, 1 a
// container started, 2 a leaf started.
var starts = []func(*parser, *block) int{
	startQuote, startATX, startFence, startHTML, startSetext, startBreak, startItem, startIndented,
}

func startQuote(p *parser, c *block) int {
	if p.indented || p.peek(p.nextNonspace) != '>' || c.depth >= MaxNest {
		return 0
	}
	p.advanceNextNonspace()
	p.advanceOffset(1, false)
	if ch := p.peek(p.offset); ch == ' ' || ch == '\t' {
		p.advanceOffset(1, true)
	}
	p.closeUnmatched()
	p.addChild(kQuote, p.nextNonspace)
	return 1
}

func startATX(p *parser, c *block) int {
	if p.indented {
		return 0
	}
	rest := p.line[p.nextNonspace:]
	n := run(rest, '#')
	if n < 1 || n > 6 || n < len(rest) && rest[n] != ' ' && rest[n] != '\t' {
		return 0
	}
	p.advanceNextNonspace()
	p.closeUnmatched()
	h := p.addChild(kHeading, p.nextNonspace)
	h.level = n
	s := strings.Trim(rest[n:], " \t")
	j := len(s)
	for j > 0 && s[j-1] == '#' {
		j--
	}
	if j == 0 {
		s = ""
	} else if j < len(s) && (s[j-1] == ' ' || s[j-1] == '\t') {
		s = strings.TrimRight(s[:j], " \t")
	}
	h.content = s
	p.offset = len(p.line)
	return 2
}

func startFence(p *parser, c *block) int {
	if p.indented {
		return 0
	}
	rest := p.line[p.nextNonspace:]
	if len(rest) < 3 || rest[0] != '`' && rest[0] != '~' {
		return 0
	}
	n := run(rest, rest[0])
	if n < 3 || rest[0] == '`' && strings.IndexByte(rest[n:], '`') >= 0 {
		return 0
	}
	p.closeUnmatched()
	b := p.addChild(kCode, p.nextNonspace)
	b.fenced, b.fence, b.fenceLen, b.fenceOff = true, rest[0], n, p.indent
	p.advanceNextNonspace()
	p.advanceOffset(n, false)
	return 2
}

// Type 1 and type 6 tag names of the HTML block start conditions.
var (
	rawTags   = []string{"pre", "script", "style", "textarea"}
	blockTags = set("address article aside base basefont blockquote body caption center col colgroup dd " +
		"details dialog dir div dl dt fieldset figcaption figure footer form frame frameset h1 h2 h3 h4 " +
		"h5 h6 head header hr html iframe legend li link main menu menuitem nav noframes ol optgroup " +
		"option p param search section summary table tbody td tfoot th thead title tr track ul")
)

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(s) {
		m[f] = true
	}
	return m
}

// htmlStart returns the type of HTML block s starts, 0 if none.
func htmlStart(s string, inPara bool) int {
	ls := strings.ToLower(s)
	for _, t := range rawTags {
		if strings.HasPrefix(ls, "<"+t) {
			r := ls[1+len(t):]
			if r == "" || r[0] == ' ' || r[0] == '\t' || r[0] == '>' {
				return 1
			}
		}
	}
	switch {
	case strings.HasPrefix(s, "<!--"):
		return 2
	case strings.HasPrefix(s, "<?"):
		return 3
	case strings.HasPrefix(s, "<![CDATA["):
		return 5
	case len(s) > 2 && s[1] == '!' && isLetter(s[2]):
		return 4
	}
	i := 1
	if i < len(s) && s[i] == '/' {
		i++
	}
	j := i
	for j < len(s) && (isAlnum(s[j]) || s[j] == '-') {
		j++
	}
	if j > i && blockTags[ls[i:j]] {
		r := s[j:]
		if r == "" || r[0] == ' ' || r[0] == '\t' || r[0] == '>' || strings.HasPrefix(r, "/>") {
			return 6
		}
	}
	if !inPara {
		if n := scanTag(s, 0); n > 0 && strings.Trim(s[n:], " \t") == "" {
			name := ls[i:j]
			if name != "script" && name != "style" && name != "pre" && name != "textarea" {
				return 7
			}
		}
	}
	return 0
}

// htmlEnds reports whether line ends an HTML block of type 1 to 5.
func htmlEnds(t int, line string) bool {
	switch t {
	case 1:
		l := strings.ToLower(line)
		for _, tag := range rawTags {
			if strings.Contains(l, "</"+tag+">") {
				return true
			}
		}
		return false
	case 2:
		return strings.Contains(line, "-->")
	case 3:
		return strings.Contains(line, "?>")
	case 4:
		return strings.Contains(line, ">")
	case 5:
		return strings.Contains(line, "]]>")
	}
	return false
}

func startHTML(p *parser, c *block) int {
	if p.indented || p.peek(p.nextNonspace) != '<' {
		return 0
	}
	lazy := !p.allClosed && !p.blank && p.tip.kind == kPara
	t := htmlStart(p.line[p.nextNonspace:], c.kind == kPara || lazy)
	if t == 0 {
		return 0
	}
	p.closeUnmatched()
	b := p.addChild(kHTML, p.offset)
	b.htmlType = t
	return 2
}

func startSetext(p *parser, c *block) int {
	if p.indented || c.kind != kPara {
		return 0
	}
	rest := strings.TrimRight(p.line[p.nextNonspace:], " \t")
	if rest == "" || rest[0] != '=' && rest[0] != '-' || run(rest, rest[0]) != len(rest) {
		return 0
	}
	p.closeUnmatched()
	c.content = p.takeRefdefs(c.cb.String())
	c.cb.Reset()
	c.cb.WriteString(c.content)
	if strings.Trim(c.content, " \t\n") == "" {
		return 0
	}
	c.kind = kHeading
	c.level = 1
	if rest[0] == '-' {
		c.level = 2
	}
	p.touch(c, p.lineStart+len(p.line))
	c.open = false
	p.tip = c.parent
	p.offset = len(p.line)
	return 2
}

func isBreak(s string) bool {
	var ch byte
	n := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case ' ', '\t':
		case '*', '-', '_':
			if ch != 0 && c != ch {
				return false
			}
			ch = c
			n++
		default:
			return false
		}
	}
	return n >= 3
}

func startBreak(p *parser, c *block) int {
	if p.indented || !isBreak(p.line[p.nextNonspace:]) {
		return 0
	}
	p.closeUnmatched()
	p.addChild(kHR, p.nextNonspace)
	p.offset = len(p.line)
	return 2
}

func startItem(p *parser, c *block) int {
	if p.indented && c.kind != kList || p.indent >= 4 || c.depth >= MaxNest {
		return 0
	}
	rest := p.line[p.nextNonspace:]
	var ordered bool
	var bullet, delim byte
	num, n := 0, 0
	switch {
	case rest != "" && (rest[0] == '*' || rest[0] == '+' || rest[0] == '-'):
		bullet, n = rest[0], 1
	default:
		for n < len(rest) && n < 9 && rest[n] >= '0' && rest[n] <= '9' {
			n++
		}
		if n == 0 || n >= len(rest) || rest[n] != '.' && rest[n] != ')' {
			return 0
		}
		num, _ = strconv.Atoi(rest[:n])
		if c.kind == kPara && num != 1 {
			return 0
		}
		ordered, delim = true, rest[n]
		n++
	}
	if n < len(rest) && rest[n] != ' ' && rest[n] != '\t' {
		return 0
	}
	if c.kind == kPara && strings.Trim(rest[n:], " \t") == "" {
		return 0
	}
	markerOffset := p.indent
	p.advanceNextNonspace()
	p.advanceOffset(n, true)
	sc, so := p.column, p.offset
	for {
		p.advanceOffset(1, true)
		if ch := p.peek(p.offset); p.column-sc >= 5 || ch != ' ' && ch != '\t' {
			break
		}
	}
	blankItem := p.peek(p.offset) == -1
	after := p.column - sc
	padding := n + after
	if after >= 5 || after < 1 || blankItem {
		padding = n + 1
		p.column, p.offset = sc, so
		if ch := p.peek(p.offset); ch == ' ' || ch == '\t' {
			p.advanceOffset(1, true)
		}
	}
	p.closeUnmatched()
	if t := p.tip; t.kind != kList || t.ordered != ordered || t.bullet != bullet || t.delim != delim {
		l := p.addChild(kList, p.nextNonspace)
		l.ordered, l.bullet, l.delim, l.startNum = ordered, bullet, delim, num
	}
	it := p.addChild(kItem, p.nextNonspace)
	it.markerOffset, it.padding = markerOffset, padding
	return 1
}

func startIndented(p *parser, c *block) int {
	if !p.indented || p.tip.kind == kPara || p.blank {
		return 0
	}
	p.advanceOffset(4, true)
	p.closeUnmatched()
	p.addChild(kCode, p.offset)
	return 2
}

// table makes a paragraph whose first line is a header row and second a
// GFM delimiter row a table.
func (p *parser) table(b *block) {
	lines := strings.Split(strings.TrimRight(b.content, "\n"), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "|") && !strings.Contains(lines[1], "|") {
		return
	}
	head, delims := cells(lines[0]), cells(lines[1])
	if len(head) != len(delims) {
		return
	}
	for _, d := range delims {
		d = strings.TrimSuffix(strings.TrimPrefix(d, ":"), ":")
		if d == "" || run(d, '-') != len(d) {
			return
		}
	}
	b.kind = kTable
	b.rows = append(b.rows, head)
	for _, l := range lines[2:] {
		row := cells(l)
		row = append(row, make([]string, max(0, len(head)-len(row)))...)
		b.rows = append(b.rows, row[:len(head)])
	}
}

// cells splits a table row at the pipes that are not escaped.
func cells(line string) []string {
	s := strings.Trim(line, " \t")
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, "\\|") {
		s = s[:len(s)-1]
	}
	var out []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '|':
			cur.WriteByte('|')
			i++
		case s[i] == '|':
			out = append(out, strings.Trim(cur.String(), " \t"))
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	return append(out, strings.Trim(cur.String(), " \t"))
}

func isLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
func isAlnum(c byte) bool  { return isLetter(c) || c >= '0' && c <= '9' }
