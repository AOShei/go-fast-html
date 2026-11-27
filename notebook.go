package gofasthtml

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/blocks"
	"github.com/AOShei/go-fast-html/internal/dom"
	"github.com/AOShei/go-fast-html/internal/md"
)

// AttrOutput is the Block.Attrs key that marks a block made from the
// output of a notebook's code cell, as the notebook stores it: "stdout"
// or "stderr" for text the code printed to that stream, "execute_result"
// for the value of the cell, "display_data" for what the code displayed,
// "error" for the error it raised. The blocks of a cell's code and of
// its other cells have no such key.
const AttrOutput = "output"

// Values of Attrs[AttrOutput].
const (
	OutputStdout        = "stdout"
	OutputStderr        = "stderr"
	OutputExecuteResult = "execute_result"
	OutputDisplayData   = "display_data"
	OutputError         = "error"
)

// nbRepresentations are the representations of an output a notebook
// shows, in the order JupyterLab prefers them; the first one an output
// has is read, and its others are not.
var nbRepresentations = []string{
	"text/html", "text/markdown", "text/latex",
	"image/svg+xml", "image/bmp", "image/png", "image/jpeg", "image/gif", "image/webp",
	"text/plain",
}

// nbV3Keys are the keys under which version 3 of the notebook format
// stores an output's representations.
var nbV3Keys = map[string]string{
	"html": "text/html", "markdown": "text/markdown", "latex": "text/latex",
	"svg": "image/svg+xml", "png": "image/png", "jpeg": "image/jpeg", "text": "text/plain",
}

// nbCellsLeft says how many cells were not read after the text budget ran
// out, for the warning.
func nbCellsLeft(n int) string {
	if n == 0 {
		return ""
	}
	return " (" + count(n, "cell", "cells") + " not read)"
}

// notebook reads a Jupyter notebook into one part of kind body: its
// cells in order, Markdown cells as Markdown, code cells as code blocks
// followed by the blocks of their outputs.
func (e *Extractor) notebook(ctx context.Context, doc *gofastcore.Document, src []byte, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget) (*gofastcore.Document, error) {
	src = bytes.TrimPrefix(src, []byte("\xEF\xBB\xBF"))
	var top map[string]json.RawMessage
	if err := json.Unmarshal(src, &top); err != nil || top == nil {
		return nil, fmt.Errorf("gofasthtml: the notebook is not a JSON object: %w", gofastcore.ErrMalformed)
	}
	r := nbReader{e: e, ctx: ctx, opts: opts, text: text, decode: decode}
	version, hasVersion := nbNumber(top["nbformat"])
	minor, _ := nbNumber(top["nbformat_minor"])
	var cells []json.RawMessage
	switch {
	case hasVersion && version < 3:
		return nil, fmt.Errorf("gofasthtml: notebook format version %d: %w", version, gofastcore.ErrUnsupported)
	case top["cells"] != nil && (version >= 4 || !hasVersion):
		if !r.decodeJSON(top["cells"], &cells) {
			return nil, fmt.Errorf("gofasthtml: the notebook's cells are not a list: %w", gofastcore.ErrMalformed)
		}
	case top["worksheets"] != nil:
		var sheets []map[string]json.RawMessage
		if !r.decodeJSON(top["worksheets"], &sheets) {
			return nil, fmt.Errorf("gofasthtml: the notebook's worksheets are not a list: %w", gofastcore.ErrMalformed)
		}
		for _, ws := range sheets {
			var c []json.RawMessage
			if ws["cells"] != nil && !r.decodeJSON(ws["cells"], &c) {
				r.wrongType++
			}
			cells = append(cells, c...)
		}
		r.v3 = true
	default:
		return nil, fmt.Errorf("gofasthtml: the input has no notebook cells: %w", gofastcore.ErrMalformed)
	}

	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1}
	t := &tally{}
	if n := nbInvalidUTF8(src); n > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs,
			fmt.Sprintf("%d byte sequences are not valid UTF-8 and appear as U+FFFD", n))
	}
	doc.Metadata = r.metadata(top["metadata"], t)
	if hasVersion {
		if doc.Metadata.Extra == nil {
			doc.Metadata.Extra = map[string]string{}
		}
		doc.Metadata.Extra["nbformat"] = strconv.Itoa(version) + "." + strconv.Itoa(minor)
	}

	r.pc = pageConfig{opts: opts, text: text, decode: decode, notebook: true,
		noDataElsewhere: func() int { return r.attachOver },
		// An attachment is in the notebook, not loaded from elsewhere.
		internal: func(ref string) bool { return strings.HasPrefix(ref, "attachment:") }}
	r.agg = &blocks.Result{}
	for i, raw := range cells {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		before := len(r.agg.Blocks)
		if err := r.cell(i, raw); err != nil {
			return nil, err
		}
		if r.agg.Cut || r.decodeCut {
			left := len(cells) - i - 1
			if len(r.agg.Blocks) == before {
				// Nothing of this cell was output: it was not read either.
				left++
			}
			r.left = nbCellsLeft(left)
			break
		}
	}
	if r.wrongType > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputRepaired,
			count(r.wrongType, "value has", "values have")+" a JSON type the notebook format does not allow where it stands, and "+
				count(r.wrongType, "was", "were")+" not read")
	}
	if r.attachOver > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; "+count(r.attachOver, "attached image has", "attached images have")+" no data")
	}
	if r.decodeCut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; the rest of the notebook was not read"+r.left)
	}
	part.Blocks = r.agg.Blocks
	e.report(&part, walked{res: r.agg, caps: r.caps, limited: r.limited, left: r.left, replaced: r.replaced}, r.decodeCut, r.pc, t)
	doc.Parts = append(doc.Parts, part)
	if len(t.stored) > 0 && opts.Parts == (gofastcore.PartRange{}) {
		stored := gofastcore.Part{Kind: gofastcore.PartStored, Number: 2, Blocks: t.stored}
		stored.Finish()
		doc.Parts = append(doc.Parts, stored)
	}
	t.finish(doc)
	return doc, nil
}

