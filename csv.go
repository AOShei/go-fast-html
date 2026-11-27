package gofasthtml

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/charset"
)

// Bounds of comma- and tab-separated input. Each protects against one
// hostile row: the text budget bounds the whole.
const (
	// MaxCSVColumns is the number of fields of a row that are read, as
	// many columns as a sheet of Excel has. The fields past it are not
	// read, with limit.exceeded.
	MaxCSVColumns = 16384
	// MaxCSVField is the length in bytes of the longest field read in
	// full. A longer field is cut there, at a character boundary, with
	// limit.exceeded.
	MaxCSVField = 1 << 20
)

const (
	// csvRowsPerBlock and csvCellsPerBlock bound a table block: a longer
	// table is split by rows, and the later pieces are Continued.
	csvRowsPerBlock  = 1000
	csvCellsPerBlock = 1 << 20
	// The delimiter of a csv file is sniffed from at most this much of
	// its text, and this many rows.
	csvSniffBytes = 64 << 10
	csvSniffRows  = 100
)

// How the delimiter was chosen, in Metadata.Extra["delimiter_source"].
const (
	delimDeclared = "declared" // an Excel "sep=" first line
	delimOption   = "option"   // Extractor.Delimiter
	delimFormat   = "format"   // tab for tsv
	delimDetected = "detected" // sniffed among , ; tab |, or the default ,
)

// csvDelimiters are the delimiters a csv file's rows are sniffed for, in
// the order a tie is decided.
var csvDelimiters = []rune{',', ';', '\t', '|'}

// usableDelimiter reports whether d can separate fields: not a quote,
// not a line end, a valid character.
func usableDelimiter(d rune) bool {
	return d != '"' && d != '\r' && d != '\n' && d != utf8.RuneError && utf8.ValidRune(d)
}

// csv reads comma- or tab-separated text into one part of kind sheet.
func (e *Extractor) csv(ctx context.Context, doc *gofastcore.Document, src []byte, format string, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget) (*gofastcore.Document, error) {
	part := gofastcore.Part{Kind: gofastcore.PartSheet, Number: 1, Name: csvPartName(opts.Name)}
	t := &tally{}
	enc := charset.SniffText(src, e.Charset)
	if err := t.sniffed(&enc, false); err != nil {
		return nil, err
	}
	decoded, _, invalid, cut := charset.Decode(src, enc, max(decode.Left(), 0))
	decode.Take(int64(len(decoded)))
	if invalid > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs,
			fmt.Sprintf("%d byte sequences are not valid %s and appear as U+FFFD", invalid, enc.Enc))
	}
	if cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; the rest of the input was not read")
	}

	body := decoded
	delim, how := rune(0), ""
	if d, rest, ok := sepLine(body); ok {
		delim, how, body = d, delimDeclared, rest
	} else if e.Delimiter != 0 {
		delim, how = e.Delimiter, delimOption
	} else if format == "tsv" {
		delim, how = '\t', delimFormat
	} else {
		delim, how = sniffDelimiter(body, e.NoQuotes), delimDetected
	}
	shown := string(delim)
	if delim != '\t' && unicode.IsControl(delim) {
		// No string has a control character but tab and line feed: one
		// such as U+001F, the unit separator, is named instead.
		shown = fmt.Sprintf("U+%04X", delim)
	} else if l := gofastcore.Ligature(delim); l != "" {
		shown = fmt.Sprintf("U+%04X", delim)
	}
	doc.Metadata.Extra = map[string]string{"delimiter": shown, "delimiter_source": how}

	res, err := readCSV(ctx, body, delim, e.Header, e.NoQuotes, text, &part)
	if err != nil {
		return nil, err
	}
	if res.controls > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, controlsMessage(res.controls, "the fields"))
	}
	if res.repaired > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputRepaired,
			count(res.repaired, "row has", "rows have")+" a quote outside the rules of RFC 4180 (a quote inside an unquoted field, text after a closing quote, or a quoted field not closed before the end of the input); the quote inside a field was kept as text, the text after a closing quote was joined to the field, and an unclosed field runs to the end of the input")
	}
	if res.colsCut > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("fields past column %d were not read (%s)", MaxCSVColumns, count(res.colsCut, "field", "fields")))
	}
	if res.fieldsCut > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("fields longer than %d bytes were cut there (%s, %d bytes left out)", MaxCSVField, count(res.fieldsCut, "field", "fields"), res.bytesCut))
	}
	if res.budget {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			textCutMessage(part.Kind)+" ("+count(res.rowsLeft, "row", "rows")+" not read)")
	}
	if len(part.Blocks) == 0 && !cut && !res.budget {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "no text")
	}
	part.Finish()
	doc.Parts = append(doc.Parts, part)
	t.finish(doc)
	return doc, nil
}

