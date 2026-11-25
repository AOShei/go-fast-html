// Package mimepart reads the MIME structure of a web page saved as one
// file (MHTML, RFC 2557): the header section, the parameters of a header,
// the parts of a multipart body, and the transfer encodings. It works on
// bytes in memory, trusts nothing it reads, and never fails: what is
// damaged is read as far as it goes, and the caller is told.
//
// header.go, multipart.go and decode.go are copied from go-fast-email's
// internal/rfc822 (same authors, same licence), since the libraries do not
// import each other.
package mimepart

import (
	"bytes"
	"slices"
	"strings"
)

// Field is one header field: its name as written and its value with the
// folding removed, still in the bytes of the message.
type Field struct {
	Name  string
	Value []byte
}

// Header is the fields of a header section, in order.
type Header []Field

// Get returns the value of the first field with the given name, compared
// without regard to case, and whether there is one.
func (h Header) Get(name string) ([]byte, bool) {
	for i := range h {
		if strings.EqualFold(h[i].Name, name) {
			return h[i].Value, true
		}
	}
	return nil, false
}

// Problems found while a header section was read.
type Problems struct {
	// Truncated: the section was longer than the limit and was cut there.
	Truncated bool
	// Malformed: a line that is not a field ended the section where no
	// blank line did; the body begins at that line.
	Malformed bool
	// TooMany: there were more fields than the limit; the rest were left
	// out.
	TooMany bool
}

// MaxFields bounds the fields kept from one header section.
const MaxFields = 10_000

// ParseHeader reads the header section at the start of msg and returns its
// fields and the offset at which the body begins. At most maxBytes of msg
// are read as header; a section longer than that is cut there and the body
// is taken to begin at the end of it.
//
// A line that is neither a field nor the continuation of one ends the
// section, as a blank line does, and the body begins with that line. A
// first line that is an mbox "From " line is skipped.
func ParseHeader(msg []byte, maxBytes int) (Header, int, Problems) {
	return parseHeader(msg, maxBytes, nil)
}

// A Scratch holds the memory ParseHeader uses, so that one header's can
// be used again for the next. The zero value is ready to use.
type Scratch struct {
	buf   []byte
	h     Header
	first int
}

// First is where, in the bytes the last ParseHeader of s read, the line of
// the header's first field begins; -1 if it found none. A field is read
// when its line begins before the limit, so a header read with a larger
// limit holds the fields a smaller one would, and has one under a limit
// exactly when First is below it.
func (s *Scratch) First() int { return s.first }

// ParseHeader is ParseHeader, with the memory of s. What it returns
// refers to that memory: it may be used only until s parses again.
func (s *Scratch) ParseHeader(msg []byte, maxBytes int) (Header, int, Problems) {
	return parseHeader(msg, maxBytes, s)
}

// Poison overwrites the memory of s, for tests that check nothing still
// refers to it.
func (s *Scratch) Poison() {
	b := s.buf[:cap(s.buf)]
	for i := range b {
		b[i] = 0xFF
	}
}

// Small reports whether s holds no more than a header of 64 KiB and 1,024
// fields needs, so that a caller keeping scratches can leave a larger one
// to be collected.
func (s *Scratch) Small() bool { return cap(s.buf) <= 64<<10 && cap(s.h) <= 1024 }

func parseHeader(msg []byte, maxBytes int, s *Scratch) (h Header, at int, p Problems) {
	pos := 0
	limit := len(msg)
	if maxBytes > 0 && limit > maxBytes {
		limit = maxBytes
	}
	if bytes.HasPrefix(msg, []byte("From ")) {
		pos = lineEnd(msg, 0)
	}
	// The values are copied into one buffer, in order, each capped at
	// its length (so that an append to one cannot run into the next); a
	// continuation line extends the last value, at the buffer's end.
	// Its capacity is that of the section, to its first blank line, at
	// most 64 KiB to begin with; it grows past that as append does.
	n := limit - pos
	for _, end := range [][]byte{[]byte("\n\n"), []byte("\n\r\n")} {
		if i := bytes.Index(msg[pos:limit], end); i >= 0 && i < n {
			n = i + 1
		}
	}
	// A field a line at most: as many lines as the section has.
	fields := min(bytes.Count(msg[pos:pos+min(n, 64<<10)], []byte("\n"))+1, MaxFields)
	var buf []byte
	if s != nil {
		// The scratch keeps what was made or grown, for the next header.
		buf, h = slices.Grow(s.buf[:0], min(n, 64<<10)), slices.Grow(s.h[:0], fields)
		s.first = -1
		defer func() { s.buf, s.h = buf[:0], h[:0] }()
	} else {
		buf, h = make([]byte, 0, min(n, 64<<10)), make(Header, 0, fields)
	}
	start := 0 // where the last value begins in buf
	for pos < limit {
		end := lineEnd(msg, pos)
		line := trimEOL(msg[pos:end])
		if len(line) == 0 {
			return h, end, p // the blank line that ends the section
		}
		if (line[0] == ' ' || line[0] == '\t') && len(h) > 0 {
			// The continuation of the field before: the line break is
			// removed and the whitespace kept (RFC 5322, 2.2.3).
			if !p.TooMany {
				last := &h[len(h)-1]
				buf = append(buf, line...)
				last.Value = buf[start:len(buf):len(buf)]
			}
			pos = end
			continue
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 || !fieldName(line[:colon]) {
			if line[0] == ' ' || line[0] == '\t' {
				// Whitespace before the first field: skipped.
				pos = end
				continue
			}
			p.Malformed = true
			return h, pos, p
		}
		if len(h) >= MaxFields {
			p.TooMany = true
		} else {
			value := bytes.TrimLeft(line[colon+1:], " \t")
			if len(h) == 0 && s != nil {
				s.first = pos
			}
			start = len(buf)
			buf = append(buf, value...)
			h = append(h, Field{Name: string(bytes.TrimRight(line[:colon], " \t")), Value: buf[start:len(buf):len(buf)]})
		}
		pos = end
	}
	if limit < len(msg) {
		p.Truncated = true
		return h, limit, p
	}
	return h, len(msg), p
}

// fieldName reports whether b is a field name: printable ASCII other than
// the colon (RFC 5322, 3.6.8). Whitespace before the colon is tolerated,
// as old mail has it.
func fieldName(b []byte) bool {
	b = bytes.TrimRight(b, " \t")
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c <= ' ' || c >= 0x7F {
			return false
		}
	}
	return true
}

// lineEnd returns the offset just past the line that begins at pos: past
// its "\n", or the end of b.
func lineEnd(b []byte, pos int) int {
	if i := bytes.IndexByte(b[pos:], '\n'); i >= 0 {
		return pos + i + 1
	}
	return len(b)
}

func trimEOL(line []byte) []byte {
	line = bytes.TrimSuffix(line, []byte("\n"))
	return bytes.TrimSuffix(line, []byte("\r"))
}
