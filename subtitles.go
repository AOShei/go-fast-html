package gofasthtml

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/blocks"
	"github.com/AOShei/go-fast-html/internal/charset"
	"github.com/AOShei/go-fast-html/internal/subtitle"
)

// maxSubtitleAttr bounds a cue's attribute values (timings as written,
// settings, voices): past it a value is cut, with limit.exceeded.
const maxSubtitleAttr = 4 << 10

// isWebVTT reports whether src begins, after a byte order mark, with the
// WebVTT signature: WEBVTT followed by a space, a tab, a line end or the
// end of the input.
func isWebVTT(src []byte) bool {
	head := src[:min(len(src), 32)]
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		head = head[3:]
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}), bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		head = bytes.ReplaceAll(head[2:], []byte{0}, nil)
	}
	if !bytes.HasPrefix(head, []byte("WEBVTT")) {
		return false
	}
	return len(head) == 6 || strings.IndexByte(" \t\r\n", head[6]) >= 0
}

// subtitleCounts counts what reading the cues found, for the warnings.
type subtitleCounts struct {
	unparsed, empty, stray, controls, cut, anchors, attrs int
}

// subtitles reads SubRip (srt) or WebVTT (vtt) into one part of kind
// body: a paragraph per cue, in file order, with its timings in Attrs.
// WebVTT NOTE blocks are notes stored as markup comments where they
// stand; STYLE blocks go to the stored part.
func (e *Extractor) subtitles(ctx context.Context, doc *gofastcore.Document, src []byte, format string, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget) (*gofastcore.Document, error) {
	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1}
	t := &tally{}
	label := e.Charset
	if format == "vtt" {
		label = "" // UTF-8 by the spec: no transport encoding
	}
	var (
		decoded []byte
		source  func(int) int64
		invalid int
		cut     bool
		encName string
	)
	if be, ok := utf32BOM(src); ok {
		decoded, source, invalid, cut = decodeUTF32(src, be, max(decode.Left(), 0))
		encName = map[bool]string{true: "utf-32be", false: "utf-32le"}[be]
	} else {
		enc := charset.SniffText(src, label)
		// Text whose encoding is not certain is read as windows-1252,
		// with text.encoding_detected, as an EPUB chapter is: a file of
		// cues is not refused for the letters of its text.
		if err := t.sniffed(&enc, true); err != nil {
			return nil, err
		}
		var m *charset.Map
		decoded, m, invalid, cut = charset.Decode(src, enc, max(decode.Left(), 0))
		source, encName = m.Source, enc.Enc.String()
	}
	decode.Take(int64(len(decoded)))
	if invalid > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs,
			fmt.Sprintf("%d byte sequences are not valid %s and appear as U+FFFD", invalid, encName))
	}
	if cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("the limit on decoded bytes was reached; the rest of the input was not read (%d bytes)",
				int64(len(src))-source(len(decoded))))
	}

	var (
		c      subtitleCounts
		stored []gofastcore.Block
		spent  bool
	)
	attr := func(s string) string {
		if len(s) <= maxSubtitleAttr {
			return s
		}
		c.attrs++
		n := maxSubtitleAttr
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		return s[:n]
	}
	yield := func(it *subtitle.Item) bool {
		if it.Kind == subtitle.Cue && !it.TimingOK {
			c.unparsed++
		}
		if it.Text == "" {
			if it.Kind == subtitle.Cue {
				c.empty++
			}
			c.controls += it.Controls
			return true
		}
		off := source(it.Start)
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: it.Text,
			Loc: gofastcore.Location{Offset: off, Length: source(it.End) - off}}
		switch it.Kind {
		case subtitle.Cue:
			b.Attrs = map[string]string{"start": attr(it.Begin), "end": attr(it.Finish)}
			if it.Settings != "" {
				b.Attrs["settings"] = attr(it.Settings)
			}
			if len(it.Voices) > 0 {
				b.Attrs["voice"] = attr(strings.Join(it.Voices, ", "))
			}
			if opts.Spans {
				b.Spans = it.Spans
			}
		case subtitle.Note:
			b.Kind = gofastcore.KindNote
			b.Attrs = map[string]string{gofastcore.AttrStored: gofastcore.StoredMarkupComment}
		case subtitle.Style:
			b.Kind = gofastcore.KindCode
			b.Attrs = map[string]string{gofastcore.AttrStored: gofastcore.StoredStylesheet, gofastcore.AttrLanguage: "css"}
		case subtitle.Stray:
			c.stray++
		}
		if it.ID != "" {
			if len(it.ID) <= blocks.MaxAnchor {
				b.Loc.Anchor = it.ID
			} else {
				c.anchors++
			}
		}
		cost := gofastcore.BlockCost + len(b.Text) + len(b.Loc.Anchor)
		for _, v := range b.Attrs {
			cost += len(v)
		}
		if !text.Take(cost) {
			spent = true
			return false
		}
		c.controls += it.Controls
		if it.Cut {
			c.cut++
		}
		if it.Kind == subtitle.Style {
			if opts.Parts == (gofastcore.PartRange{}) {
				stored = append(stored, b)
			}
		} else {
			part.Blocks = append(part.Blocks, b)
		}
		return true
	}
	var err error
	if format == "vtt" {
		var h subtitle.Header
		h, err = subtitle.VTT(ctx, decoded, yield)
		if err == nil {
			vttMetadata(doc, &part, &h, t)
		}
	} else {
		err = subtitle.SRT(ctx, decoded, yield)
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.unparsed > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputRepaired,
			count(c.unparsed, "cue has timings", "cues have timings")+" that do not parse; their start and end are as written")
	}
	if c.empty > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputRepaired,
			count(c.empty, "cue has", "cues have")+" no text and no block")
	}
	if c.stray > 0 {
		msg := "text before the first cue is a paragraph of its own"
		if format == "vtt" {
			msg = count(c.stray, "block is", "blocks are") + " not a cue, NOTE, STYLE or REGION block; the text is given as paragraphs"
		}
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputRepaired, msg)
	}
	if c.cut > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			count(c.cut, "cue or block has", "cues or blocks have")+fmt.Sprintf(" more than %d MiB of text and %s cut there",
				subtitle.MaxText>>20, map[bool]string{true: "is", false: "are"}[c.cut == 1]))
	}
	if c.attrs > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			count(c.attrs, "cue attribute (timing, settings or voices) is", "cue attributes (timings, settings or voices) are")+
				fmt.Sprintf(" longer than %d KiB and cut there", maxSubtitleAttr>>10))
	}
	if c.anchors > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			count(c.anchors, "cue has an identifier", "cues have an identifier")+fmt.Sprintf(" longer than %d bytes, not used as Loc.Anchor", blocks.MaxAnchor))
	}
	if spent {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, textCutMessage(part.Kind))
	}
	if c.controls > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, controlsMessage(c.controls, "the text"))
	}
	if len(part.Blocks) == 0 && !cut && !spent {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "no text")
	}
	part.Finish()
	doc.Parts = append(doc.Parts, part)
	if len(stored) > 0 {
		sp := gofastcore.Part{Kind: gofastcore.PartStored, Number: 2, Blocks: stored}
		sp.Finish()
		doc.Parts = append(doc.Parts, sp)
	}
	t.finish(doc)
	return doc, nil
}

