package gofasthtml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/blocks"
	"github.com/AOShei/go-fast-html/internal/yaml"
)

// Front matter: the block of metadata at the start of a Markdown file,
// YAML between "---" lines (Jekyll, Hugo, GitHub, and the skill, agent,
// prompt and rule files of AI tools) or TOML between "+++" lines (Hugo).
const (
	// AttrFrontMatter marks the code block that holds a Markdown file's
	// front matter; its value is the language, "yaml" or "toml".
	AttrFrontMatter = "front_matter"
	// ExtraFrontMatterPrefix begins the Metadata.Extra keys of the front
	// matter's values that no field of Metadata holds.
	ExtraFrontMatterPrefix = "front_matter."
	// maxFrontMatterKeys bounds the values of front matter put in the
	// metadata; the rest are in its block only.
	maxFrontMatterKeys = 256
	// maxFrontMatterKey bounds a key path in Metadata.Extra.
	maxFrontMatterKey = 1024
)

// frontMatter is the front matter found at the start of a Markdown text.
type frontMatter struct {
	lang         string // "yaml" or "toml"
	start, end   int    // the whole, fences included, to the end of the closing fence's line
	cstart, cend int    // the content between the fences
	values       []fmValue
	valuesCut    bool // more than maxFrontMatterKeys values
	// notYAML is why YAML front matter is not YAML, when its values were
	// read line by line instead.
	notYAML string
}

// fmValue is one value of the front matter: its key path and its text
// (the items of a list of scalars, each one).
type fmValue struct {
	path  string
	items []string
	list  bool
	// nulls says, item by item, which are a null (nil when none is): a
	// null is kept as written, but is no title, author or keyword.
	nulls []bool
	// long: the key path is longer than maxFrontMatterKey; the value is
	// left out of the metadata.
	long bool
}

// fmItem is an item of a YAML list of scalars; null for a YAML null.
type fmItem struct {
	v    string
	null bool
}

// yamlNull reports whether a plain scalar is a YAML null: "~", "null",
// "Null", "NULL" or nothing (YAML 1.2, the core schema).
func yamlNull(v string) bool {
	switch v {
	case "", "~", "null", "Null", "NULL":
		return true
	}
	return false
}

// findFrontMatter returns the front matter at the start of src, if any:
// a first line "---" (or "+++") alone, and a line "---" or "..." (or
// "+++") alone that closes it. YAML front matter must be a mapping (or
// empty): a text that begins with a thematic break is not taken for it.
func findFrontMatter(src []byte) (frontMatter, bool) {
	var fence string
	switch {
	case fenceLine(src, 0, "---"):
		fence = "---"
	case fenceLine(src, 0, "+++"):
		fence = "+++"
	default:
		return frontMatter{}, false
	}
	_, cstart := lineAt(src, 0)
	for pos := cstart; pos < len(src); {
		end, next := lineAt(src, pos)
		if fenceLine(src, pos, fence) || fence == "---" && fenceLine(src, pos, "...") {
			fm := frontMatter{start: 0, end: end, cstart: cstart, cend: pos}
			content := src[cstart:pos]
			if fence == "---" {
				fm.lang = "yaml"
				values, cut, why := yamlFrontMatter(content)
				if why != "" {
					// Front matter that is not YAML, as Cursor's rules
					// write it (globs: **/*.ts), is read line by line
					// when every line is key: value or under one.
					if values, cut, ok := looseFrontMatter(content); ok {
						fm.values, fm.valuesCut, fm.notYAML = values, cut, why
						return fm, true
					}
					return frontMatter{}, false
				}
				fm.values, fm.valuesCut = values, cut
			} else {
				fm.lang = "toml"
				fm.values, fm.valuesCut = tomlFrontMatter(content)
			}
			return fm, true
		}
		pos = next
	}
	return frontMatter{}, false
}

// fenceLine reports whether the line at pos is the fence alone, with
// trailing white space allowed.
func fenceLine(src []byte, pos int, fence string) bool {
	end, _ := lineAt(src, pos)
	line := src[pos:end]
	return bytes.HasPrefix(line, []byte(fence)) && isBlank(line[len(fence):])
}

