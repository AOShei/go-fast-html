package charset

import (
	"math"
	"testing"
	"unicode/utf8"
)

func TestSniff(t *testing.T) {
	for _, c := range []struct {
		in       string
		enc      Encoding
		from     string
		declared string
		unsup    bool
	}{
		{"\xEF\xBB\xBF<p>x", UTF8, FromBOM, "", false},
		{"\xFF\xFE<\x00p\x00", UTF16LE, FromBOM, "", false},
		{"\xFE\xFF\x00<\x00p", UTF16BE, FromBOM, "", false},
		{`<meta charset="windows-1252"><p>`, Windows1252, FromMeta, "windows-1252", false},
		{`<META CHARSET=ISO-8859-1>`, Windows1252, FromMeta, "iso-8859-1", false},
		{`<meta http-equiv="Content-Type" content="text/html; charset=utf-8">`, UTF8, FromMeta, "utf-8", false},
		{`<meta content="text/html; charset=utf-8">`, UTF8, FromDetected, "", false},
		{`<meta charset="utf-16">`, UTF8, FromMeta, "utf-16", false},
		{`<meta charset="shift_jis">`, Windows1252, FromMeta, "shift_jis", true},
		{`<!-- <meta charset="shift_jis"> --><p>caf` + "\xC3\xA9", UTF8, FromDetected, "", false},
		{`<div title="<meta charset=koi8-r>">x`, UTF8, FromDetected, "", false},
		{"<p>caf\xE9", Windows1252, FromDetected, "", false},
	} {
		r := Sniff([]byte(c.in))
		if r.Enc != c.enc || r.From != c.from || r.Declared != c.declared || r.Unsupported != c.unsup {
			t.Errorf("Sniff(%q) = %+v", c.in, r)
		}
	}
}

func TestDecodeMap(t *testing.T) {
	for _, c := range []struct {
		src  string
		want string
	}{
		{"a\xE9b\x80c", "aéb€c"},
		{"\xFF\xFEa\x00\xE9\x00=\xD8\x00\xDEz\x00", "aé\U0001F600z"},
		{"\xFE\xFF\x00a\xD8\x00", "a�"},
		{"<meta charset=utf-8>\xC3(", "<meta charset=utf-8>�("},
	} {
		src := []byte(c.src)
		r := Sniff(src)
		out, m, _, cut := Decode(src, r, math.MaxInt64)
		if string(out) != c.want || cut {
			t.Errorf("Decode(%q) = %q, want %q", c.src, out, c.want)
			continue
		}
		// Every character's offset maps to a source offset in order,
		// and the end maps to the end.
		prev := int64(-1)
		for i := range string(out) {
			s := m.Source(i)
			if s <= prev || s >= int64(len(src)) {
				t.Errorf("%q: Source(%d) = %d after %d", c.src, i, s, prev)
			}
			prev = s
		}
		if got := m.Source(len(out)); got != int64(len(src)) {
			t.Errorf("%q: Source(end) = %d, want %d", c.src, got, len(src))
		}
	}
}

func TestDecodeLong(t *testing.T) {
	// Long enough for several checkpoints.
	src := make([]byte, 20000)
	for i := range src {
		src[i] = byte('a' + i%3)
		if i%7 == 0 {
			src[i] = 0xE9
		}
	}
	out, m, _, _ := Decode(src, Result{Enc: Windows1252}, math.MaxInt64)
	dec := 0
	for i := range src {
		if got := m.Source(dec); got != int64(i) {
			t.Fatalf("Source(%d) = %d, want %d", dec, got, i)
		}
		r, n := utf8.DecodeRune(out[dec:])
		_ = r
		dec += n
	}
}

func TestDecodeLimit(t *testing.T) {
	out, _, _, cut := Decode([]byte("\xE9\xE9\xE9"), Result{Enc: Windows1252}, 5)
	if !cut || len(out) != 4 {
		t.Errorf("got %q, cut %v", out, cut)
	}
	out, _, _, cut = Decode([]byte("ab\xC3\xA9"), Result{Enc: UTF8}, 3)
	if !cut || string(out) != "ab" {
		t.Errorf("got %q, cut %v", out, cut)
	}
}

func FuzzCharset(f *testing.F) {
	f.Add([]byte(`<meta charset="utf-8"><p>x`))
	f.Add([]byte("\xFF\xFEa\x00"))
	f.Add([]byte("<p>caf\xE9"))
	f.Fuzz(func(t *testing.T, b []byte) {
		r := Sniff(b)
		out, m, _, _ := Decode(b, r, 1<<20)
		if !utf8.Valid(out) {
			t.Fatal("invalid UTF-8")
		}
		prev := int64(-1)
		for i := range string(out) {
			s := m.Source(i)
			if s < prev || s > int64(len(b)) {
				t.Fatalf("Source(%d) = %d after %d", i, s, prev)
			}
			prev = s
		}
	})
}
