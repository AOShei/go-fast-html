package jsonscan

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// tokens renders the tokens of src: kind, text, depth.
func tokens(src string) (string, error) {
	s := New([]byte(src))
	var out []string
	for {
		tok, ok, err := s.Next()
		if err != nil {
			return strings.Join(out, " "), err
		}
		if !ok {
			return strings.Join(out, " "), nil
		}
		out = append(out, fmt.Sprintf("%d:%s@%d", tok.Kind, src[tok.Start:tok.End], tok.Depth))
	}
}

func TestTokens(t *testing.T) {
	for src, want := range map[string]string{
		`{"a": [1, -2.5e3, true, null], "b": {}}`: `1:{@0 5:"a"@1 3:[@1 7:1@2 7:-2.5e3@2 8:true@2 10:null@2 4:]@1 5:"b"@1 1:{@1 2:}@1 2:}@0`,
		"\uFEFF\"s\\u00e9\"":                      `6:"s\u00e9"@0`,
		"// c\n{/* x */\"a\":1,}":                 `1:{@0 5:"a"@1 7:1@1 2:}@0`,
		"[1,]":                                    `3:[@0 7:1@1 4:]@0`,
		"1 2\n[3]":                                `7:1@0 7:2@0 3:[@0 7:3@1 4:]@0`,
	} {
		got, err := tokens(src)
		if err != nil || got != want {
			t.Errorf("%q:\n got %s (%v)\nwant %s", src, got, err, want)
		}
	}
}

func TestErrors(t *testing.T) {
	for src, off := range map[string]int{
		`{"a" 1}`: 5, `[1 2]`: 3, `{,}`: 1, `[,1]`: 1, `[01]`: 1, `"a`: 0, `"\x"`: 1, `/`: 0, `[1`: 2,
		`tru`: 0, `truex`: 0, `{1: 2}`: 1, `1.`: 0, `-`: 0, `/* x`: 0,
	} {
		_, err := tokens(src)
		e, ok := err.(*Error)
		if !ok || e.Offset != off {
			t.Errorf("%q: %v, want an error at %d", src, err, off)
		}
	}
	// Nesting past the bound.
	s := New([]byte(strings.Repeat("[", 10)))
	s.MaxDepth = 5
	var err error
	for err == nil {
		_, _, err = s.Next()
	}
	if e := err.(*Error); e.Msg != ErrDepth || e.Offset != 5 {
		t.Errorf("depth: %v", err)
	}
	// After an error, Reset goes on at a position.
	s = New([]byte("[x\n[1]"))
	if _, _, err := s.Next(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Next(); err == nil {
		t.Fatal("no error")
	}
	s.Reset(3)
	if got, _, err := s.Next(); err != nil || got.Kind != ArrayStart || got.Start != 3 {
		t.Errorf("after Reset: %+v %v", got, err)
	}
}

func TestUnquote(t *testing.T) {
	for in, want := range map[string]string{
		`"abc"`: "abc", `"a\"b\\c\/d\n"`: "a\"b\\c/d\n", `"\u00e9\ud83d\ude00"`: "\u00e9\U0001F600", `"\ud800x"`: "\uFFFDx",
	} {
		if got, err := Unquote([]byte(in)); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	if _, err := Unquote([]byte(`"\q"`)); err == nil {
		t.Error(`\q`)
	}
}

// TestLinear reads inputs built to cost much for their size: 4 times the
// input should cost about 4 times the time.
func TestLinear(t *testing.T) {
	for name, unit := range map[string]string{
		"deep":     "[",
		"comments": "/**/",
		"strings":  `"\u0041",`,
		"numbers":  "1e5,",
		"keys":     `{"a":1},`,
	} {
		run := func(n int) time.Duration {
			src := []byte("[" + strings.Repeat(unit, n/len(unit)))
			best := time.Duration(1 << 62)
			for range 3 {
				start := time.Now()
				s := New(src)
				s.MaxDepth = 1 << 30
				for {
					if _, ok, err := s.Next(); !ok || err != nil {
						break
					}
				}
				best = min(best, time.Since(start))
			}
			return best
		}
		small, large := run(1<<18), run(1<<20)
		if large > 10*small+10*time.Millisecond {
			t.Errorf("%s: 256 KiB %v, 1 MiB %v", name, small, large)
		}
	}
}

// FuzzScan reads arbitrary input: no panic, tokens in order inside the
// input, containers balanced when there is no error.
//
//	go test -run XXX -fuzz FuzzScan -fuzztime 30s -fuzzminimizetime 0s ./internal/jsonscan
func FuzzScan(f *testing.F) {
	for _, s := range []string{`{"a": [1, 2.5, "x\u00e9"]}`, "// c\n[1,]", "1 2 3", `{"a"`, "[[[[", `"\ud800"`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		s := New(b)
		s.MaxDepth = 64
		depth, last := 0, 0
		for n := 0; n <= len(b)+1; n++ {
			tok, ok, err := s.Next()
			if err != nil {
				if e := err.(*Error); e.Offset < 0 || e.Offset > len(b) {
					t.Fatalf("error offset %d of %d", e.Offset, len(b))
				}
				return
			}
			if !ok {
				if depth != 0 {
					t.Fatalf("ended at depth %d", depth)
				}
				return
			}
			if tok.Start < last || tok.End <= tok.Start || tok.End > len(b) {
				t.Fatalf("token %+v after %d", tok, last)
			}
			last = tok.End
			switch tok.Kind {
			case ObjectStart, ArrayStart:
				depth++
			case ObjectEnd, ArrayEnd:
				depth--
			case String, Key:
				if _, err := Unquote(b[tok.Start:tok.End]); err != nil {
					t.Fatalf("string %q: %v", b[tok.Start:tok.End], err)
				}
			}
			if tok.Depth != depth && !(tok.Kind == ObjectStart || tok.Kind == ArrayStart) || tok.Depth < 0 {
				if (tok.Kind == ObjectStart || tok.Kind == ArrayStart) && tok.Depth == depth-1 {
					continue
				}
				t.Fatalf("token %+v at depth %d", tok, depth)
			}
		}
		t.Fatal("more tokens than bytes")
	})
}
