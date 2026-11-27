package md

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core/entity"
)

type ikind uint8

const (
	iRoot ikind = iota
	iText
	iSoft
	iHard
	iCode
	iHTML
	iEmph
	iStrong
	iLink
	iImage
	iStrike
)

type inode struct {
	kind                            ikind
	text, dest, title               string
	parent, first, last, prev, next *inode
}

func (n *inode) append(c *inode) {
	c.parent, c.prev, c.next = n, n.last, nil
	if n.last != nil {
		n.last.next = c
	} else {
		n.first = c
	}
	n.last = c
}

func (n *inode) unlink() {
	if n.prev != nil {
		n.prev.next = n.next
	} else if n.parent != nil {
		n.parent.first = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else if n.parent != nil {
		n.parent.last = n.prev
	}
	n.parent, n.prev, n.next = nil, nil, nil
}

func (n *inode) insertAfter(c *inode) {
	c.parent, c.prev, c.next = n.parent, n, n.next
	if n.next != nil {
		n.next.prev = c
	} else if n.parent != nil {
		n.parent.last = c
	}
	n.next = c
}

type delim struct {
	node              *inode
	ch                byte
	num, orig         int
	canOpen, canClose bool
	prev, next        *delim
}

type bracket struct {
	node                        *inode
	prev                        *bracket
	prevDelim                   *delim
	pos                         int
	image, active, bracketAfter bool
}

type inliner struct {
	s        string
	pos      int
	root     *inode
	delims   *delim
	brackets *bracket
	refs     map[string]Ref
	// Scans that found no end once find none later either.
	noTick                           map[int]bool
	noComment, noPI, noCDATA, noDecl bool
	gfm                              bool
	// replaced counts character references read as U+FFFD.
	replaced *int
}

var special [256]bool

func init() {
	for _, c := range "\n\\`*_[]!<&~" {
		special[c] = true
	}
}

// inlines parses the inline content s.
// replaced counts the character references read as U+FFFD.
func inlines(s string, refs map[string]Ref, gfm bool, replaced *int) *inode {
	p := &inliner{s: s, root: &inode{kind: iRoot}, refs: refs, gfm: gfm, replaced: replaced}
	for p.pos < len(s) {
		p.step()
	}
	p.emphasis(nil)
	return p.root
}

func (p *inliner) text(t string) *inode {
	n := &inode{kind: iText, text: t}
	p.root.append(n)
	return n
}

func (p *inliner) step() {
	s := p.s
	switch c := s[p.pos]; c {
	case '\n':
		p.pos++
		kind := iSoft
		if l := p.root.last; l != nil && l.kind == iText && strings.HasSuffix(l.text, " ") {
			t := strings.TrimRight(l.text, " ")
			if len(l.text)-len(t) >= 2 {
				kind = iHard
			}
			l.text = t
		}
		p.root.append(&inode{kind: kind})
		for p.pos < len(s) && s[p.pos] == ' ' {
			p.pos++
		}
	case '\\':
		p.pos++
		switch {
		case p.pos < len(s) && s[p.pos] == '\n':
			p.pos++
			p.root.append(&inode{kind: iHard})
			for p.pos < len(s) && s[p.pos] == ' ' {
				p.pos++
			}
		case p.pos < len(s) && isPunctASCII(s[p.pos]):
			p.text(s[p.pos : p.pos+1])
			p.pos++
		default:
			p.text("\\")
		}
	case '`':
		p.codeSpan()
	case '*', '_':
		p.delimRun(c)
	case '~':
		if p.gfm {
			p.delimRun(c)
		} else {
			p.text("~")
			p.pos++
		}
	case '[':
		p.pos++
		p.pushBracket(p.text("["), false)
	case '!':
		if p.pos+1 < len(s) && s[p.pos+1] == '[' {
			p.pos += 2
			p.pushBracket(p.text("!["), true)
		} else {
			p.pos++
			p.text("!")
		}
	case ']':
		p.closeBracket()
	case '<':
		p.angle()
	case '&':
		if r, n, bad := entityAt(s, p.pos); n > 0 {
			if bad {
				*p.replaced++
			}
			p.text(r)
			p.pos += n
		} else {
			p.text("&")
			p.pos++
		}
	default:
		j := p.pos + 1
		for j < len(s) && !special[s[j]] {
			j++
		}
		if p.gfm {
			if k, end, dest := extAutolink(s, p.pos, j); k >= 0 {
				if k > p.pos {
					p.text(s[p.pos:k])
				}
				l := &inode{kind: iLink, dest: dest}
				l.append(&inode{kind: iText, text: s[k:end]})
				p.root.append(l)
				p.pos = end
				return
			}
		}
		p.text(s[p.pos:j])
		p.pos = j
	}
}

// extAutolink finds the first GFM extended autolink (www., http://,
// https://) that starts in s[from:to] where a link may start: at the
// start of the text, after white space, or after one of *_~(. It returns
// where it starts and ends and its address, or k = -1.
func extAutolink(s string, from, to int) (k, end int, dest string) {
	for i := from; i < to; i++ {
		c := s[i]
		if c != 'w' && c != 'h' && c != 'W' && c != 'H' {
			continue
		}
		if i > 0 && !strings.ContainsRune(" \t\n*_~(", rune(s[i-1])) {
			continue
		}
		rest := s[i:]
		prefix := ""
		switch {
		case hasPrefixFold(rest, "www."):
			prefix = "http://"
		case hasPrefixFold(rest, "http://"), hasPrefixFold(rest, "https://"):
		default:
			continue
		}
		start := i
		if prefix == "" {
			start = i + strings.IndexByte(rest, ':') + 3
		}
		// The domain: segments of letters, digits, _ and -, separated
		// by periods; no _ in the last two.
		j := start
		segs, lastUnderscore, prevUnderscore := 0, false, false
		for j < len(s) {
			e := j
			under := false
			for e < len(s) && (isAlnum(s[e]) || s[e] == '-' || s[e] == '_' || s[e] >= 0x80) {
				under = under || s[e] == '_'
				e++
			}
			if e == j {
				break
			}
			segs++
			prevUnderscore, lastUnderscore = lastUnderscore, under
			j = e
			if j+1 < len(s) && s[j] == '.' && (isAlnum(s[j+1]) || s[j+1] == '-' || s[j+1] == '_' || s[j+1] >= 0x80) {
				j++
				continue
			}
			break
		}
		if segs < 1 || lastUnderscore || prevUnderscore || prefix != "" && segs < 2 {
			continue
		}
		// The path: up to white space or '<'.
		for j < len(s) && s[j] != '<' && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' {
			j++
		}
		j = trimAutolink(s, start, j)
		return i, j, prefix + s[i:j]
	}
	return -1, 0, ""
}

