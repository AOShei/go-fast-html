package blocks

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/dom"
)

func (w *walker) title(n *dom.Node) {
	if w.res.Title || n.NS != dom.HTML || w.frame != nil {
		return
	}
	w.res.Title = true
	t := cleanProse(string(textOf(n)), true)
	w.res.Meta.Title = w.metaValue(t)
}

func (w *walker) meta(n *dom.Node) {
	content, _ := n.Attr("content")
	if eq, ok := n.Attr("http-equiv"); ok && strings.EqualFold(strings.TrimSpace(eq), "refresh") {
		if strings.Contains(strings.ToLower(content), "url") {
			w.res.External++
		}
		return
	}
	if w.frame != nil {
		return // a srcdoc document's meta is not the page's
	}
	name, _ := n.Attr("name")
	m := &w.res.Meta
	v := w.metaValue(cleanProse(content, true))
	if v == "" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "author":
		if len(m.Authors) < 256 {
			m.Authors = append(m.Authors, v)
		} else {
			w.res.MetaCut = true
		}
	case "description":
		if m.Extra == nil {
			m.Extra = map[string]string{}
		}
		if _, ok := m.Extra["description"]; !ok {
			m.Extra["description"] = v
		}
	case "keywords":
		if len(m.Keywords) < 256 {
			m.Keywords = append(m.Keywords, v)
		} else {
			w.res.MetaCut = true
		}
	case "generator":
		if m.Application == "" {
			m.Application = v
		}
	}
}

func (w *walker) metaValue(s string) string {
	if len(s) <= MaxMeta {
		return s
	}
	w.res.MetaCut = true
	n := MaxMeta
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return strings.TrimSpace(s[:n])
}

// image emits an img as an image block, after the block it stands in.
func (w *walker) image(n *dom.Node) {
	w.res.Images++
	src, _ := n.Attr("src")
	if n.NS == dom.SVG {
		// An SVG image: href, or xlink:href in SVG 1.1.
		var ok bool
		if src, ok = n.Attr("href"); !ok {
			src, _ = n.Attr("xlink:href")
		}
	}
	src = strings.TrimSpace(src)
	data := hasScheme(src, "data")
	if src != "" && !data && !isScriptURL(src) {
		w.external(src)
	}
	if w.cfg.Images == gofastcore.ImagesNone {
		return
	}
	alt, _ := n.Attr("alt")
	img := &gofastcore.Image{}
	if v, ok := n.Attr("width"); ok {
		if x, ok := parseInt(v); ok && x > 0 && x < 1<<24 {
			img.Width = int(x)
		}
	}
	if v, ok := n.Attr("height"); ok {
		if x, ok := parseInt(v); ok && x > 0 && x < 1<<24 {
			img.Height = int(x)
		}
	}
	if data {
		mime, bytes, ok, overBudget := w.dataURI(src)
		switch {
		case overBudget:
			w.res.ImageBudget++
		case ok:
			sum := sha256.Sum256(bytes)
			img.ID = "sha256:" + hex.EncodeToString(sum[:16])
			img.MIME = mime
			if w.cfg.Images == gofastcore.ImagesData {
				img.Data = bytes
			}
		default:
			w.res.ImageData++
		}
	} else if ref, ok := w.resolve(src); ok {
		img.ID, img.MIME = ref.ID, ref.MIME
		if w.cfg.Images == gofastcore.ImagesData {
			if d, ok := ref.Data(); ok {
				img.Data = d
			} else {
				w.res.ImageData++
			}
		}
	} else if src != "" {
		img.ID = cleanRef(src)
		if len(img.ID) > 2048 {
			sum := sha256.Sum256([]byte(src))
			img.ID = "sha256:" + hex.EncodeToString(sum[:16])
		}
	}
	w.later(gofastcore.Block{Kind: gofastcore.KindImage, Text: cleanProse(alt, true), Image: img,
		Display: n.Display, Loc: w.loc(n.Start, n.End, anchorOf(n))})
}