// yamlFrontMatter reads YAML front matter: it must be one document whose
// value is a mapping, or nothing. Scalars and lists of scalars are
// values, under their key path (a.b for a nested mapping, a[0].b inside a
// list).
func yamlFrontMatter(content []byte) (values []fmValue, cut bool, why string) {
	p := yaml.NewParser(content, yaml.Limits{MaxDepth: 64})
	type open struct {
		mapping bool
		atKey   bool
		path    string
		long    bool // the path is longer than maxFrontMatterKey: not built
		index   int
		// a list of scalars, gathered as one value
		items  []fmItem
		scalar bool
	}
	var (
		stack []*open
		key   string
		docs  int
	)
	add := func(v fmValue) {
		if len(values) >= maxFrontMatterKeys {
			cut = true
			return
		}
		values = append(values, v)
	}
	// child returns the path of the next member of o; long when it is
	// longer than maxFrontMatterKey, and then it is not built.
	child := func(o *open) (string, bool) {
		if o.mapping {
			n := len(key)
			if o.path != "" {
				n += len(o.path) + 1
			}
			switch {
			case o.long || n > maxFrontMatterKey:
				return "", true
			case o.path == "":
				return key, false
			}
			return o.path + "." + key, false
		}
		i := strconv.Itoa(o.index)
		o.index++
		if o.long || len(o.path)+len(i)+2 > maxFrontMatterKey {
			return "", true
		}
		return o.path + "[" + i + "]", false
	}
	for {
		ev, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			var se *yaml.SyntaxError
			if errors.As(err, &se) {
				return nil, false, fmt.Sprintf("line %d, column %d: %s", se.Line+1, se.Column, se.Msg)
			}
			return nil, false, err.Error()
		}
		switch ev.Kind {
		case yaml.DocumentStart:
			docs++
			if docs > 1 {
				return nil, false, "more than one document"
			}
			continue
		case yaml.StreamStart, yaml.StreamEnd, yaml.DocumentEnd:
			continue
		}
		if len(stack) == 0 {
			// The document's value: a mapping, or an empty scalar.
			switch {
			case ev.Kind == yaml.MappingStart:
				stack = append(stack, &open{mapping: true, atKey: true})
				continue
			case ev.Kind == yaml.Scalar && ev.Value == "" && ev.Style == yaml.Plain:
				continue
			}
			return nil, false, "not a mapping"
		}
		top := stack[len(stack)-1]
		if top.mapping && top.atKey && ev.Kind != yaml.MappingEnd {
			top.atKey = false
			if ev.Kind != yaml.Scalar {
				return nil, false, "a key that is not a scalar"
			}
			key = ev.Value
			continue
		}
		if top.mapping {
			top.atKey = true
		}
		if !top.mapping && top.scalar && (ev.Kind == yaml.MappingStart || ev.Kind == yaml.SequenceStart) {
			// A list holding collections: its scalars are values of their own.
			top.scalar = false
			at := top.index
			top.index = 0
			for _, it := range top.items {
				path, long := child(top)
				v := fmValue{path: path, long: long, items: []string{it.v}}
				if it.null {
					v.nulls = []bool{true}
				}
				add(v)
			}
			top.index = at
			top.items = nil
		}
		switch ev.Kind {
		case yaml.MappingStart:
			path, long := child(top)
			stack = append(stack, &open{mapping: true, atKey: true, path: path, long: long})
		case yaml.SequenceStart:
			path, long := child(top)
			stack = append(stack, &open{path: path, long: long, scalar: true})
		case yaml.MappingEnd, yaml.SequenceEnd:
			stack = stack[:len(stack)-1]
			if !top.mapping && top.scalar {
				if len(top.items) > 0 {
					v := fmValue{path: top.path, long: top.long, items: make([]string, len(top.items)), list: true}
					for i, it := range top.items {
						v.items[i] = it.v
						if it.null {
							if v.nulls == nil {
								v.nulls = make([]bool, len(top.items))
							}
							v.nulls[i] = true
						}
					}
					add(v)
				}
			}
			if len(stack) > 0 && stack[len(stack)-1].mapping {
				stack[len(stack)-1].atKey = true
			}
		case yaml.Scalar, yaml.Alias:
			v := ev.Value
			if ev.Kind == yaml.Alias {
				v = "*" + v
			}
			// A null (~, null, or nothing) is kept as written.
			null := ev.Kind == yaml.Scalar && ev.Style == yaml.Plain && ev.Tag == "" && yamlNull(v)
			if !top.mapping && top.scalar {
				top.items = append(top.items, fmItem{v: v, null: null})
				top.index++
				continue
			}
			path, long := child(top)
			fv := fmValue{path: path, long: long, items: []string{v}}
			if null {
				fv.nulls = []bool{true}
			}
			add(fv)
		}
	}
	return values, cut, ""
}

