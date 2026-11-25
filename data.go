package gofasthtml

import (
	"bytes"
	"context"
	"fmt"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/charset"
)

// A JSON or YAML document longer than this is split at line ends into
// pieces of about this many lines and bytes, the later pieces Continued.
// A line is never cut.
const (
	dataBlockLines = 1000
	dataBlockBytes = 256 << 10
)

// dataLine is one line of a JSON or YAML file.
type dataLine struct {
	text       []byte // without its line end, control characters dropped
	start, end int    // in the decoded text, its line end included
	blank      bool   // nothing but white space
	controls   int    // control characters dropped
}

// dataText reads a JSON or YAML file into one part of kind body: by its
// structure (jsonStructure, yamlStructure), or, when it is neither, as
// lines: its text, verbatim but for control characters, in code blocks
// whose language is the format. Read as lines, a YAML file of several
// documents has a block (or a run of pieces) for each; the blank lines at
// the start and end of a document are left out, and the pieces of a
// document joined with line feeds give it back.
func (e *Extractor) dataText(ctx context.Context, doc *gofastcore.Document, src []byte, format string, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget) (*gofastcore.Document, error) {
	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1}
	t := &tally{}
	enc := charset.SniffText(src, e.Charset)
	if format == "json" && enc.Unknown {
		// JSON is UTF-8 (RFC 8259, 8.1): text whose encoding is not
		// certain is read as UTF-8, its invalid bytes U+FFFD.
		enc = charset.Result{Enc: charset.UTF8, From: charset.FromDetected}
	}
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

	// JSON and YAML are cut by their structure; text that is neither is
	// read as lines.
	var (
		s      *dSplitter
		synErr string
	)
	switch format {
	case "json":
		sp, se, err := e.jsonStructure(ctx, decoded, m, opts.Name, &part, text)
		if err != nil {
			return nil, err
		}
		s = sp
		if se != nil {
			synErr = syntaxPlace(decoded, se.Offset) + ": " + se.Msg
		}
	case "yaml":
		sp, se, err := e.yamlStructure(ctx, decoded, m, &part, text)
		if err != nil {
			return nil, err
		}
		s = sp
		if se != nil {
			synErr = syntaxPlace(decoded, se.Offset) + ": " + se.Msg
		}
	}
	if synErr != "" {
		if doc.Metadata.Extra == nil {
			doc.Metadata.Extra = map[string]string{}
		}
		msg, n := cleanMeta(synErr) // the message may quote the input
		t.controls += n
		doc.Metadata.Extra[format+".syntax_error"] = msg
	}
	if s != nil {
		if s.spent {
			part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				textCutMessage(part.Kind)+" ("+count(dataLinesFrom(decoded, s.notRead()), "line", "lines")+" not read)")
		}
		if s.controls > 0 {
			part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, controlsMessage(s.controls, "the text"))
		}
		if len(part.Blocks) == 0 && !cut && !s.spent {
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "no text")
		}
		part.Finish()
		doc.Parts = append(doc.Parts, part)
		t.finish(doc)
		return doc, nil
	}

	var (
		pending   []dataLine // the lines of the block being made
		size      int        // their bytes
		continued bool       // the block continues the one before it
		emitted   int        // past the last line of the blocks made, in decoded
		spent     bool       // the text budget ran out
		controls  int        // dropped from the lines read: in blocks made, or blank and skipped
	)
	// flush makes a block of the pending lines, but for blank lines at
	// its end when the document ends there (last).
	flush := func(last bool) bool {
		n := len(pending)
		if last {
			for n > 0 && pending[n-1].blank {
				n--
			}
		}
		if n == 0 {
			for _, l := range pending {
				controls += l.controls
			}
			pending, size = pending[:0], 0
			return true
		}
		lines := pending[:n]
		var b []byte
		for i, l := range lines {
			if i > 0 {
				b = append(b, '\n')
			}
			b = append(b, l.text...)
		}
		if !text.Take(gofastcore.BlockCost + len(b)) {
			return false
		}
		first, end := lines[0].start, lines[n-1].end
		off := m.Source(first)
		part.Blocks = append(part.Blocks, gofastcore.Block{Kind: gofastcore.KindCode, Text: string(b),
			Continued: continued, Attrs: map[string]string{gofastcore.AttrLanguage: format},
			Loc: gofastcore.Location{Offset: off, Length: m.Source(end) - off}})
		emitted = pending[len(pending)-1].end
		for _, l := range pending {
			controls += l.controls
		}
		continued = !last
		pending, size = pending[:0], 0
		return true
	}
	// endDocument ends a YAML document before the line being read.
	endDocument := func() bool {
		ok := flush(true)
		continued = false
		return ok
	}
	ended := false // a YAML "..." line ended the document
	for pos, n := 0, 0; pos < len(decoded); n++ {
		if n&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		l, next := dataNextLine(decoded, pos)
		pos = next
		if format == "yaml" {
			// A document starts at "---", and after "..." at the next
			// line with text (its directives, %YAML and %TAG, first).
			if yamlMarker(l.text, "---") || ended && !l.blank {
				if !endDocument() {
					spent = true
					break
				}
			}
			if !l.blank {
				ended = false
			}
		}
		if len(pending) == 0 && l.blank {
			emitted = l.end // blank lines before a block are in none
			controls += l.controls
			continue
		}
		// A long document is split at a line end, after a line with
		// text, so that no piece is only blank lines.
		if len(pending) > 0 && !pending[len(pending)-1].blank &&
			(len(pending) >= dataBlockLines || size+len(l.text) > dataBlockBytes) {
			if !flush(false) {
				spent = true
				break
			}
		}
		pending = append(pending, l)
		size += len(l.text) + 1
		if format == "yaml" && yamlMarker(l.text, "...") {
			ended = true
		}
	}
	if !spent && !flush(true) {
		spent = true
	}
	if spent {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			textCutMessage(part.Kind)+" ("+count(dataLinesFrom(decoded, emitted), "line", "lines")+" not read)")
	}
	if controls > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, controlsMessage(controls, "the text"))
	}
	if len(part.Blocks) == 0 && !cut && !spent {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "no text")
	}
	part.Finish()
	doc.Parts = append(doc.Parts, part)
	t.finish(doc)
	return doc, nil
}