// ImageRef is an image a page refers to that is inside the container the
// page is in (an EPUB).
type ImageRef struct {
	ID, MIME string
	// Data reads the image's bytes, within the decode budget.
	Data func() ([]byte, bool)
}

func (w *walker) resolve(src string) (ImageRef, bool) {
	if w.cfg.Image == nil || src == "" {
		return ImageRef{}, false
	}
	return w.cfg.Image(src)
}

// dataURI decodes a data: URI within the decode budget.
// dataURI decodes a data: URI. The bytes are decoded whatever the images
// mode, since the image's ID is their hash. overBudget reports that they
// would pass the decode budget and were not decoded.
func (w *walker) dataURI(s string) (mime string, data []byte, ok, overBudget bool) {
	rest := s[5:]
	head, body, found := strings.Cut(rest, ",")
	if !found {
		return "", nil, false, false
	}
	params := strings.Split(head, ";")
	mime = strings.ToLower(strings.TrimSpace(params[0]))
	b64 := false
	for _, p := range params[1:] {
		if strings.EqualFold(strings.TrimSpace(p), "base64") {
			b64 = true
		}
	}
	size := int64(len(body))
	if b64 {
		size = int64(base64.StdEncoding.DecodedLen(len(body)))
	}
	if !w.cfg.Decode.Take(size) {
		return "", nil, false, true
	}
	if b64 {
		clean := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
				return -1
			}
			return r
		}, body)
		clean = strings.TrimRight(clean, "=")
		d, err := base64.RawStdEncoding.DecodeString(clean)
		if err != nil {
			return "", nil, false, false
		}
		data = d
	} else {
		d, err := url.PathUnescape(body)
		if err != nil {
			return "", nil, false, false
		}
		data = []byte(d)
	}
	if strings.ContainsFunc(mime, func(r rune) bool { return r < 0x21 || r > 0x7E }) {
		mime = ""
	}
	return mime, data, true, false
}

// textOf returns all text under n.
func textOf(n *dom.Node) []byte {
	var out []byte
	var walk func(*dom.Node)
	walk = func(p *dom.Node) {
		for c := p.FirstChild; c != nil; c = c.Next {
			if c.Type == dom.TextNode {
				out = append(out, c.Text...)
			} else if c.Type == dom.ElementNode {
				walk(c)
			}
		}
	}
	walk(n)
	return out
}

// Clean makes a string read from outside a page (EPUB metadata, a table
// of contents) into prose: invalid UTF-8 replaced, white space collapsed,
// control characters dropped, ligatures written as letters.
func Clean(s string) string {
	return cleanProse(strings.ToValidUTF8(s, "�"), true)
}

// cleanProse collapses white space (keeping line breaks when lines is
// false), drops control characters and writes ligatures as letters.
func cleanProse(s string, collapse bool) string {
	b := newBuf()
	ws := dom.Normal
	if !collapse {
		ws = dom.PreLine
	}
	b.add([]byte(s), ws, 0, false)
	t, _, _ := b.finish()
	return t
}