// csvPartName is the sheet's name: the file's name without its
// directory and extension.
func csvPartName(name string) string {
	if name == "" {
		return ""
	}
	base := filepath.Base(name)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return string(cleanAppend(nil, []byte(base), nil)) // a name: not counted
}

// sepLine reads Excel's declaration of the delimiter, a first line
// "sep=X", and returns X and the text after the line.
func sepLine(b []byte) (rune, []byte, bool) {
	if len(b) < 5 || !bytes.EqualFold(b[:4], []byte("sep=")) {
		return 0, nil, false
	}
	d, n := utf8.DecodeRune(b[4:])
	if !usableDelimiter(d) {
		return 0, nil, false
	}
	rest := b[4+n:]
	switch {
	case len(rest) == 0:
	case rest[0] == '\n':
		rest = rest[1:]
	case rest[0] == '\r':
		rest = rest[1:]
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
	default:
		return 0, nil, false
	}
	return d, rest, true
}

// sniffDelimiter picks the delimiter of a csv file among csvDelimiters:
// the one that splits the most of its first rows into the same number of
// fields, more than one. With none, it is the comma. A first row longer
// than the window is skipped, so that the rows after it decide.
func sniffDelimiter(b []byte, noQuotes bool) rune {
	best, bestRows := ',', 0
	for _, d := range csvDelimiters {
		delim := []byte(string(d))
		from := 0
		if len(b) > csvSniffBytes {
			// Where the first row ends depends on the delimiter, which
			// decides where a quoted field may begin.
			first := csvReader{src: b, delim: delim, noQuotes: noQuotes}
			if first.next() && first.pos >= csvSniffBytes {
				from = first.pos
			}
		}
		window := b[from:min(len(b), from+csvSniffBytes)]
		r := csvReader{src: window, delim: delim, noQuotes: noQuotes}
		counts := map[int]int{}
		for i := 0; i < csvSniffRows && r.next(); i++ {
			if r.pos >= len(window) && from+len(window) < len(b) {
				break // the window cut this row off
			}
			if r.fields > 1 {
				counts[r.fields]++
			}
		}
		top := 0
		for _, n := range counts {
			top = max(top, n)
		}
		if top > bestRows {
			best, bestRows = d, top
		}
	}
	return best
}

// csvResult is what reading the rows found.
type csvResult struct {
	repaired  int   // rows with a quote outside the rules
	controls  int   // control characters dropped from the fields output
	colsCut   int   // fields past MaxCSVColumns
	fieldsCut int   // fields longer than MaxCSVField
	bytesCut  int64 // what those fields lost
	budget    bool  // the text budget cut the sheet off
	rowsLeft  int   // rows not read for it
}

// readCSV reads the rows of text into table blocks of part. It reads the
// text twice: once to count the rows and find the widest, without
// keeping anything, so that every piece of a split table has the same
// columns; then to build the tables. What it holds besides the output is
// one row.
func readCSV(ctx context.Context, text []byte, delim rune, header, noQuotes bool, budget *gofastcore.TextBudget, part *gofastcore.Part) (csvResult, error) {
	var res csvResult
	d := []byte(string(delim))

	// The rows to read end at the last row with text; the width is the
	// widest of them.
	r := csvReader{src: text, delim: d, noQuotes: noQuotes}
	rows, w, width := 0, 0, 0
	for i := 0; r.next(); i++ {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return res, err
			}
		}
		w = max(w, min(r.fields, MaxCSVColumns))
		if r.text {
			rows, width = i+1, w
		}
	}
	if rows == 0 {
		return res, nil
	}

	perBlock := min(csvRowsPerBlock, max(1, csvCellsPerBlock/width))
	var table *gofastcore.Table
	first := 0 // the row the table begins at
	flush := func() {
		if table == nil {
			return
		}
		if len(table.Rows) > 0 {
			if header && first == 0 {
				table.HeaderRows = 1
			}
			part.Blocks = append(part.Blocks, gofastcore.Block{Kind: gofastcore.KindTable, Table: table, Continued: first > 0,
				Loc: gofastcore.Location{Range: a1Range(first, 0, first+len(table.Rows)-1, width-1)}})
		}
		table = nil
	}
	r = csvReader{src: text, delim: d, noQuotes: noQuotes, keep: true}
	var clean []byte
	var ends []int
	for i := 0; i < rows && r.next(); i++ {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return res, err
			}
		}
		if table == nil {
			if !budget.Take(gofastcore.BlockCost) {
				res.budget, res.rowsLeft = true, rows-i
				break
			}
			table = &gofastcore.Table{Rows: make([][]string, 0, min(perBlock, rows-i))}
			first = i
		}
		clean, ends = clean[:0], ends[:0]
		prev, rowControls := 0, 0
		for _, e := range r.ends {
			clean = cleanAppend(clean, r.buf[prev:e], &rowControls)
			ends = append(ends, len(clean))
			prev = e
		}
		if !budget.Take(gofastcore.CellCost*width + len(clean)) {
			res.budget, res.rowsLeft = true, rows-i
			break
		}
		// Only a row that is output is counted.
		if r.repaired {
			res.repaired++
		}
		res.controls += rowControls
		res.colsCut += r.colsCut
		res.fieldsCut += r.fieldsCut
		res.bytesCut += r.bytesCut
		s := string(clean)
		cells := make([]string, width)
		prev = 0
		for j, e := range ends {
			cells[j] = s[prev:e]
			prev = e
		}
		table.Rows = append(table.Rows, cells)
		if len(table.Rows) == perBlock {
			flush()
		}
		if cap(clean) > 1<<20 {
			clean = nil // one long row does not keep its buffer
		}
	}
	flush()
	return res, nil
}