// dataNextLine reads the line at pos: it ends at LF, CR LF or a lone CR.
// It returns the line and where the next begins.
func dataNextLine(b []byte, pos int) (dataLine, int) {
	i := pos
	for i < len(b) && b[i] != '\n' && b[i] != '\r' {
		i++
	}
	next := i
	switch {
	case i+1 < len(b) && b[i] == '\r' && b[i+1] == '\n':
		next = i + 2
	case i < len(b):
		next = i + 1
	}
	controls := 0
	text := dropControls(nil, b[pos:i], &controls)
	return dataLine{text: text, start: pos, end: next, blank: len(bytes.TrimSpace(text)) == 0, controls: controls}, next
}

// yamlMarker reports whether a line is a YAML document marker ("---" or
// "..."): the three characters, then the line end, a space or a tab.
func yamlMarker(line []byte, marker string) bool {
	if !bytes.HasPrefix(line, []byte(marker)) {
		return false
	}
	return len(line) == 3 || line[3] == ' ' || line[3] == '\t'
}

// dropControls appends src to dst without its control characters other
// than tab: verbatim otherwise, as code is. src is valid UTF-8, a line
// without its end. The characters dropped are counted in *controls.
func dropControls(dst, src []byte, controls *int) []byte {
	from := 0
	for i := 0; i < len(src); {
		c := src[i]
		n := 0
		switch {
		case c < 0x20 && c != '\t', c == 0x7F:
			n = 1
		case c == 0xC2 && i+1 < len(src) && src[i+1] >= 0x80 && src[i+1] <= 0x9F: // C1 controls
			n = 2
		}
		if n == 0 {
			i++
			continue
		}
		*controls++
		dst = append(dst, src[from:i]...)
		i += n
		from = i
	}
	if from == 0 && dst == nil {
		return src // nothing dropped: the line as it is
	}
	return append(dst, src[from:]...)
}

// dataLinesFrom counts the lines of b from pos to the end: every line,
// blank or not, a last one without a line end included.
func dataLinesFrom(b []byte, pos int) int {
	n := 0
	for pos < len(b) {
		_, pos = dataNextLine(b, pos)
		n++
	}
	return n
}
