package mimepart

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestParseHeader(t *testing.T) {
	msg := "From sender@x Tue Jan  1 00:00:00 2026\nSubject: a\r\n  folded\r\nX-Two : 2\r\n\r\nbody"
	h, at, p := ParseHeader([]byte(msg), 0)
	if len(h) != 2 || string(h[0].Value) != "a  folded" || h[1].Name != "X-Two" || msg[at:] != "body" || p != (Problems{}) {
		t.Fatalf("got %q at %d, %+v", h, at, p)
	}
	h, at, p = ParseHeader([]byte("A: 1\nnot a header\nB: 2\n"), 0)
	if len(h) != 1 || !p.Malformed || !strings.HasPrefix("A: 1\nnot a header\nB: 2\n"[at:], "not a header") {
		t.Fatalf("malformed: %q at %d %+v", h, at, p)
	}
	_, at, p = ParseHeader([]byte("A: 1234567890\nB: 2\n\nbody"), 8)
	if !p.Truncated || at != 8 {
		t.Fatalf("truncated: at %d %+v", at, p)
	}
}

func TestTransferDecoding(t *testing.T) {
	if got := string(Base64([]byte("aGVs\r\nbG8g!d29y\nbGQ=\ntrailing"))); got != "hello world" {
		t.Errorf("Base64 = %q", got)
	}
	// Lengths around the size of the decoder's buffer, wrapped at 76.
	for _, n := range []int{0, 1, 2, 3, 3071, 3072, 3073, 10000} {
		raw := make([]byte, n)
		for i := range raw {
			raw[i] = byte(i*7 + i/251)
		}
		enc := base64.StdEncoding.EncodeToString(raw)
		var wrapped strings.Builder
		for len(enc) > 76 {
			wrapped.WriteString(enc[:76] + "\r\n")
			enc = enc[76:]
		}
		wrapped.WriteString(enc)
		if got := Base64([]byte(wrapped.String())); !bytes.Equal(got, raw) {
			t.Errorf("Base64 of %d bytes: got %d bytes, not the same", n, len(got))
		}
	}
	if got := string(QuotedPrintable([]byte("caf=C3=A9 =\r\nau lait  \r\n=XYend=3d"))); got != "caf\xc3\xa9 au lait\n=XYend=" {
		t.Errorf("QuotedPrintable = %q", got)
	}
}

func TestSplit(t *testing.T) {
	body := "preamble\r\n--b\r\nContent-Type: text/plain\r\n\r\none\r\n--b \r\n\r\ntwo\n--bb not a boundary\n--b--\r\nepilogue"
	m := Split([]byte(body), "b", 10)
	if string(m.Preamble) != "preamble" || len(m.Parts) != 2 || !m.Closed || string(m.Epilogue) != "epilogue" {
		t.Fatalf("got %+v", m)
	}
	if string(m.Parts[0]) != "Content-Type: text/plain\r\n\r\none" || string(m.Parts[1]) != "\r\ntwo\n--bb not a boundary" {
		t.Fatalf("parts %q", m.Parts)
	}
	m = Split([]byte("--b\nx\n--b\ny"), "b", 1)
	if len(m.Parts) != 1 || !m.More || m.Closed {
		t.Fatalf("limit: %+v", m)
	}
	m = Split([]byte("--b\nx\n--b\ny"), "b", 10)
	if len(m.Parts) != 2 || m.Closed || string(m.Parts[1]) != "y" {
		t.Fatalf("unclosed: %+v", m)
	}
}

// TestHeaderValuesApart: the values share one buffer, so an append to
// one, folded or not, must not change the next.
func TestHeaderValuesApart(t *testing.T) {
	for _, msg := range []string{
		"A: one\r\nB: two\r\n folded\r\nC: three\r\n\r\nbody",
		"A: one\nB: two\n\tfolded\nC: three\n\nbody",
		"From x\nA: one\nB: two", // no blank line
	} {
		h, _, _ := ParseHeader([]byte(msg), 0)
		want := make([]string, len(h))
		for i, f := range h {
			want[i] = string(f.Value)
		}
		for i := range h {
			_ = append(h[i].Value, "XXXXXXXXXXXXXXXX"...)
		}
		for i, f := range h {
			if string(f.Value) != want[i] {
				t.Errorf("%q: field %d is %q after an append to the others, want %q", msg, i, f.Value, want[i])
			}
		}
	}
}

func TestParams(t *testing.T) {
	for _, c := range []struct {
		in, value string
		params    map[string]string
	}{
		{`Multipart/Related; type="text/html"; boundary="----=_Next \"q\""`, "multipart/related", map[string]string{"type": "text/html", "boundary": `----=_Next "q"`}},
		{"text/html;charset=UTF-8;;charset=x; bad; =v; name", "text/html", map[string]string{"charset": "UTF-8"}},
		{`text/html; a="unclosed`, "text/html", map[string]string{"a": "unclosed"}},
		{"text/plain", "text/plain", map[string]string{}},
	} {
		v, p := Params([]byte(c.in))
		if v != c.value || fmt.Sprint(p) != fmt.Sprint(c.params) {
			t.Errorf("Params(%q) = %q %v, want %q %v", c.in, v, p, c.value, c.params)
		}
	}
}

func TestSplitAnywhere(t *testing.T) {
	m := SplitAnywhere([]byte("pre--b\r\nA\r\n\r\none--b\r\nB\r\n\r\ntwo\r\n--b--\r\nepi"), "b", 10)
	if len(m.Parts) != 2 || !m.Closed || string(m.Parts[0]) != "A\r\n\r\none" || string(m.Epilogue) != "epi" {
		t.Fatalf("%q", m.Parts)
	}
}

// FuzzMIME runs the header and parameter readers, the splitters and the
// transfer decoders on arbitrary bytes: they must not panic or grow what
// they are given.
func FuzzMIME(f *testing.F) {
	f.Add([]byte("A: 1\n  2\nContent-Type: multipart/related; boundary=b\n\npre\n--b\nx\n--b--\npost=3D=\n"), "b")
	f.Fuzz(func(t *testing.T, data []byte, boundary string) {
		h, at, _ := ParseHeader(data, 4096)
		if at < 0 || at > len(data) {
			t.Fatalf("body at %d of %d", at, len(data))
		}
		for _, field := range h {
			Params(field.Value)
		}
		if len(Base64(data)) > len(data) || len(QuotedPrintable(data)) > len(data)+1 {
			t.Fatal("decoding grew the input")
		}
		if boundary == "" {
			return
		}
		for _, m := range []Multipart{Split(data, boundary, 100), SplitAnywhere(data, boundary, 100)} {
			total := len(m.Preamble) + len(m.Epilogue)
			for _, p := range m.Parts {
				total += len(p)
			}
			if total > len(data) || len(m.Parts) > 100 {
				t.Fatalf("split gave %d bytes in %d parts from %d", total, len(m.Parts), len(data))
			}
		}
	})
}