func hasPrefixFold(s, p string) bool {
	return len(s) >= len(p) && strings.EqualFold(s[:len(p)], p)
}

// trimAutolink drops what GFM says does not end an extended autolink:
// trailing punctuation, an unbalanced closing parenthesis, and what looks
// like an entity reference.
func trimAutolink(s string, start, j int) int {
	for j > start {
		c := s[j-1]
		switch {
		case strings.IndexByte("?!.,:*_~'\"", c) >= 0:
			j--
		case c == ')':
			open := strings.Count(s[start:j], "(")
			if strings.Count(s[start:j], ")") > open {
				j--
				continue
			}
			return j
		case c == ';':
			k := j - 2
			for k > start && isAlnum(s[k]) {
				k--
			}
			if k >= start && s[k] == '&' && k < j-2 {
				j = k
				continue
			}
			return j
		default:
			return j
		}
	}
	return j
}

func (p *inliner) codeSpan() {
	s := p.s
	start := p.pos
	n := run(s[start:], '`')
	open := start + n
	if !p.noTick[n] {
		for i := open; i < len(s); {
			j := strings.IndexByte(s[i:], '`')
			if j < 0 {
				break
			}
			j += i
			m := run(s[j:], '`')
			if m == n {
				c := strings.ReplaceAll(s[open:j], "\n", " ")
				if len(c) >= 2 && c[0] == ' ' && c[len(c)-1] == ' ' && strings.Trim(c, " ") != "" {
					c = c[1 : len(c)-1]
				}
				p.root.append(&inode{kind: iCode, text: c})
				p.pos = j + m
				return
			}
			i = j + m
		}
		if p.noTick == nil {
			p.noTick = map[int]bool{}
		}
		p.noTick[n] = true
	}
	p.text(s[start:open])
	p.pos = open
}

