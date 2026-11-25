package mimepart

import (
	"bytes"
	"strings"
)

// maxParams bounds the parameters kept from one header value.
const maxParams = 64

// Params reads a Content-Type or Content-Disposition value: the value
// before the first ";", lowercase and trimmed, and its parameters, names
// lowercase. It never fails. A parameter value may be a token or a quoted
// string (backslash escapes undone); a quoted string with no closing
// quote runs to the end. Parameters written without "=" and repeated
// names after the first are skipped, as browsers read the parameters of
// MHTML files (Chromium's "relaxed" parameters). RFC 2231 continuations
// and charsets are not joined or decoded: MHTML files written by
// browsers do not use them, and such a parameter is kept under its name
// as written ("name*0").
func Params(raw []byte) (string, map[string]string) {
	s := string(bytes.TrimSpace(raw))
	i := strings.IndexByte(s, ';')
	if i < 0 {
		return strings.ToLower(strings.TrimSpace(s)), map[string]string{}
	}
	value := strings.ToLower(strings.TrimSpace(s[:i]))
	params := map[string]string{}
	rest := s[i+1:]
	for rest != "" && len(params) < maxParams {
		rest = strings.TrimLeft(rest, " \t\r\n;")
		eq := strings.IndexAny(rest, "=;")
		if eq < 0 {
			break
		}
		name := strings.ToLower(strings.TrimSpace(rest[:eq]))
		if rest[eq] == ';' {
			rest = rest[eq:]
			continue
		}
		rest = strings.TrimLeft(rest[eq+1:], " \t\r\n")
		var v string
		if strings.HasPrefix(rest, `"`) {
			var sb strings.Builder
			j := 1
			for ; j < len(rest); j++ {
				c := rest[j]
				if c == '\\' && j+1 < len(rest) {
					j++
					sb.WriteByte(rest[j])
					continue
				}
				if c == '"' {
					break
				}
				sb.WriteByte(c)
			}
			v = sb.String()
			rest = rest[min(j+1, len(rest)):]
		} else {
			end := strings.IndexByte(rest, ';')
			if end < 0 {
				end = len(rest)
			}
			v = strings.TrimSpace(rest[:end])
			rest = rest[end:]
		}
		if name == "" {
			continue
		}
		if _, dup := params[name]; !dup {
			params[name] = v
		}
	}
	return value, params
}