// a1Range writes the cells from row r0, column c0 to row r1, column c1
// (from 0) in A1 notation.
func a1Range(r0, c0, r1, c1 int) string {
	if r0 == r1 && c0 == c1 {
		return a1Cell(r0, c0)
	}
	return a1Cell(r0, c0) + ":" + a1Cell(r1, c1)
}

func a1Cell(row, col int) string {
	var b []byte
	for c := col; c >= 0; c = c/26 - 1 {
		b = append(b, byte('A'+c%26))
	}
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b) + strconv.Itoa(row+1)
}

// cleanAppend appends field text to dst following the text conventions:
// control characters other than tab and line feed are dropped, and
// ligatures written as their letters. Nothing else is changed: white
// space is kept as written. src is valid UTF-8. The control characters
// dropped are counted in *controls when it is not nil.
func cleanAppend(dst, src []byte, controls *int) []byte {
	from := 0
	for i := 0; i < len(src); {
		c := src[i]
		n := 0 // the length of what is dropped or rewritten at i
		var lig string
		switch {
		case c < 0x20 && c != '\t' && c != '\n', c == 0x7F:
			n = 1
			if controls != nil && c != '\r' && c != '\f' {
				*controls++
			}
		case c == 0xC2 && i+1 < len(src) && src[i+1] >= 0x80 && src[i+1] <= 0x9F: // C1 controls
			n = 2
			if controls != nil {
				*controls++
			}
		case c == 0xEF && i+2 < len(src) && src[i+1] == 0xAC && src[i+2] <= 0x86:
			r, size := utf8.DecodeRune(src[i:])
			if lig = gofastcore.Ligature(r); lig != "" {
				n = size
			}
		}
		if n == 0 {
			i++
			continue
		}
		dst = append(dst, src[from:i]...)
		dst = append(dst, lig...)
		i += n
		from = i
	}
	return append(dst, src[from:]...)
}

// csvReader reads the records of comma-separated text as RFC 4180 has
// them, leniently: a quote inside an unquoted field is text, the text
// after a closing quote is joined to the field, and a quoted field not
// closed runs to the end of the input. A record ends at CRLF, LF or CR
// outside quotes; inside quotes each of them is a line feed.
type csvReader struct {
	src   []byte
	pos   int
	delim []byte
	// keep collects the fields' bytes in buf; without it only the fields
	// are counted.
	keep bool
	// noQuotes reads quotes as text: fields end only at the delimiter and
	// records at line ends.
	noQuotes bool

	// The last record read.
	buf       []byte // its fields, up to MaxCSVColumns, each held to MaxCSVField
	ends      []int  // where each field kept ends in buf
	fields    int    // the number of fields, also those past MaxCSVColumns; 0 for an empty line
	text      bool   // a field kept has a character cleanAppend keeps
	repaired  bool   // a quote is outside the rules
	colsCut   int    // fields past MaxCSVColumns
	fieldsCut int    // fields cut at MaxCSVField
	bytesCut  int64  // the bytes those lost

	// The field being read.
	inCols    bool // it is within MaxCSVColumns
	keepField bool
	start     int  // where it begins in buf
	raw       int  // its length in full
	c2        bool // the last byte of it added was 0xC2
}