func (p *inliner) delimRun(ch byte) {
	s := p.s
	start := p.pos
	n := run(s[start:], ch)
	p.pos += n
	before, after := '\n', '\n'
	if start > 0 {
		before, _ = utf8.DecodeLastRuneInString(s[:start])
	}
	if p.pos < len(s) {
		after, _ = utf8.DecodeRuneInString(s[p.pos:])
	}
	bw, aw := isSpace(before), isSpace(after)
	bp, ap := isPunct(before), isPunct(after)
	left := !aw && (!ap || bw || bp)
	right := !bw && (!bp || aw || ap)
	canOpen, canClose := left, right
	if ch == '_' {
		canOpen = left && (!right || bp)
		canClose = right && (!left || ap)
	}
	node := p.text(s[start:p.pos])
	if (canOpen || canClose) && (ch != '~' || n <= 2) {
		d := &delim{node: node, ch: ch, num: n, orig: n, canOpen: canOpen, canClose: canClose, prev: p.delims}
		if p.delims != nil {
			p.delims.next = d
		}
		p.delims = d
	}
}

func (p *inliner) removeDelim(d *delim) {
	if d.prev != nil {
		d.prev.next = d.next
	}
	if d.next != nil {
		d.next.prev = d.prev
	} else {
		p.delims = d.prev
	}
}

// emphasis is CommonMark's "process emphasis" over the delimiters above
// bottom.
func (p *inliner) emphasis(bottom *delim) {
	type key struct {
		ch      byte
		canOpen bool
		mod     int
	}
	openersBottom := map[key]*delim{}
	closer := p.delims
	for closer != nil && closer.prev != bottom {
		closer = closer.prev
	}
	for closer != nil {
		if !closer.canClose {
			closer = closer.next
			continue
		}
		k := key{closer.ch, closer.canOpen, closer.orig % 3}
		ob, ok := openersBottom[k]
		if !ok {
			ob = bottom
		}
		opener := closer.prev
		found := false
		for opener != nil && opener != bottom && opener != ob {
			odd := (closer.canOpen || opener.canClose) && closer.orig%3 != 0 && (opener.orig+closer.orig)%3 == 0
			if closer.ch == '~' {
				// Strikethrough: runs of the same length, one or two.
				odd = opener.num != closer.num
			}
			if opener.ch == closer.ch && opener.canOpen && !odd {
				found = true
				break
			}
			opener = opener.prev
		}
		old := closer
		if found {
			use := 1
			if closer.num >= 2 && opener.num >= 2 {
				use = 2
			}
			if closer.ch == '~' {
				use = closer.num
			}
			on, cn := opener.node, closer.node
			opener.num -= use
			closer.num -= use
			on.text = on.text[:len(on.text)-use]
			cn.text = cn.text[:len(cn.text)-use]
			em := &inode{kind: iEmph}
			switch {
			case closer.ch == '~':
				em.kind = iStrike
			case use == 2:
				em.kind = iStrong
			}
			for tmp := on.next; tmp != nil && tmp != cn; {
				nx := tmp.next
				tmp.unlink()
				em.append(tmp)
				tmp = nx
			}
			on.insertAfter(em)
			opener.next, closer.prev = closer, opener
			if opener.num == 0 {
				on.unlink()
				p.removeDelim(opener)
			}
			if closer.num == 0 {
				cn.unlink()
				nx := closer.next
				p.removeDelim(closer)
				closer = nx
			}
		} else {
			closer = closer.next
			openersBottom[k] = old.prev
			if !old.canOpen {
				p.removeDelim(old)
			}
		}
	}
	for p.delims != nil && p.delims != bottom {
		p.removeDelim(p.delims)
	}
}

