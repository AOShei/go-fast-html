package gofasthtml

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core/entity"
	"github.com/AOShei/go-fast-html/internal/md"
)

// TestCommonMarkSpec renders the examples of the CommonMark spec with the
// Markdown parser and compares the HTML with the spec's, normalized as
// the reference test runner (normalize.py) normalizes it. The spec is
// CC-BY-SA 4.0 and is not in this repository: the test reads it from
// $GOFASTHTML_COMMONMARK_SPEC, by default ../.testdata/commonmark-0.31.2-spec.json,
// and is skipped without it.
//
// Examples known to fail are in commonMarkFailing with the reason; the
// test fails when one of them passes (remove it) or another fails.
func TestCommonMarkSpec(t *testing.T) {
	path := os.Getenv("GOFASTHTML_COMMONMARK_SPEC")
	if path == "" {
		path = "../.testdata/commonmark-0.31.2-spec.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("CommonMark spec not found: %v", err)
	}
	var examples []struct {
		Markdown, HTML, Section string
		Example                 int
	}
	if err := json.Unmarshal(raw, &examples); err != nil {
		t.Fatal(err)
	}
	type tally struct{ pass, total int }
	sections := map[string]*tally{}
	var order []string
	passed := 0
	for _, ex := range examples {
		s := sections[ex.Section]
		if s == nil {
			s = &tally{}
			sections[ex.Section] = s
			order = append(order, ex.Section)
		}
		s.total++
		d, err := md.Parse(context.Background(), ex.Markdown)
		if err != nil {
			t.Fatal(err)
		}
		got := normalizeHTML(string(d.RenderSpec().B))
		want := normalizeHTML(ex.HTML)
		ok := got == want
		if ok {
			s.pass++
			passed++
		}
		reason, expected := commonMarkFailing[ex.Example]
		switch {
		case ok && expected:
			t.Errorf("example %d (%s) passes now; remove it from commonMarkFailing (%s)", ex.Example, ex.Section, reason)
		case !ok && !expected:
			t.Errorf("example %d (%s) fails:\n%q\n got %q\nwant %q", ex.Example, ex.Section, ex.Markdown, got, want)
		case !ok && os.Getenv("GOFASTHTML_COMMONMARK_VERBOSE") != "":
			t.Logf("example %d (%s, expected: %s):\n%q\n got %q\nwant %q", ex.Example, ex.Section, reason, ex.Markdown, got, want)
		}
	}
	var lines []string
	for _, sec := range order {
		s := sections[sec]
		lines = append(lines, fmt.Sprintf("%-40s %3d/%3d", sec, s.pass, s.total))
	}
	t.Logf("CommonMark 0.31.2: %d of %d examples pass\n%s", passed, len(examples), strings.Join(lines, "\n"))
}

// commonMarkFailing lists the spec examples that still fail, with why.
var commonMarkFailing = map[int]string{}

// blockTags are the tags around which normalize.py drops white space.
var blockTags = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`article header aside hgroup blockquote hr iframe body li map button object
		canvas ol caption output col p colgroup pre dd progress div section dl table td dt tbody embed
		textarea fieldset tfoot figcaption th figure thead footer tr form ul h1 h2 h3 h4 h5 h6 video script style`) {
		blockTags[t] = true
	}
}

// normalizeHTML follows commonmark-spec's normalize.py: white space
// collapsed outside pre and dropped around block tags, attributes
// sorted, self-closing tags written as start tags, character references
// resolved and & < > " escaped again.
func normalizeHTML(s string) string {
	var out strings.Builder
	last, lastTag := "", ""
	inPre := false
	rstrip := func() {
		str := strings.TrimRight(out.String(), " \t\n\r\f")
		out.Reset()
		out.WriteString(str)
	}
	data := func(d string) {
		afterTag := last == "endtag" || last == "starttag"
		if afterTag && lastTag == "br" {
			d = strings.TrimLeft(d, "\n")
		}
		if !inPre {
			d = wsRun.ReplaceAllString(d, " ")
		}
		if afterTag && blockTags[lastTag] && !inPre {
			if last == "starttag" {
				d = strings.TrimLeft(d, " ")
			} else {
				d = strings.Trim(d, " ")
			}
		}
		out.WriteString(d)
		last = "data"
	}
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			data(text.String())
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c == '&' {
			r, n := charRef(s[i:])
			if n > 0 {
				text.WriteString(escapeText(r))
				i += n
				continue
			}
			text.WriteString("&amp;")
			i++
			continue
		}
		if c != '<' {
			switch c {
			case '>':
				text.WriteString("&gt;")
			case '"':
				text.WriteString("&quot;")
			default:
				text.WriteByte(c)
			}
			i++
			continue
		}
		rest := s[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			flush()
			end := strings.Index(rest[4:], "-->")
			if end < 0 {
				end = len(rest) - 4
			}
			out.WriteString(rest[:min(len(rest), 4+end+3)])
			last = "comment"
			i += min(len(rest), 4+end+3)
			continue
		case strings.HasPrefix(rest, "<?") || strings.HasPrefix(rest, "<!"):
			flush()
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				end = len(rest) - 1
			}
			out.WriteString(rest[:end+1])
			i += end + 1
			continue
		}
		name, attrs, closing, selfClose, n := parseTag(rest)
		if n == 0 {
			text.WriteString("&lt;")
			i++
			continue
		}
		flush()
		i += n
		if closing {
			if name == "pre" {
				inPre = false
			} else if blockTags[name] {
				rstrip()
			}
			out.WriteString("</" + name + ">")
			lastTag, last = name, "endtag"
			continue
		}
		if name == "pre" {
			inPre = true
		}
		if blockTags[name] {
			rstrip()
		}
		sort.Slice(attrs, func(a, b int) bool { return attrs[a][0] < attrs[b][0] })
		out.WriteString("<" + name)
		for _, a := range attrs {
			out.WriteString(" " + a[0] + `="` + escapeAttr(a[1]) + `"`)
		}
		out.WriteString(">")
		lastTag, last = name, "starttag"
		if selfClose {
			last = "endtag"
		}
	}
	flush()
	return out.String()
}