// nbReader reads the cells of a notebook into blocks.
type nbReader struct {
	e      *Extractor
	ctx    context.Context
	opts   *gofastcore.Options
	text   *gofastcore.TextBudget
	decode *gofastcore.DecodeBudget
	pc     pageConfig
	v3     bool   // version 3: worksheets, input, prompt numbers
	lang   string // the language of the notebook's code

	agg       *blocks.Result // what the walks of the cells found
	caps      dom.Caps
	replaced  int // character references read as U+FFFD (walked.replaced)
	limited   bool
	decodeCut bool   // the decode budget ran out
	left      string // the cells not read, for the warning
	// wrongType counts the values of the wrong JSON type, not read.
	wrongType int
	// attachOver counts the attached images whose data passed the decode
	// budget.
	attachOver int
}

// decodeJSON decodes raw into v and reports whether it could. A value of
// the wrong type is counted.
func (r *nbReader) decodeJSON(raw json.RawMessage, v any) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	if err := json.Unmarshal(raw, v); err != nil {
		r.wrongType++
		return false
	}
	return true
}

// object decodes a JSON object; a missing or null one is empty.
func (r *nbReader) object(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	r.decodeJSON(raw, &m)
	return m
}

// str decodes a JSON string; a missing or null one is empty.
func (r *nbReader) str(raw json.RawMessage) string {
	var s string
	r.decodeJSON(raw, &s)
	return s
}

// multiline decodes the notebook format's multiline string: a string, or
// a list of strings to be joined.
func (r *nbReader) multiline(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '[' {
		var lines []string
		if r.decodeJSON(raw, &lines) {
			return strings.Join(lines, "")
		}
		return ""
	}
	return r.str(raw)
}

// flag decodes a JSON boolean; anything else is false.
func flag(raw json.RawMessage) bool { return string(raw) == "true" }

// nbInt decodes a JSON integer; anything else is 0.
func nbInt(raw json.RawMessage) int {
	n, _ := nbNumber(raw)
	return n
}

