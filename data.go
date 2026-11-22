package gofasthtml

import (
	"bytes"
	"context"
	"fmt"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/charset"
)

// A JSON or YAML file is read as text: code blocks of its lines as they
// are written, a long one split into pieces of at most dataBlockLines
// lines and about dataBlockBytes bytes, the later pieces Continued.
const (
	dataBlockLines = 1000
	dataBlockBytes = 256 << 10
)

// dataText reads a JSON or YAML file into one part of kind body: its text,
// verbatim, in code blocks whose language is the format. A YAML file of
// several documents has a block (or a run of them) for each, the "---"
// line that starts a document at its head. Nothing is parsed: data is
// text here, as a person reading the file sees it.
func (e *Extractor) dataText(ctx context.Context, doc *gofastcore.Document, src []byte, format string, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget) (*gofastcore.Document, error) {
	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1}
	t := &tally{}
	enc := charset.SniffText(src, e.Charset)
	if enc.Transport != "" {
		t.transport = blocksLabel(enc.Transport)
	}
	decoded, m, invalid, cut := charset.Decode(src, enc, max(decode.Left(), 0))
	decode.Take(int64(len(decoded)))
	if invalid > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs,
			fmt.Sprintf("%d byte sequences are not valid %s and appear as U+FFFD", invalid, enc.Enc))
	}
	if cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; the rest of the input was not read")
	}

	var (
		buf       []byte // the block being made
		lines     int    // its lines
		start     = -1   // its first byte in decoded
		end       int    // past its last line with text, in decoded
		keep      int    // the length of buf to its last line with text
		continued bool   // it continues the block before it
		spent     bool   // the text budget ran out
		left      int    // lines not read then
	)
	flush := func(next bool) bool {
		body := buf[:keep]
		if len(body) > 0 {
			if !text.Take(gofastcore.BlockCost + len(body)) {
				return false
			}
			off := m.Source(start)
			part.Blocks = append(part.Blocks, gofastcore.Block{Kind: gofastcore.KindCode, Text: string(body),
				Continued: continued, Attrs: map[string]string{gofastcore.AttrLanguage: format},
				Loc: gofastcore.Location{Offset: off, Length: m.Source(end) - off}})
			continued = next
		}
		buf, lines, start, keep = buf[:0], 0, -1, 0
		return true
	}
	// add adds a piece of a line, at offset off of decoded, to the block,
	// beginning another first when the block is full.
	add := func(piece []byte, off int) bool {
		if len(buf) > 0 && (len(buf)+len(piece) > dataBlockBytes || lines >= dataBlockLines) {
			if !flush(true) {
				return false
			}
		}
		blank := len(bytes.TrimSpace(piece)) == 0
		if start < 0 && blank {
			return true // blank lines before a block are not in it
		}
		if start < 0 {
			start = off
		}
		nl := bytes.HasSuffix(piece, []byte("\n"))
		buf = cleanAppend(buf, bytes.TrimSuffix(bytes.TrimSuffix(piece, []byte("\n")), []byte("\r")))
		if !blank {
			keep, end = len(buf), off+len(piece)
		}
		if nl {
			buf = append(buf, '\n')
		}
		lines++
		return true
	}
	for pos, n := 0, 0; pos < len(decoded) && !spent; n++ {
		if n&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		next := len(decoded)
		if i := bytes.IndexByte(decoded[pos:], '\n'); i >= 0 {
			next = pos + i + 1
		}
		line := decoded[pos:next]
		// A YAML document begins at a line "---" (or "--- " and more).
		if format == "yaml" && start >= 0 && (bytes.Equal(bytes.TrimRight(line, "\r\n"), []byte("---")) || bytes.HasPrefix(line, []byte("--- "))) {
			if !flush(false) {
				spent = true
			}
			continued = false
		}
		// A line too long for one piece is cut at a character boundary.
		for off := pos; off < next && !spent; {
			k := min(next-off, dataBlockBytes)
			for k < next-off && k > 0 && decoded[off+k]&0xC0 == 0x80 {
				k--
			}
			if !add(decoded[off:off+k], off) {
				spent = true
			}
			off += k
		}
		if spent {
			left = linesFrom(decoded, start, pos)
			break
		}
		pos = next
	}
	if !spent && !flush(false) {
		spent, left = true, linesFrom(decoded, start, len(decoded))
	}
	if spent {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			textCutMessage(part.Kind)+" ("+count(left, "line", "lines")+" not read)")
	}
	if len(part.Blocks) == 0 && !cut && !spent {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "no text")
	}
	part.Finish()
	doc.Parts = append(doc.Parts, part)
	t.finish(doc)
	return doc, nil
}

// linesFrom counts the lines not read when the budget ran out: from the
// start of the block not made (start, or pos when none was begun) to the
// end. A line cut into pieces counts once.
func linesFrom(decoded []byte, start, pos int) int {
	from := pos
	if start >= 0 {
		from = start
	}
	for from > 0 && decoded[from-1] != '\n' {
		from-- // the start of the line the piece is in
	}
	rest := decoded[from:]
	n := bytes.Count(rest, []byte("\n"))
	if len(rest) > 0 && rest[len(rest)-1] != '\n' {
		n++
	}
	return n
}