// utf32BOM reports whether src begins with a UTF-32 byte order mark, and
// whether it is big-endian. FF FE 00 00 is read as UTF-32 little-endian,
// not as UTF-16 beginning with U+0000.
func utf32BOM(src []byte) (be, ok bool) {
	switch {
	case bytes.HasPrefix(src, []byte{0, 0, 0xFE, 0xFF}):
		return true, true
	case bytes.HasPrefix(src, []byte{0xFF, 0xFE, 0, 0}):
		return false, true
	}
	return false, false
}

// decodeUTF32 decodes UTF-32 after its byte order mark into UTF-8, at
// most limit bytes of it. A unit that is not a Unicode scalar value, and
// bytes at the end too few for a unit, are U+FFFD and counted. source
// maps an offset of the text to the input's: each character comes from
// four bytes, so the map keeps the decoded offset of every 1024th
// character and counts on from there.
func decodeUTF32(src []byte, be bool, limit int64) (out []byte, source func(int) int64, invalid int, cut bool) {
	body := src[4:]
	var marks []int // decoded offset of characters 0, 1024, 2048, ...
	for i := 0; i < len(body); i += 4 {
		if (i/4)%1024 == 0 {
			marks = append(marks, len(out))
		}
		r := utf8.RuneError
		if i+4 <= len(body) {
			u := body[i : i+4]
			var v uint32
			if be {
				v = uint32(u[0])<<24 | uint32(u[1])<<16 | uint32(u[2])<<8 | uint32(u[3])
			} else {
				v = uint32(u[3])<<24 | uint32(u[2])<<16 | uint32(u[1])<<8 | uint32(u[0])
			}
			if v <= unicode.MaxRune && (v < 0xD800 || v > 0xDFFF) {
				r = rune(v)
			} else {
				invalid++
			}
		} else {
			invalid++
		}
		if int64(len(out)+utf8.RuneLen(r)) > limit {
			cut = true
			break
		}
		out = utf8.AppendRune(out, r)
	}
	source = func(dec int) int64 {
		k := sort.SearchInts(marks, dec+1) - 1 // the last mark at or before dec
		if k < 0 {
			return 4
		}
		d, ch := marks[k], k*1024
		for d < dec && d < len(out) {
			_, n := utf8.DecodeRune(out[d:])
			if d+n > dec {
				break
			}
			d += n
			ch++
		}
		return int64(4 + min(ch*4, len(body)))
	}
	return out, source, invalid, cut
}