func (p *inliner) pushBracket(n *inode, image bool) {
	if p.brackets != nil {
		p.brackets.bracketAfter = true
	}
	p.brackets = &bracket{node: n, prev: p.brackets, prevDelim: p.delims, pos: p.pos, image: image, active: true}
}

func (p *inliner) closeBracket() {
	s := p.s
	startpos := p.pos
	p.pos++
	opener := p.brackets
	if opener == nil {
		p.text("]")
		return
	}
	if !opener.active {
		p.text("]")
		p.brackets = opener.prev
		return
	}
	save := p.pos
	matched := false
	var dest, title string
	if p.pos < len(s) && s[p.pos] == '(' {
		i := spnl(s, p.pos+1)
		if d, j, ok := linkDest(s, i, true); ok {
			dest = d
			from, to := i, j // what the address and title are read from
			i = j
			if k := spnl(s, i); k != i {
				if t, m, ok := linkTitle(s, k); ok {
					title = t
					i, to = m, m
				}
			}
			i = spnl(s, i)
			if i < len(s) && s[i] == ')' {
				p.pos = i + 1
				matched = true
				*p.replaced += replacedIn(s[from:to])
			}
		}
	}
	if !matched {
		label := ""
		n := linkLabel(s, p.pos)
		switch {
		case n > 2:
			label = s[p.pos : p.pos+n]
			label = label[1 : len(label)-1]
			p.pos += n
		case !opener.bracketAfter:
			label = s[opener.pos:startpos]
			if n == 2 {
				p.pos += n
			}
		}
		if label != "" && len(label) <= 999 {
			if r, ok := p.refs[normLabel(label)]; ok {
				dest, title, matched = r.Dest, r.Title, true
			}
		}
		if !matched {
			p.pos = save
		}
	}
	if !matched {
		p.brackets = opener.prev
		p.text("]")
		return
	}
	node := &inode{kind: iLink, dest: dest, title: title}
	if opener.image {
		node.kind = iImage
	}
	for tmp := opener.node.next; tmp != nil; {
		nx := tmp.next
		tmp.unlink()
		node.append(tmp)
		tmp = nx
	}
	p.root.append(node)
	p.emphasis(opener.prevDelim)
	p.brackets = opener.prev
	opener.node.unlink()
	if !opener.image {
		for b := p.brackets; b != nil; b = b.prev {
			if !b.image {
				b.active = false
			}
		}
	}
}

func (p *inliner) angle() {
	s, i := p.s, p.pos
	if n := autolink(s, i); n > 0 {
		addr := s[i+1 : i+n-1]
		dest := addr
		if strings.IndexByte(addr, ':') < 0 {
			dest = "mailto:" + addr
		}
		l := &inode{kind: iLink, dest: dest}
		l.append(&inode{kind: iText, text: addr})
		p.root.append(l)
		p.pos += n
		return
	}
	if n := p.rawHTML(s, i); n > 0 {
		p.root.append(&inode{kind: iHTML, text: s[i : i+n]})
		p.pos += n
		return
	}
	p.text("<")
	p.pos++
}