var wsRun = regexp.MustCompile(`[ \t\n\r\f]+`)

func isWS(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' }

func escapeText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;").Replace(s)
}

// charRef resolves a character reference at the start of s.
func charRef(s string) (string, int) {
	if strings.HasPrefix(s, "&#") {
		j := 2
		base := 10
		if j < len(s) && (s[j] == 'x' || s[j] == 'X') {
			j++
			base = 16
		}
		k := j
		for k < len(s) && strings.IndexByte("0123456789abcdefABCDEF", s[k]) >= 0 && (base == 16 || s[k] <= '9') {
			k++
		}
		if k == j || k >= len(s) || s[k] != ';' {
			return "", 0
		}
		v, err := strconv.ParseInt(s[j:k], base, 64)
		if err != nil || v == 0 || v > utf8.MaxRune {
			return "�", k + 1
		}
		return string(rune(v)), k + 1
	}
	k := 1
	for k < len(s) && k < 40 && (s[k] >= 'a' && s[k] <= 'z' || s[k] >= 'A' && s[k] <= 'Z' || s[k] >= '0' && s[k] <= '9') {
		k++
	}
	if k < len(s) && s[k] == ';' {
		if r, ok := entity.Lookup(s[1 : k+1]); ok {
			return r, k + 1
		}
	}
	return "", 0
}

// parseTag reads a start or end tag at the start of s.
func parseTag(s string) (name string, attrs [][2]string, closing, selfClose bool, n int) {
	i := 1
	if i < len(s) && s[i] == '/' {
		closing = true
		i++
	}
	j := i
	for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || j > i && (s[j] >= '0' && s[j] <= '9' || s[j] == '-')) {
		j++
	}
	if j == i {
		return "", nil, false, false, 0
	}
	name = strings.ToLower(s[i:j])
	for {
		for j < len(s) && isWS(rune(s[j])) {
			j++
		}
		if j >= len(s) {
			return "", nil, false, false, 0
		}
		if s[j] == '>' {
			return name, attrs, closing, selfClose, j + 1
		}
		if s[j] == '/' {
			selfClose = true
			j++
			continue
		}
		k := j
		for k < len(s) && !isWS(rune(s[k])) && s[k] != '=' && s[k] != '>' && s[k] != '/' {
			k++
		}
		if k == j {
			return "", nil, false, false, 0
		}
		an := strings.ToLower(s[j:k])
		j = k
		for j < len(s) && isWS(rune(s[j])) {
			j++
		}
		v := ""
		if j < len(s) && s[j] == '=' {
			j++
			for j < len(s) && isWS(rune(s[j])) {
				j++
			}
			if j < len(s) && (s[j] == '"' || s[j] == '\'') {
				e := strings.IndexByte(s[j+1:], s[j])
				if e < 0 {
					return "", nil, false, false, 0
				}
				v = s[j+1 : j+1+e]
				j += e + 2
			} else {
				e := j
				for e < len(s) && !isWS(rune(s[e])) && s[e] != '>' {
					e++
				}
				v = s[j:e]
				j = e
			}
		}
		attrs = append(attrs, [2]string{an, unescapeRefs(v)})
	}
}

func unescapeRefs(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '&' {
			if r, n := charRef(s[i:]); n > 0 {
				b.WriteString(r)
				i += n
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
