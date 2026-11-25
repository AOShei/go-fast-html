package mimepart

import (
	"bytes"
	"encoding/base64"
)

// Base64 decodes base64 leniently: characters outside the alphabet (line
// breaks, spaces, stray punctuation) are skipped, decoding stops at the
// first "=", and a last group that is short is decoded as far as it goes.
// The result is never longer than three quarters of the input.
//
// The characters of the alphabet are gathered a few kilobytes at a time
// and decoded from there, so that decoding holds the input and the output
// and no copy of the input beside them.
func Base64(in []byte) []byte {
	out := make([]byte, 0, len(in)/4*3+3)
	var buf [4096]byte // a multiple of 4
	n := 0
	flush := func(final bool) {
		k := n
		if !final {
			k -= k % 4
		} else if k%4 == 1 { // one character is no byte
			k--
		}
		dst := out[len(out) : len(out)+base64.RawStdEncoding.DecodedLen(k)]
		m, _ := base64.RawStdEncoding.Decode(dst, buf[:k])
		out = out[:len(out)+m]
		n = copy(buf[:], buf[k:n])
	}
loop:
	for _, c := range in {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			buf[n] = c
			n++
			if n == len(buf) {
				flush(false)
			}
		case c == '=':
			break loop
		}
	}
	flush(true)
	return out
}

// Base64Bad reports whether in holds anything other than the base64
// alphabet, padding and whitespace: a sign that it was damaged.
func Base64Bad(in []byte) bool {
	for _, c := range in {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/', c == '=',
			c == '\r', c == '\n', c == ' ', c == '\t':
		default:
			return true
		}
	}
	return false
}

// QuotedPrintable decodes the quoted-printable encoding (RFC 2045, 6.7)
// leniently: "=XX" is a byte, "=" at the end of a line joins it to the
// next, whitespace at the end of a line is removed, and an "=" followed by
// anything else is kept as it is. Line breaks become "\n" (canonical CRLF
// becomes the line break of the text).
func QuotedPrintable(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for len(in) > 0 {
		var line []byte
		eol := false
		if i := bytes.IndexByte(in, '\n'); i >= 0 {
			line, in, eol = in[:i], in[i+1:], true
		} else {
			line, in = in, nil
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		line = bytes.TrimRight(line, " \t")
		soft := false
		if bytes.HasSuffix(line, []byte("=")) {
			soft, line = true, line[:len(line)-1]
		}
		for i := 0; i < len(line); i++ {
			c := line[i]
			if c == '=' && i+2 < len(line) && isHex(line[i+1]) && isHex(line[i+2]) {
				out = append(out, unhex(line[i+1])<<4|unhex(line[i+2]))
				i += 2
				continue
			}
			out = append(out, c)
		}
		if eol && !soft {
			out = append(out, '\n')
		}
	}
	return out
}
func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}