// vttMetadata puts what a WebVTT header says in the document's metadata,
// and warns on part when the signature is missing.
func vttMetadata(doc *gofastcore.Document, part *gofastcore.Part, h *subtitle.Header, t *tally) {
	if !h.Signature {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputRepaired,
			"the file does not begin with the WebVTT signature; it was read as WebVTT all the same")
	}
	t.controls += h.Controls
	cuts, left := 0, 0
	set := func(k, v string) {
		if v == "" {
			return
		}
		if doc.Metadata.Extra == nil {
			doc.Metadata.Extra = map[string]string{}
		}
		old, ok := doc.Metadata.Extra[k]
		if !ok && len(doc.Metadata.Extra) >= blocks.MaxMetaList {
			left++
			return
		}
		if ok {
			v = old + "\n" + v
		}
		if len(v) > blocks.MaxMeta {
			cuts++
			n := blocks.MaxMeta
			for n > 0 && !utf8.RuneStart(v[n]) {
				n--
			}
			v = v[:n]
		}
		doc.Metadata.Extra[k] = v
	}
	set("vtt.header", h.Text)
	for _, f := range h.Fields {
		k := strings.ToLower(f.Key)
		set("vtt."+k, f.Value)
		if k == "language" && doc.Metadata.Language == "" && languageTag(f.Value) {
			doc.Metadata.Language = f.Value
		}
	}
	for _, r := range h.Regions {
		set("vtt.regions", r)
	}
	if cuts > 0 {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			count(cuts, "header value is", "header values are")+fmt.Sprintf(" longer than %d KiB and cut there", blocks.MaxMeta>>10))
	}
	if left > 0 {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("the header has more than %d keys; %s left out", blocks.MaxMetaList, count(left, "was", "were")))
	}
}

// languageTag reports whether s has the form of a BCP 47 language tag:
// subtags of one to eight letters or digits joined by hyphens, the first
// of two to eight letters.
func languageTag(s string) bool {
	if len(s) > blocks.MaxLang {
		return false
	}
	for i, sub := range strings.Split(s, "-") {
		if len(sub) < 1 || len(sub) > 8 || i == 0 && len(sub) < 2 {
			return false
		}
		for _, c := range []byte(sub) {
			letter := c|0x20 >= 'a' && c|0x20 <= 'z'
			if !letter && (i == 0 || c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
