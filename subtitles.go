package gofasthtml

import (
	"bytes"
	"context"
	"fmt"
	"strings"

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
	enc := charset.SniffText(src, label)
	if err := t.sniffed(&enc, false); err != nil {
		return nil, err
	}
	decoded, m, invalid, cut := charset.Decode(src, enc, max(decode.Left(), 0))
	decode.Take(int64(len(decoded)))
	if invalid > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs,
			fmt.Sprintf("%d byte sequences are not valid %s and appear as U+FFFD", invalid, enc.Enc))
	}
	if cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("the limit on decoded bytes was reached; the rest of the input was not read (%d bytes)",
				int64(len(src))-m.Source(len(decoded))))
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
		for n > 0 && !utf8RuneStart(s[n]) {
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
		off := m.Source(it.Start)
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: it.Text,
			Loc: gofastcore.Location{Offset: off, Length: m.Source(it.End) - off}}
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

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

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
			for n > 0 && !utf8RuneStart(v[n]) {
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