// nbNumber decodes an integer, as a JSON number or a string, and reports
// whether there was one.
func nbNumber(raw json.RawMessage) (int, bool) {
	s := string(raw)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// nbInvalidUTF8 counts the sequences of src that are not valid UTF-8,
// which a JSON decoder reads as U+FFFD.
func nbInvalidUTF8(src []byte) int {
	if utf8.Valid(src) {
		return 0
	}
	n := 0
	for len(src) > 0 {
		c, size := utf8.DecodeRune(src)
		if c == utf8.RuneError && size == 1 {
			n++
		}
		src = src[size:]
	}
	return n
}

// metaText cleans a metadata value and cuts it at blocks.MaxMeta, counting
// the cut in t.
func metaText(s string, t *tally) string {
	if len(s) > blocks.MaxMeta {
		n := blocks.MaxMeta
		for n > 0 && s[n]&0xC0 == 0x80 {
			n--
		}
		s = s[:n]
		t.meta.values++
	}
	return strings.TrimSpace(string(cleanAppend(nil, []byte(strings.ToValidUTF8(s, "\uFFFD")), &t.controls)))
}

// metadata reads the notebook's metadata: its title and authors, and the
// kernel and language its code is for, which also sets r.lang.
func (r *nbReader) metadata(raw json.RawMessage, t *tally) gofastcore.Metadata {
	var m gofastcore.Metadata
	meta := r.object(raw)
	if meta == nil {
		return m
	}
	m.Title = metaText(r.str(meta["title"]), t)
	var authors []json.RawMessage
	if meta["authors"] != nil && r.decodeJSON(meta["authors"], &authors) {
		for _, a := range authors {
			name := ""
			if len(a) > 0 && a[0] == '"' {
				name = r.str(a)
			} else {
				name = r.str(r.object(a)["name"])
			}
			if name = metaText(name, t); name == "" {
				continue
			}
			if len(m.Authors) == blocks.MaxMetaList {
				t.meta.authors++
				continue
			}
			m.Authors = append(m.Authors, name)
		}
	}
	extra := map[string]string{}
	kernel := r.object(meta["kernelspec"])
	info := r.object(meta["language_info"])
	if k := metaText(r.str(kernel["display_name"]), t); k != "" {
		extra["kernel"] = k
	} else if k := metaText(r.str(kernel["name"]), t); k != "" {
		extra["kernel"] = k
	}
	r.lang = metaText(r.str(info["name"]), t)
	if r.lang == "" {
		r.lang = metaText(r.str(kernel["language"]), t)
	}
	if r.lang == "" {
		r.lang = metaText(r.str(meta["language"]), t) // version 3
	}
	if r.lang != "" {
		extra["language"] = r.lang
	}
	if v := metaText(r.str(info["version"]), t); v != "" {
		extra["language_version"] = v
	}
	if len(extra) > 0 {
		m.Extra = extra
	}
	return m
}

// fragment is one piece of a cell read on its own: its HTML, and what to
// set on the blocks it gives.
type fragment struct {
	html   []byte
	source func(int) int64
	lang   string // Attrs[AttrLanguage] of its code blocks
	output string // Attrs[AttrOutput]
	latex  bool   // its code block is a LaTeX formula
	hidden bool   // the notebook declares it collapsed
}

// cell reads the i-th cell. A cell that is not a JSON object is counted
// and left out.
func (r *nbReader) cell(i int, raw json.RawMessage) error {
	c := r.object(raw)
	if c == nil {
		return nil
	}
	meta := r.object(c["metadata"])
	jup := r.object(meta["jupyter"])
	sourceHidden := flag(jup["source_hidden"])
	outputsHidden := flag(jup["outputs_hidden"]) || flag(meta["collapsed"]) || flag(c["collapsed"])
	anchor := ""
	if id := r.str(c["id"]); id != "" && utf8.ValidString(id) && !strings.ContainsFunc(id, nbControl) {
		if len(id) > blocks.MaxAnchor {
			r.agg.AnchorsLeftOut++
		} else {
			anchor = id
		}
	}
	var att map[string]map[string]json.RawMessage
	for name, v := range r.object(c["attachments"]) {
		if bundle := r.object(v); bundle != nil {
			if att == nil {
				att = map[string]map[string]json.RawMessage{}
			}
			att[name] = bundle
		}
	}
	image := r.attachments(i, att)

	var frags []fragment
	kind := r.str(c["cell_type"])
	switch kind {
	case "markdown":
		f, err := r.markdown(r.multiline(c["source"]))
		if err != nil {
			return err
		}
		frags = append(frags, f)
	case "heading": // version 3
		level := min(max(nbInt(c["level"]), 1), 6)
		h := strconv.Itoa(level)
		frags = append(frags, fragment{html: []byte("<h" + h + ">" + html.EscapeString(r.multiline(c["source"])) + "</h" + h + ">")})
	case "code":
		code := r.multiline(c["source"])
		lang := r.lang
		if r.v3 {
			code = r.multiline(c["input"])
			if l := metaText(r.str(c["language"]), &tally{}); l != "" {
				lang = l
			}
		}
		frags = append(frags, pre(code, lang, ""))
	default:
		// A raw cell, or a kind of cell this version does not know: its
		// text as it is.
		frags = append(frags, pre(r.multiline(c["source"]), "", ""))
	}
	for j := range frags {
		frags[j].hidden = sourceHidden
	}
	if kind == "code" {
		var outs []json.RawMessage
		if c["outputs"] != nil {
			r.decodeJSON(c["outputs"], &outs)
		}
		of, err := r.outputs(outs)
		if err != nil {
			return err
		}
		for j := range of {
			of[j].hidden = outputsHidden
		}
		frags = append(frags, of...)
	}

	pc := r.pc
	pc.image = image
	for _, f := range frags {
		if !r.decode.Take(int64(len(f.html))) {
			r.decodeCut = true
			return nil
		}
		w, err := r.e.walk(r.ctx, f.html, f.source, pc)
		if err != nil {
			return err
		}
		for k := range w.res.Blocks {
			b := &w.res.Blocks[k]
			if b.Loc.Anchor == "" {
				b.Loc.Anchor = anchor
			}
			if b.Kind == gofastcore.KindCode && f.latex {
				// A formula's text has no space around it, as a code
				// block's may.
				b.Kind = gofastcore.KindFormula
				b.Text = strings.TrimSpace(b.Text)
				b.Spans = nil
				delete(b.Attrs, gofastcore.AttrLanguage)
				setAttr(b, gofastcore.AttrNotation, gofastcore.NotationLaTeX)
			} else if b.Kind == gofastcore.KindCode && f.lang != "" {
				if len(f.lang) > blocks.MaxLang {
					w.res.LanguagesLeftOut++
				} else {
					setAttr(b, gofastcore.AttrLanguage, f.lang)
				}
			}
			if f.output != "" {
				setAttr(b, AttrOutput, f.output)
			}
			if f.hidden {
				hideBlock(b)
			}
		}
		if f.latex {
			w.res.Blocks = slices.DeleteFunc(w.res.Blocks, func(b gofastcore.Block) bool {
				return b.Kind == gofastcore.KindFormula && b.Text == ""
			})
		}
		r.merge(w)
		if w.res.Cut {
			return nil
		}
	}
	return nil
}

// setAttr sets a key of b.Attrs.
func setAttr(b *gofastcore.Block, k, v string) {
	if b.Attrs == nil {
		b.Attrs = map[string]string{}
	}
	b.Attrs[k] = v
}

// nbControl reports a control character, which no anchor has.
func nbControl(c rune) bool { return c < 0x20 || c == 0x7F || c >= 0x80 && c < 0xA0 }

// hideBlock marks a whole block hidden, as the notebook declares its
// cell's input or outputs collapsed. The reason then applies to the whole
// block, so it is taken off the spans and table regions that had it, and
// those that are left marking nothing are dropped.
func hideBlock(b *gofastcore.Block) {
	b.Display |= gofastcore.DisplayHidden
	b.Spans = slices.DeleteFunc(b.Spans, func(sp gofastcore.Span) bool {
		sp.Display &^= gofastcore.DisplayHidden
		return sp.Display == 0 && sp.Style == 0 && sp.Href == ""
	})
	for k := range b.Spans {
		b.Spans[k].Display &^= gofastcore.DisplayHidden
	}
	if b.Table != nil {
		b.Table.Display = slices.DeleteFunc(b.Table.Display, func(d gofastcore.DisplayCells) bool {
			return d.Display&^gofastcore.DisplayHidden == 0
		})
		for k := range b.Table.Display {
			b.Table.Display[k].Display &^= gofastcore.DisplayHidden
		}
		if len(b.Table.Display) == 0 {
			b.Table.Display = nil
		}
	}
	if len(b.Spans) == 0 {
		b.Spans = nil
	}
}

// pre is a fragment of verbatim text: a cell's code, the text of an
// output.
func pre(s, lang, output string) fragment {
	return fragment{html: []byte("<pre>" + html.EscapeString(s) + "</pre>"), lang: lang, output: output}
}

// markdown renders a Markdown cell or output to HTML within the decode
// budget.
func (r *nbReader) markdown(s string) (fragment, error) {
	d, err := md.Parse(r.ctx, s)
	if err != nil {
		return fragment{}, err
	}
	h := d.Render(int(min(max(r.decode.Left(), 0), math.MaxInt32)))
	if h.Cut {
		r.decodeCut = true
	}
	r.replaced += h.Replaced
	return fragment{html: h.B}, nil
}

// attachments resolves the images a Markdown cell names as
// "attachment:name", from the cell's attachments.
func (r *nbReader) attachments(i int, att map[string]map[string]json.RawMessage) func(string) (blocks.ImageRef, bool) {
	if len(att) == 0 {
		return nil
	}
	type found struct {
		ref blocks.ImageRef
		ok  bool
	}
	seen := map[string]found{} // each attachment is read once
	return func(src string) (blocks.ImageRef, bool) {
		name, ok := strings.CutPrefix(src, "attachment:")
		if !ok {
			return blocks.ImageRef{}, false
		}
		if _, ok := att[name]; !ok {
			if u, err := url.PathUnescape(name); err == nil {
				name = u
			}
		}
		if f, ok := seen[name]; ok {
			return f.ref, f.ok
		}
		bundle := att[name]
		for _, mime := range nbRepresentations {
			if !strings.HasPrefix(mime, "image/") || bundle[mime] == nil {
				continue
			}
			value := r.multiline(bundle[mime])
			ref := blocks.ImageRef{ID: attachmentID(i, name), MIME: mime, Data: func() ([]byte, bool) {
				return r.imageBytes(mime, value)
			}}
			seen[name] = found{ref, true}
			return ref, true
		}
		seen[name] = found{}
		return blocks.ImageRef{}, false
	}
}

// attachmentID is the Image.ID of the attachment of the i-th cell with
// this name: "cell-N/attachment:name", or with a name of control
// characters or longer than blocks.MaxAnchor, the start of the SHA-256 of
// the name in its place.
func attachmentID(i int, name string) string {
	if len(name) > blocks.MaxAnchor || !utf8.ValidString(name) || strings.ContainsFunc(name, nbControl) {
		sum := sha256.Sum256([]byte(name))
		name = "sha256:" + hex.EncodeToString(sum[:16])
	}
	return fmt.Sprintf("cell-%d/attachment:%s", i+1, name)
}

// imageBytes decodes an image of the notebook within the decode budget:
// base64, or the text of an SVG image.
func (r *nbReader) imageBytes(mime, value string) ([]byte, bool) {
	if mime == "image/svg+xml" {
		if !r.decode.Take(int64(len(value))) {
			r.attachOver++
			return nil, false
		}
		return []byte(value), true
	}
	clean := strings.Map(func(c rune) rune {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
			return -1
		}
		return c
	}, value)
	clean = strings.TrimRight(clean, "=")
	if !r.decode.Take(int64(base64.RawStdEncoding.DecodedLen(len(clean)))) {
		r.attachOver++
		return nil, false
	}
	d, err := base64.RawStdEncoding.DecodeString(clean)
	return d, err == nil
}