// autolink returns the length of a URI or email autolink at s[i].
func autolink(s string, i int) int {
	j := i + 1
	k := j
	for k < len(s) && k-j <= 32 && (isAlnum(s[k]) || k > j && strings.IndexByte("+.-", s[k]) >= 0) {
		k++
	}
	if k-j >= 2 && k-j <= 32 && isLetter(s[j]) && k < len(s) && s[k] == ':' {
		for k++; k < len(s); k++ {
			c := s[k]
			if c == '>' {
				return k + 1 - i
			}
			if c <= ' ' || c == '<' || c == 0x7F {
				return 0
			}
		}
		return 0
	}
	at := -1
	for k = j; k < len(s); k++ {
		c := s[k]
		switch {
		case c == '@' && at < 0 && k > j:
			at = k
		case c == '>':
			if at > 0 && k > at+1 && s[k-1] != '.' && s[k-1] != '-' {
				return k + 1 - i
			}
			return 0
		case at < 0 && (isAlnum(c) || strings.IndexByte(".!#$%&'*+/=?^_`{|}~-", c) >= 0):
		case at > 0 && (isAlnum(c) || c == '.' || c == '-'):
		default:
			return 0
		}
	}
	return 0
}

// rawHTML returns the length of inline raw HTML at s[i].
func (p *inliner) rawHTML(s string, i int) int {
	r := s[i:]
	find := func(open int, close string, none *bool) int {
		if *none {
			return 0
		}
		e := strings.Index(r[open:], close)
		if e < 0 {
			*none = true
			return 0
		}
		return open + e + len(close)
	}
	switch {
	case strings.HasPrefix(r, "<!-->"):
		return 5
	case strings.HasPrefix(r, "<!--->"):
		return 6
	case strings.HasPrefix(r, "<!--"):
		return find(4, "-->", &p.noComment)
	case strings.HasPrefix(r, "<?"):
		return find(2, "?>", &p.noPI)
	case strings.HasPrefix(r, "<![CDATA["):
		return find(9, "]]>", &p.noCDATA)
	case len(r) > 2 && r[1] == '!' && isLetter(r[2]):
		return find(2, ">", &p.noDecl)
	}
	return scanTag(s, i)
}

// scanTag returns the length of an open or closing tag at s[i].
func scanTag(s string, i int) int {
	j := i + 1
	if j < len(s) && s[j] == '/' {
		j++
		if j >= len(s) || !isLetter(s[j]) {
			return 0
		}
		for j < len(s) && (isAlnum(s[j]) || s[j] == '-') {
			j++
		}
		j = skipWS(s, j)
		if j < len(s) && s[j] == '>' {
			return j + 1 - i
		}
		return 0
	}
	if j >= len(s) || !isLetter(s[j]) {
		return 0
	}
	for j < len(s) && (isAlnum(s[j]) || s[j] == '-') {
		j++
	}
	for {
		k := skipWS(s, j)
		if k < len(s) && s[k] == '/' {
			if k+1 < len(s) && s[k+1] == '>' {
				return k + 2 - i
			}
			return 0
		}
		if k < len(s) && s[k] == '>' {
			return k + 1 - i
		}
		if k == j || k >= len(s) || !(isLetter(s[k]) || s[k] == '_' || s[k] == ':') {
			return 0
		}
		j = k + 1
		for j < len(s) && (isAlnum(s[j]) || strings.IndexByte("_.:-", s[j]) >= 0) {
			j++
		}
		m := skipWS(s, j)
		if m < len(s) && s[m] == '=' {
			m = skipWS(s, m+1)
			if m >= len(s) {
				return 0
			}
			if q := s[m]; q == '"' || q == '\'' {
				e := strings.IndexByte(s[m+1:], q)
				if e < 0 {
					return 0
				}
				j = m + 1 + e + 1
			} else {
				e := m
				for e < len(s) && strings.IndexByte(" \t\n\r\f\"'=<>`", s[e]) < 0 {
					e++
				}
				if e == m {
					return 0
				}
				j = e
			}
		}
	}
}

func skipWS(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	return i
}

// linkLabel returns the length of a link label at s[i], brackets
// included, or 0.
func linkLabel(s string, i int) int {
	if i >= len(s) || s[i] != '[' {
		return 0
	}
	for j := i + 1; j < len(s) && j-i <= 1001; j++ {
		switch s[j] {
		case '\\':
			j++
		case '[':
			return 0
		case ']':
			return j + 1 - i
		}
	}
	return 0
}