// looseFrontMatter reads front matter that is not YAML line by line:
// "key: value" lines at the start of the line, a value in quotes taken
// out of them, and lines under a key with no value as its list ("- item")
// or its text. It is front matter only when every line with text is one
// of these (or a comment) and there is a key.
func looseFrontMatter(content []byte) (values []fmValue, cut, ok bool) {
	var (
		cur  *fmValue
		more []string // the lines that continue cur's first item
	)
	// item adds an item to cur's, written raw: a null when it is one
	// unquoted (nothing, "~", "null").
	var nulls []bool
	item := func(raw string) {
		cur.items = append(cur.items, unquote(raw))
		nulls = append(nulls, yamlNull(raw))
	}
	flush := func() {
		if cur == nil {
			return
		}
		if len(more) > 0 {
			// Joined once: a value of many lines costs its length.
			cur.items[0] += " " + strings.Join(more, " ")
			more = more[:0]
			nulls[0] = false
		}
		if len(cur.items) == 0 {
			item("") // a key with nothing after it: a null
		}
		if slices.Contains(nulls, true) {
			cur.nulls = slices.Clone(nulls)
		}
		nulls = nulls[:0]
		if len(values) >= maxFrontMatterKeys {
			cut = true
		} else {
			values = append(values, *cur)
		}
		cur = nil
	}
	for pos := 0; pos < len(content); {
		end, next := lineAt(content, pos)
		line := string(content[pos:end])
		pos = next
		t := strings.TrimSpace(line)
		switch {
		case t == "" || t[0] == '#':
			continue
		case line[0] == ' ' || line[0] == '\t':
			if cur == nil {
				return nil, false, false
			}
			if it, ok := strings.CutPrefix(t, "- "); ok || t == "-" {
				item(strings.TrimSpace(it))
				cur.list = true
			} else if len(cur.items) > 0 && !cur.list {
				more = append(more, t)
			} else {
				cur.items = append(cur.items, t)
				nulls = append(nulls, false)
			}
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 || !looseKey(line[:colon]) {
			return nil, false, false
		}
		flush()
		cur = &fmValue{path: line[:colon]}
		if v := strings.TrimSpace(line[colon+1:]); v != "" {
			item(v)
		}
	}
	flush()
	return values, cut, len(values) > 0 || cut
}

// looseKey reports whether k is a key of loose front matter.
func looseKey(k string) bool {
	for _, c := range k {
		if !(c == '_' || c == '-' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// unquote takes a value out of the quotes around it, if it has them.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// tomlFrontMatter reads the values of TOML front matter that are simple:
// key = value lines, under [table] headers, with strings, numbers,
// booleans, dates and arrays of them on one line. Anything else is in the
// front matter's block only.
func tomlFrontMatter(content []byte) (values []fmValue, cut bool) {
	table := ""
	inArray := false // under an array of tables: not read into the metadata
	for pos := 0; pos < len(content); {
		end, next := lineAt(content, pos)
		line := strings.TrimSpace(string(content[pos:end]))
		pos = next
		switch {
		case line == "" || line[0] == '#':
			continue
		case strings.HasPrefix(line, "[["):
			table, inArray = "", true
			continue
		case line[0] == '[':
			table, inArray = "", false
			if i := strings.IndexByte(line, ']'); i > 0 {
				table = strings.TrimSpace(line[1:i])
			}
			continue
		case inArray:
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.Trim(strings.TrimSpace(line[:eq]), `"'`)
		if table != "" {
			key = table + "." + key
		}
		items, list, ok := tomlValue(strings.TrimSpace(line[eq+1:]))
		if !ok {
			continue
		}
		if len(values) >= maxFrontMatterKeys {
			return values, true
		}
		values = append(values, fmValue{path: key, items: items, list: list})
	}
	return values, false
}

// tomlValue reads a TOML value written on one line: a string, a number, a
// boolean, a date, or an array of these.
func tomlValue(v string) (items []string, list, ok bool) {
	if strings.HasPrefix(v, "[") {
		if !strings.HasSuffix(strings.TrimSpace(stripTOMLComment(v)), "]") {
			return nil, false, false
		}
		inner := strings.TrimSpace(stripTOMLComment(v))
		inner = inner[1 : len(inner)-1]
		for _, part := range splitTOMLArray(inner) {
			if part = strings.TrimSpace(part); part == "" {
				continue
			}
			s, ok := tomlScalar(part)
			if !ok {
				return nil, false, false
			}
			items = append(items, s)
		}
		return items, true, true
	}
	s, ok := tomlScalar(stripTOMLComment(v))
	if !ok {
		return nil, false, false
	}
	return []string{s}, false, true
}

func tomlScalar(v string) (string, bool) {
	v = strings.TrimSpace(v)
	switch {
	case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' && !strings.HasPrefix(v, `"""`):
		s, err := strconv.Unquote(v)
		if err != nil {
			return v[1 : len(v)-1], true
		}
		return s, true
	case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' && !strings.HasPrefix(v, "'''"):
		return v[1 : len(v)-1], true
	case v == "" || strings.ContainsAny(v[:1], `"'{[`):
		return "", false
	}
	return v, true
}

// stripTOMLComment removes a comment after a value, outside strings.
func stripTOMLComment(v string) string {
	var quote byte
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}

// splitTOMLArray splits an array's inside at commas outside strings.
func splitTOMLArray(v string) []string {
	var parts []string
	var quote byte
	from := 0
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ',':
			parts = append(parts, v[from:i])
			from = i + 1
		}
	}
	return append(parts, v[from:])
}

// applyFrontMatter puts the front matter's values in the document's
// metadata: title, author(s), description, keywords and tags, language,
// and dates to their fields when these are empty; every other value in
// Extra under front_matter.<path>. A list is written with ", " between
// its items in Extra. A null (~, null, or nothing) or an empty string is
// no title, author, keyword or date: a key whose value is only that is in
// Extra as written ("~", "null", ""), and a null in a list of authors or
// keywords is left out of it. In a list in Extra, a null is as written.
//
// It returns the values cut at MaxMetaValue or left out for a key path
// longer than maxFrontMatterKey, and the authors and keywords left out
// past blocks.MaxMetaList.
func applyFrontMatter(md *gofastcore.Metadata, fm frontMatter, ctl *int) (cut, listLeft int) {
	extra := func(k, v string) {
		if md.Extra == nil {
			md.Extra = map[string]string{}
		}
		if _, ok := md.Extra[k]; !ok {
			md.Extra[k] = v
		}
	}
	bounded := func(list []string, items []string) []string {
		room := max(blocks.MaxMetaList-len(list), 0)
		if len(items) > room {
			listLeft += len(items) - room
			items = items[:room]
		}
		return append(list, items...)
	}
	for _, v := range fm.values {
		if v.long {
			cut++
			continue
		}
		// all is every item as written, nulls and empty strings too; items
		// those that are a value for a field of Metadata.
		all := make([]string, 0, len(v.items))
		items := make([]string, 0, len(v.items))
		for i, it := range v.items {
			s, c := cleanMeta(it)
			*ctl += c
			if len(s) > MaxMetaValue {
				s, cut = cutUTF8(s, MaxMetaValue), cut+1
			}
			all = append(all, s)
			if s != "" && (v.nulls == nil || !v.nulls[i]) {
				items = append(items, s)
			}
		}
		if len(all) == 0 {
			continue // an empty list
		}
		join := func(items []string) string {
			one := strings.Join(items, ", ")
			if len(one) > MaxMetaValue {
				one, cut = cutUTF8(one, MaxMetaValue), cut+1
			}
			return one
		}
		one := join(items)
		field := fmField(v.path)
		if len(items) == 0 {
			field = "" // no value for a field: in Extra, as written
		}
		switch field {
		case "title":
			if md.Title == "" && !v.list {
				md.Title = one
				continue
			}
		case "author", "authors":
			md.Authors = bounded(md.Authors, items)
			continue
		case "description", "summary":
			if _, ok := md.Extra["description"]; !ok && !v.list {
				extra("description", one)
				continue
			}
		case "keywords", "tags":
			md.Keywords = bounded(md.Keywords, items)
			continue
		case "lang", "language", "locale":
			if md.Language == "" && !v.list {
				md.Language = one
				continue
			}
		case "date", "created", "published":
			if t, ok := frontMatterDate(one); ok && md.Created.IsZero() && !v.list {
				md.Created = t
				continue
			}
		case "lastmod", "updated", "modified", "last_modified_at":
			if t, ok := frontMatterDate(one); ok && md.Modified.IsZero() && !v.list {
				md.Modified = t
				continue
			}
		}
		if len(v.path) > maxFrontMatterKey {
			cut++ // a key path that long is left out
			continue
		}
		key, c := cleanMeta(v.path)
		*ctl += c
		if len(all) > len(items) {
			one = join(all)
		}
		extra(ExtraFrontMatterPrefix+key, one)
	}
	return cut, listLeft
}

// fmField returns the lowercased key of a top-level value, and "authors"
// for the names in a list of authors (authors[0].name, authors[1]).
func fmField(path string) string {
	p := strings.ToLower(path)
	if rest, ok := strings.CutPrefix(p, "authors["); ok || strings.HasPrefix(p, "author[") {
		if !ok {
			rest = p[len("author["):]
		}
		if i := strings.IndexByte(rest, ']'); i > 0 {
			if _, err := strconv.Atoi(rest[:i]); err == nil && (rest[i+1:] == "" || rest[i+1:] == ".name") {
				return "authors"
			}
		}
	}
	return p
}

// MaxMetaValue bounds a value of front matter put in the metadata, as a
// page's metadata values are bounded.
const MaxMetaValue = 64 << 10

// frontMatterDate reads the date forms front matter uses.
func frontMatterDate(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05 -07:00",
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// cleanMeta drops a metadata value's control characters other than tab
// and line feed, writes its ligatures as letters, and trims it; it
// returns the count dropped.
func cleanMeta(s string) (string, int) {
	n := 0
	s = gofastcore.ExpandLigatures(s)
	if hasControl(s) {
		var b []byte
		for i, line := range strings.Split(s, "\n") {
			if i > 0 {
				b = append(b, '\n')
			}
			b = dropControls(b, []byte(line), &n)
		}
		s = string(b)
	}
	return strings.TrimSpace(s), n
}

func cutUTF8(s string, n int) string {
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return strings.TrimSpace(s[:n])
}

// frontMatterBlock makes the code block of the front matter in src
// (decoded text; source maps its offsets to the input's).
func frontMatterBlock(src []byte, fm frontMatter, source func(int) int64, controls *int) gofastcore.Block {
	off := source(fm.start)
	return gofastcore.Block{Kind: gofastcore.KindCode, Text: cleanCode(src[fm.cstart:fm.cend], controls),
		Attrs: map[string]string{gofastcore.AttrLanguage: fm.lang, AttrFrontMatter: fm.lang},
		Loc:   gofastcore.Location{Offset: off, Length: source(fm.end) - off}}
}