// outputs makes the fragments of a code cell's outputs. Consecutive
// stream outputs of one stream are joined, as a notebook shows them.
func (r *nbReader) outputs(outs []json.RawMessage) ([]fragment, error) {
	var frags []fragment
	stream := ""
	var streamText strings.Builder
	flush := func() {
		if stream != "" {
			frags = append(frags, pre(terminal(streamText.String()), "", stream))
		}
		stream = ""
		streamText.Reset()
	}
	for _, raw := range outs {
		o := r.object(raw)
		if o == nil {
			continue
		}
		typ := r.str(o["output_type"])
		switch typ {
		case "stream":
			name := r.str(o["name"])
			if r.v3 {
				name = r.str(o["stream"])
			}
			if name != OutputStderr {
				name = OutputStdout
			}
			if name != stream {
				flush()
				stream = name
			}
			streamText.WriteString(r.multiline(o["text"]))
			continue
		}
		flush()
		switch typ {
		case "error", "pyerr":
			var tb []string
			text := ""
			if o["traceback"] != nil && r.decodeJSON(o["traceback"], &tb) && len(tb) > 0 {
				text = strings.Join(tb, "\n")
			} else {
				text = r.str(o["ename"]) + ": " + r.str(o["evalue"])
			}
			frags = append(frags, pre(terminal(text), "", OutputError))
		default:
			// execute_result, display_data, version 3's pyout, and kinds
			// this version does not know that carry representations.
			name := OutputDisplayData
			if typ == "execute_result" || typ == "pyout" {
				name = OutputExecuteResult
			}
			bundle := o
			if !r.v3 {
				bundle = r.object(o["data"])
			} else {
				bundle = map[string]json.RawMessage{}
				for k, v := range o {
					if mime, ok := nbV3Keys[k]; ok {
						bundle[mime] = v
					}
				}
			}
			f, ok, err := r.representation(bundle, name)
			if err != nil {
				return nil, err
			}
			if ok {
				frags = append(frags, f)
			}
		}
	}
	flush()
	return frags, nil
}