// cleanRef drops control characters and surrounding white space from an
// address or name, which is otherwise left as the source has it.
func cleanRef(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// anchorOf returns n's id, or for an <a>, its name.
func anchorOf(n *dom.Node) string {
	id, ok := n.Attr("id")
	if !ok && n.Name == "a" {
		id, ok = n.Attr("name")
	}
	if !ok {
		return ""
	}
	id = cleanRef(id)
	if len(id) > MaxAnchor {
		return ""
	}
	return id
}

// codeLanguage reads the language of a code block from a class such as
// "language-go" or "lang-go" on the element or the code element in it.
func codeLanguage(n *dom.Node) string {
	for _, e := range []*dom.Node{n, firstElement(n, "code")} {
		if e == nil {
			continue
		}
		class, _ := e.Attr("class")
		for _, c := range strings.Fields(class) {
			for _, p := range []string{"language-", "lang-"} {
				if l, ok := strings.CutPrefix(c, p); ok && l != "" && len(l) <= 64 {
					return cleanRef(strings.ToLower(l))
				}
			}
		}
	}
	return ""
}

func firstElement(n *dom.Node, name string) *dom.Node {
	for c := n.FirstChild; c != nil; c = c.Next {
		if c.Type == dom.ElementNode && c.Name == name {
			return c
		}
	}
	return nil
}

func hasScheme(s, scheme string) bool {
	s = strings.TrimLeft(s, " \t\n\r\f")
	return len(s) > len(scheme) && strings.EqualFold(s[:len(scheme)], scheme) && s[len(scheme)] == ':'
}

// isScriptURL reports a javascript: or vbscript: address. Browsers strip
// tabs and newlines from addresses before reading the scheme.
func isScriptURL(s string) bool {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	return hasScheme(s, "javascript") || hasScheme(s, "vbscript")
}

func loadsResource(rel string) bool {
	for _, r := range strings.Fields(strings.ToLower(rel)) {
		switch r {
		case "stylesheet", "icon", "preload", "prefetch", "modulepreload", "import", "manifest", "apple-touch-icon":
			return true
		}
	}
	return false
}

func parseInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	end := 0
	if end < len(s) && (s[end] == '-' || s[end] == '+') {
		end++
	}
	for end < len(s) && s[end] >= '0' && s[end] <= '9' && end < 12 {
		end++
	}
	v, err := strconv.ParseInt(s[:end], 10, 64)
	return v, err == nil
}

// marker returns the label of the next item of a list.
func marker(lc *listCtx, depth int) string {
	if !lc.ordered {
		switch strings.ToLower(lc.typ) {
		case "none":
			return ""
		case "circle":
			return "◦"
		case "square":
			return "▪"
		case "disc":
			return "•"
		}
		switch (depth - 1) % 3 {
		case 1:
			return "◦"
		case 2:
			return "▪"
		}
		return "•"
	}
	n := lc.n
	switch lc.typ {
	case "a":
		return alpha(n, 'a') + "."
	case "A":
		return alpha(n, 'A') + "."
	case "i":
		return strings.ToLower(roman(n)) + "."
	case "I":
		return roman(n) + "."
	}
	return strconv.FormatInt(n, 10) + "."
}

func alpha(n int64, base byte) string {
	if n <= 0 {
		return strconv.FormatInt(n, 10)
	}
	var b []byte
	for n > 0 {
		n--
		b = append([]byte{base + byte(n%26)}, b...)
		n /= 26
	}
	return string(b)
}

func roman(n int64) string {
	if n <= 0 || n >= 4000 {
		return strconv.FormatInt(n, 10)
	}
	vals := []int64{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var sb strings.Builder
	for i, v := range vals {
		for n >= v {
			sb.WriteString(syms[i])
			n -= v
		}
	}
	return sb.String()
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// blockElements are the elements a browser lays out as blocks, besides
// those with handling of their own.
var blockElements = set("address", "article", "aside", "body", "center", "dd", "details",
	"dialog", "div", "dl", "dt", "fieldset", "figure", "footer", "frameset", "header",
	"hgroup", "html", "main", "nav", "noframes", "noscript", "p", "search", "section",
	"summary", "template", "tr", "td", "th", "tbody", "thead", "tfoot", "option", "optgroup",
	"noembed", "output")

var inlineStyle = map[string]gofastcore.Style{
	"b": gofastcore.StyleBold, "strong": gofastcore.StyleBold,
	"i": gofastcore.StyleItalic, "em": gofastcore.StyleItalic, "cite": gofastcore.StyleItalic,
	"dfn": gofastcore.StyleItalic, "var": gofastcore.StyleItalic,
	"u": gofastcore.StyleUnderline, "ins": gofastcore.StyleUnderline,
	"s": gofastcore.StyleStrike, "strike": gofastcore.StyleStrike, "del": gofastcore.StyleStrike,
	"code": gofastcore.StyleCode, "kbd": gofastcore.StyleCode, "samp": gofastcore.StyleCode,
	"tt":  gofastcore.StyleCode,
	"sub": gofastcore.StyleSub, "sup": gofastcore.StyleSuper,
}
