package mimepart

import "bytes"

// Multipart is a multipart body split at its boundary.
type Multipart struct {
	Preamble []byte   // before the first boundary
	Parts    [][]byte // each part: its header section and body
	Epilogue []byte   // after the closing boundary
	// Closed is false when no closing boundary was found: the last part
	// runs to the end of the body.
	Closed bool
	// More is true when there were more parts than the limit; the rest
	// were left out.
	More bool
}

// Split splits a multipart body at the given boundary (RFC 2046, 5.1.1). A
// boundary line is "--" and the boundary at the start of a line, followed
// by optional whitespace; the closing one has "--" after the boundary. The
// line break before a boundary line belongs to it, not to the part. At
// most maxParts parts are returned.
func Split(body []byte, boundary string, maxParts int) Multipart {
	return split(body, boundary, maxParts, false)
}

// SplitAnywhere is Split for a body whose boundary lines do not always
// begin a line: a part in binary transfer encoding that does not end with
// a line break, as some browsers write MHTML files (Chromium reads them).
// A delimiter is found wherever it is, and the rest of its line must be
// white space as for Split.
func SplitAnywhere(body []byte, boundary string, maxParts int) Multipart {
	return split(body, boundary, maxParts, true)
}

func split(body []byte, boundary string, maxParts int, anywhere bool) Multipart {
	if anywhere {
		return splitAnywhere(body, boundary, maxParts)
	}
	var m Multipart
	delim := []byte("--" + boundary)
	start := -1 // where the current part begins; -1 while in the preamble
	pos := 0
	for pos <= len(body) {
		at := nextDelimiter(body, delim, pos)
		if at < 0 {
			break
		}
		lineStart := at
		after := at + len(delim)
		closing := bytes.HasPrefix(body[after:], []byte("--"))
		if closing {
			after += 2
		}
		// The rest of the boundary line must be whitespace; otherwise it
		// is a line that begins with the boundary, which is text.
		end := lineEnd(body, after)
		if len(bytes.TrimRight(body[after:end], " \t\r\n")) != 0 {
			pos = after
			continue
		}
		// The line break before the boundary belongs to the boundary.
		contentEnd := lineStart
		if contentEnd > 0 && body[contentEnd-1] == '\n' {
			contentEnd--
			if contentEnd > 0 && body[contentEnd-1] == '\r' {
				contentEnd--
			}
		}
		if start < 0 {
			m.Preamble = body[:max(contentEnd, 0)]
		} else if !m.add(body[start:max(contentEnd, start)], maxParts) {
			return m
		}
		if closing {
			m.Closed = true
			m.Epilogue = body[end:]
			return m
		}
		start = end
		pos = end
	}
	if start < 0 {
		// No boundary at all: the body is all preamble.
		m.Preamble = body
		return m
	}
	m.add(body[start:], maxParts)
	return m
}

func (m *Multipart) add(part []byte, maxParts int) bool {
	if len(m.Parts) >= maxParts {
		m.More = true
		return false
	}
	m.Parts = append(m.Parts, part)
	return true
}

// nextDelimiter returns the offset of the next occurrence of delim at the
// start of a line at or after pos, or -1.
func nextDelimiter(body, delim []byte, pos int) int {
	for pos < len(body) {
		i := bytes.Index(body[pos:], delim)
		if i < 0 {
			return -1
		}
		at := pos + i
		if at == 0 || body[at-1] == '\n' {
			return at
		}
		pos = at + 1
	}
	return -1
}

// splitAnywhere is SplitAnywhere. Since the rest of a boundary line must
// be white space, a delimiter that is not at the start of a line can only
// end where the line's text ends: each line is looked at once, at its
// end, so the time is linear in the body whatever it holds.
func splitAnywhere(body []byte, boundary string, maxParts int) Multipart {
	var m Multipart
	delim := []byte("--" + boundary)
	start := -1 // where the current part begins; -1 while in the preamble
	for ls := 0; ls < len(body); {
		end := lineEnd(body, ls) // after the line's break
		te := ls + len(bytes.TrimRight(body[ls:end], " \t\r\n"))
		at, closing := -1, false
		switch {
		case te-ls >= len(delim)+2 && bytes.HasSuffix(body[ls:te], []byte("--")) && bytes.Equal(body[te-2-len(delim):te-2], delim):
			at, closing = te-2-len(delim), true
		case te-ls >= len(delim) && bytes.Equal(body[te-len(delim):te], delim):
			at = te - len(delim)
		}
		if at < 0 {
			ls = end
			continue
		}
		// The line break before the boundary belongs to the boundary.
		contentEnd := at
		if contentEnd > 0 && body[contentEnd-1] == '\n' {
			contentEnd--
			if contentEnd > 0 && body[contentEnd-1] == '\r' {
				contentEnd--
			}
		}
		if start < 0 {
			m.Preamble = body[:max(contentEnd, 0)]
		} else if !m.add(body[start:max(contentEnd, start)], maxParts) {
			return m
		}
		if closing {
			m.Closed = true
			m.Epilogue = body[end:]
			return m
		}
		start = end
		ls = end
	}
	if start < 0 {
		m.Preamble = body
		return m
	}
	m.add(body[start:], maxParts)
	return m
}