// representation makes the fragment of the representation of an output
// a notebook shows.
func (r *nbReader) representation(bundle map[string]json.RawMessage, output string) (fragment, bool, error) {
	for _, mime := range nbRepresentations {
		raw, ok := bundle[mime]
		if !ok {
			continue
		}
		value := r.multiline(raw)
		switch {
		case mime == "text/html":
			return fragment{html: []byte(value), output: output}, true, nil
		case mime == "text/markdown":
			f, err := r.markdown(value)
			f.output = output
			return f, true, err
		case mime == "text/latex":
			f := pre(value, "", output)
			f.latex = true
			return f, true, nil
		case mime == "text/plain":
			return pre(terminal(value), "", output), true, nil
		default:
			data := value
			if mime == "image/svg+xml" {
				data = base64.StdEncoding.EncodeToString([]byte(value))
			}
			return fragment{html: []byte(`<img src="data:` + mime + `;base64,` + html.EscapeString(data) + `">`), output: output}, true, nil
		}
	}
	return fragment{}, false, nil
}

// merge adds what the walk of a fragment found to r.agg.
func (r *nbReader) merge(w walked) {
	a, s := r.agg, w.res
	a.Blocks = append(a.Blocks, s.Blocks...)
	a.Cut = a.Cut || s.Cut
	a.Active += s.Active
	a.External += s.External
	a.ImageData += s.ImageData
	a.ImageBudget += s.ImageBudget
	a.AnchorsLeftOut += s.AnchorsLeftOut
	a.LanguagesLeftOut += s.LanguagesLeftOut
	a.Controls += s.Controls
	a.Images += s.Images
	a.Stylesheets += s.Stylesheets
	a.Stored = append(a.Stored, s.Stored...)
	a.FrameDepth = a.FrameDepth || s.FrameDepth
	a.FrameFlattened = a.FrameFlattened || s.FrameFlattened
	a.FrameDecode = a.FrameDecode || s.FrameDecode
	a.FrameCaps.Add(s.FrameCaps)
	a.FrameReplaced += s.FrameReplaced
	r.caps.Add(w.caps)
	r.replaced += w.replaced
	r.limited = r.limited || w.limited
}