// linkDest reads a link destination at s[i]. An empty one is allowed only
// before ')' in an inline link.
func linkDest(s string, i int, inline bool) (string, int, bool) {
	if i < len(s) && s[i] == '<' {
		for j := i + 1; j < len(s); j++ {
			switch s[j] {
			case '\\':
				if j+1 < len(s) && isPunctASCII(s[j+1]) {
					j++
				}
			case '>':
				return unescape(s[i+1 : j]), j + 1, true
			case '<', '\n':
				return "", i, false
			}
		}
		return "", i, false
	}
	depth, j := 0, i
loop:
	for j < len(s) {
		switch c := s[j]; {
		case c == '\\' && j+1 < len(s) && isPunctASCII(s[j+1]):
			j++
		case c == '(':
			if depth++; depth > 32 {
				return "", i, false
			}
		case c == ')':
			if depth == 0 {
				break loop
			}
			depth--
		case c <= ' ' || c == 0x7F:
			break loop
		}
		j++
	}
	if depth != 0 || j == i && !(inline && j < len(s) && s[j] == ')') {
		return "", i, false
	}
	return unescape(s[i:j]), j, true
}

func linkTitle(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", i, false
	}
	open := s[i]
	var close byte
	switch open {
	case '"', '\'':
		close = open
	case '(':
		close = ')'
	default:
		return "", i, false
	}
	for j := i + 1; j < len(s); j++ {
		switch c := s[j]; {
		case c == '\\' && j+1 < len(s) && isPunctASCII(s[j+1]):
			j++
		case c == close:
			return unescape(s[i+1 : j]), j + 1, true
		case open == '(' && c == '(':
			return "", i, false
		}
	}
	return "", i, false
}

// entityAt reads an entity or numeric character reference at s[i], as
// CommonMark recognizes them (always with a semicolon).
func entityAt(s string, i int) (string, int, bool) {
	j := i + 1
	if j < len(s) && s[j] == '#' {
		j++
		hex := j < len(s) && (s[j] == 'x' || s[j] == 'X')
		if hex {
			j++
		}
		k := j
		for k < len(s) && k-j < 8 && (s[k] >= '0' && s[k] <= '9' || hex && strings.IndexByte("abcdefABCDEF", s[k]) >= 0) {
			k++
		}
		if k == j || k >= len(s) || s[k] != ';' || hex && k-j > 6 || !hex && k-j > 7 {
			return "", 0, false
		}
		base := 10
		if hex {
			base = 16
		}
		v, _ := strconv.ParseInt(s[j:k], base, 64)
		if v == 0 || v > unicode.MaxRune || v >= 0xD800 && v <= 0xDFFF {
			return string(utf8.RuneError), k + 1 - i, true
		}
		return string(rune(v)), k + 1 - i, false
	}
	k := j
	for k < len(s) && k-j <= entity.MaxName && isAlnum(s[k]) {
		k++
	}
	if k == j || k >= len(s) || s[k] != ';' {
		return "", 0, false
	}
	if r, ok := entity.Lookup(s[j : k+1]); ok {
		return r, k + 1 - i, false
	}
	return "", 0, false
}

// replacedIn counts the character references in s, text that unescape
// reads, that name no character and are read as U+FFFD.
func replacedIn(s string) int {
	if !strings.Contains(s, "&#") {
		return 0
	}
	n := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s) && isPunctASCII(s[i+1]):
			i++
		case c == '&':
			if _, k, bad := entityAt(s, i); k > 0 {
				if bad {
					n++
				}
				i += k - 1
			}
		}
	}
	return n
}

// unescape resolves backslash escapes and character references.
func unescape(s string) string {
	if strings.IndexByte(s, '\\') < 0 && strings.IndexByte(s, '&') < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s) && isPunctASCII(s[i+1]):
			b.WriteByte(s[i+1])
			i++
		case c == '&':
			if r, n, _ := entityAt(s, i); n > 0 {
				b.WriteString(r)
				i += n - 1
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isPunctASCII(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r' || unicode.Is(unicode.Zs, r)
}

func isPunct(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) }