// add adds b to the field being read.
func (r *csvReader) add(b []byte) {
	if len(b) == 0 {
		return
	}
	if room := MaxCSVField - r.raw; room > 0 && r.inCols {
		kept := b[:min(len(b), room)]
		if r.keepField {
			r.buf = append(r.buf, kept...)
		}
		if !r.text {
			r.findText(kept)
		}
	}
	r.raw += len(b)
}

// findText sets text if b, added to the field, has a character that
// cleanAppend keeps: anything but a control character other than tab and
// line feed. A C1 control is two bytes, which may come in two calls.
func (r *csvReader) findText(b []byte) {
	for _, c := range b {
		if r.c2 {
			r.c2 = false
			if c >= 0x80 && c <= 0x9F {
				continue
			}
			r.text = true
			return
		}
		switch {
		case c == 0xC2:
			r.c2 = true
		case c < 0x20 && c != '\t' && c != '\n', c == 0x7F:
		default:
			r.text = true
			return
		}
	}
}

// next reads the next record, and reports whether there was one.
func (r *csvReader) next() bool {
	src, d := r.src, r.delim
	pos := r.pos
	if pos >= len(src) {
		return false
	}
	if cap(r.buf) > 4<<20 {
		r.buf = nil
	}
	r.buf, r.ends = r.buf[:0], r.ends[:0]
	r.fields, r.text, r.repaired, r.colsCut, r.fieldsCut, r.bytesCut = 0, false, false, 0, 0, 0
	switch src[pos] {
	case '\n':
		r.pos = pos + 1
		return true
	case '\r':
		pos++
		if pos < len(src) && src[pos] == '\n' {
			pos++
		}
		r.pos = pos
		return true
	}
	d0 := d[0]
	quote := byte('"')
	if r.noQuotes {
		quote = '\n' // already a byte the fields stop at
	}
	for {
		col := r.fields
		r.fields++
		r.inCols = col < MaxCSVColumns
		r.keepField = r.keep && r.inCols
		if col >= MaxCSVColumns {
			r.colsCut++
		}
		r.start, r.raw, r.c2 = len(r.buf), 0, false
		after := false // the field was quoted and the quote closed
		if pos < len(src) && src[pos] == quote {
			pos++
			closed := false
			for pos < len(src) && !closed {
				i := pos
				for i < len(src) && src[i] != '"' && src[i] != '\r' {
					i++
				}
				r.add(src[pos:i])
				pos = i
				switch {
				case pos == len(src):
				case src[pos] == '\r':
					r.add([]byte{'\n'})
					pos++
					if pos < len(src) && src[pos] == '\n' {
						pos++
					}
				case pos+1 < len(src) && src[pos+1] == '"':
					r.add(src[pos : pos+1])
					pos += 2
				default:
					pos++
					closed = true
				}
			}
			if !closed {
				r.repaired = true
			}
			after = closed
		}
		delimited := false
		for pos < len(src) {
			i := pos
			for i < len(src) && src[i] != d0 && src[i] != '\n' && src[i] != '\r' && src[i] != quote {
				i++
			}
			if i > pos {
				if after {
					r.repaired = true
				}
				r.add(src[pos:i])
				pos = i
			}
			if pos == len(src) {
				break
			}
			c := src[pos]
			if c == d0 && bytes.HasPrefix(src[pos:], d) {
				pos += len(d)
				delimited = true
				break
			}
			if c == '\n' || c == '\r' {
				pos++
				if c == '\r' && pos < len(src) && src[pos] == '\n' {
					pos++
				}
				break
			}
			// A quote inside the field, or the first byte of a delimiter
			// of more than one byte that is not the delimiter.
			if c == quote || after {
				r.repaired = true
			}
			r.add(src[pos : pos+1])
			pos++
		}
		r.endField()
		if !delimited {
			break
		}
	}
	r.pos = pos
	return true
}

// endField finishes the field being read.
func (r *csvReader) endField() {
	if !r.keepField {
		return
	}
	if r.raw > MaxCSVField {
		// Cut at a character boundary.
		f := r.buf[r.start:]
		n := len(f)
		for i := n - 1; i >= 0 && i >= n-utf8.UTFMax; i-- {
			if utf8.RuneStart(f[i]) {
				if !utf8.FullRune(f[i:]) {
					n = i
				}
				break
			}
		}
		r.buf = r.buf[:r.start+n]
		r.fieldsCut++
		r.bytesCut += int64(r.raw - n)
	}
	r.ends = append(r.ends, len(r.buf))
}