// terminal reads text written to a terminal as JupyterLab shows it:
// escape sequences that set colours and move the cursor are left out (a
// CSI sequence ends at its final byte, and one with none before the line
// ends is not a sequence), a backspace takes out the character before it
// on its line, and text after a carriage return writes over the line
// from its start. Each step is linear in the text.
func terminal(s string) string {
	if !strings.ContainsAny(s, "\x1b\r\b") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != 0x1b {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[': // CSI: parameters and intermediates, then a final byte
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) && s[j] != '\n' {
				j++
			}
			if j < len(s) && s[j] != '\n' {
				i = j + 1
			} else {
				i++ // not a sequence: the escape alone is left out
			}
		case ']': // OSC: to BEL or ST, on its line
			j, end := i+2, -1
			for j < len(s) && s[j] != '\n' {
				if s[j] == 0x07 {
					end = j + 1
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					end = j + 2
					break
				}
				j++
			}
			if end >= 0 {
				i = end
			} else {
				i++ // not a sequence: the escape alone is left out
			}
		case '\n', '\r', 0x1b:
			// Not a sequence: the escape alone is left out, and what
			// follows is read as itself.
			i++
		default: // a two-character sequence
			_, size := utf8.DecodeRuneInString(s[i+1:])
			i += 1 + size
		}
	}
	s = b.String()
	if strings.Contains(s, "\b") {
		// Backspaces first, as JupyterLab: each takes out the character
		// before it, unless that is a line feed.
		out := make([]byte, 0, len(s))
		for i := 0; i < len(s); i++ {
			if s[i] != '\b' {
				out = append(out, s[i])
				continue
			}
			if n := len(out); n > 0 && out[n-1] != '\n' {
				_, size := utf8.DecodeLastRune(out)
				out = out[:n-size]
			}
		}
		s = string(out)
	}
	if !strings.Contains(s, "\r") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var out strings.Builder
	out.Grow(len(s))
	var line []rune
	pos := 0
	for _, c := range s {
		switch c {
		case '\n':
			out.WriteString(string(line))
			out.WriteByte('\n')
			line, pos = line[:0], 0
		case '\r':
			pos = 0
		default:
			if pos < len(line) {
				line[pos] = c
			} else {
				line = append(line, c)
			}
			pos++
		}
	}
	out.WriteString(string(line))
	return out.String()
}
