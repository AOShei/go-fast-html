package yaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// drain parses src to the end and returns the number of events.
func drain(src []byte, lim Limits) (int, error) {
	p := NewParser(src, lim)
	n := 0
	for {
		_, err := p.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		n++
	}
}

// repeatTo repeats unit (between head and tail) until the input is about
// size bytes.
func repeatTo(head, unit, tail string, size int) []byte {
	var b bytes.Buffer
	b.Grow(size + len(head) + len(tail) + len(unit))
	b.WriteString(head)
	for b.Len() < size {
		b.WriteString(unit)
	}
	b.WriteString(tail)
	return b.Bytes()
}

var shapes = []struct {
	name string
	gen  func(size int) []byte
	err  bool // the shape is an error, found late
}{
	{"long plain line", func(n int) []byte { return repeatTo("", "word ", "\n", n) }, false},
	{"multi-line plain", func(n int) []byte { return repeatTo("k: ", "word\n  ", "x\n", n) }, false},
	{"many keys", func(n int) []byte { return repeatTo("", "key: value\n", "", n) }, false},
	{"deep flow nesting", func(n int) []byte {
		unit := strings.Repeat("[", 500) + strings.Repeat("]", 500) + ",\n"
		return repeatTo("[", unit, "0]\n", n)
	}, false},
	{"long key candidates", func(n int) []byte {
		return repeatTo("", "- "+strings.Repeat("candidate ", 120)+"\n", "", n)
	}, false},
	{"flow key candidates", func(n int) []byte { return repeatTo("[", "a, ", "b]\n", n) }, false},
	{"pending flow-mapping key", func(n int) []byte { return repeatTo("{ [", "a, ", "b]: c }\n", n) }, true},
	{"many aliases", func(n int) []byte { return repeatTo("- &a x\n", "- *a\n", "", n) }, false},
	{"many documents", func(n int) []byte { return repeatTo("", "--- a\n", "", n) }, false},
	{"long quoted", func(n int) []byte { return repeatTo("\"", "ab\\n \\u00e9\n ", "\"\n", n) }, false},
	{"long literal", func(n int) []byte { return repeatTo("|\n", "  line\n\n", "", n) }, false},
	{"open flow", func(n int) []byte { return repeatTo("", "[", "", n) }, true},
}

// timeParse returns the best of runs timings: the machine may be shared,
// and the best run is the one least disturbed.
// Collection is held off while timing (up to a memory limit), so that the
// timings measure the parser rather than the collector.
func timeParse(src []byte, runs int) (time.Duration, error) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(256 << 20))
	best := time.Duration(1 << 62)
	var err error
	for i := 0; i < runs; i++ {
		runtime.GC()
		stop := cpuTimer()
		_, err = drain(src, Limits{})
		if d := stop(); d < best {
			best = d
		}
	}
	return best, err
}

func TestLinearTime(t *testing.T) {
	const mib = 1 << 20
	runs := 3
	if testing.Short() {
		runs = 1
	}
	for _, sh := range shapes {
		small := sh.gen(mib)
		d1, err := timeParse(small, runs)
		if (err != nil) != sh.err {
			t.Errorf("%s: error %v", sh.name, err)
		}
		if d1 > time.Second {
			t.Errorf("%s: 1 MiB took %v", sh.name, d1)
		}
		big := sh.gen(4 * mib)
		d4, _ := timeParse(big, runs)
		// Under ~5x for 4x the input, with a floor against the noise of a
		// shared machine (allocation of the larger values, collection).
		if d4 > 5*d1+100*time.Millisecond {
			t.Errorf("%s: 1 MiB %v, 4 MiB %v", sh.name, d1, d4)
		}
		t.Logf("%-26s 1 MiB %8v  4 MiB %8v", sh.name, d1, d4)
	}
}

func TestDepthLimit(t *testing.T) {
	tests := []struct {
		src   string
		limit int
		deep  bool
	}{
		{strings.Repeat("[", 10) + strings.Repeat("]", 10), 10, false},
		{strings.Repeat("[", 11) + strings.Repeat("]", 11), 10, true},
		{strings.Repeat("{a: ", 11) + strings.Repeat("}", 11), 10, true},
		{strings.Repeat("- ", 11) + "x\n", 10, true},
		{strings.Repeat("- ", 10) + "x\n", 10, false},
		{"a:\n" + strings.Repeat(" ", 2) + "b:\n    [[[[[[[[x]]]]]]]]\n", 10, false},
		{"a:\n" + strings.Repeat(" ", 2) + "b:\n    [[[[[[[[[x]]]]]]]]]\n", 10, true},
		{strings.Repeat("[", 600) + strings.Repeat("]", 600), 0, true},
	}
	for _, tt := range tests {
		_, err := drain([]byte(tt.src), Limits{MaxDepth: tt.limit})
		if tt.deep != errors.Is(err, ErrDepth) {
			t.Errorf("%.40q limit %d: err %v", tt.src, tt.limit, err)
		}
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		src       string
		line, col int
		contains  string
	}{
		{"a: b: c\n", 1, 5, "mapping values are not allowed"},
		{"a:\n\tb: c\n", 2, 2, "tab"},
		{"key: \"open\n", 1, 6, "end of stream"},
		{"- [a, b\n", 2, 1, ""},
		{"a: !e!x b\n", 1, 4, "undefined tag handle"},
		{"\"\\q\"\n", 1, 2, "unknown escape"},
	}
	for _, tt := range tests {
		_, err := drain([]byte(tt.src), Limits{})
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("%q: err %v, want a *SyntaxError", tt.src, err)
			continue
		}
		if se.Line != tt.line || se.Column != tt.col || !strings.Contains(se.Msg, tt.contains) {
			t.Errorf("%q: %v (offset %d), want line %d column %d with %q", tt.src, se, se.Offset, tt.line, tt.col, tt.contains)
		}
	}
}

func TestValues(t *testing.T) {
	tests := []struct{ src, want string }{
		{"a: x\n  y\n\n  z\n", "a|x y\nz"},
		{"- \"a\\x41\\u00e9\\U0001F600\\N\\_\\L\\P\\/\\ \\t\"\n", "a\u0041\u00e9\U0001F600\u0085\u00a0\u2028\u2029/ \t"},
		{"- \"a \\\n   b\"\n", "a b"},
		{"- 'a\n\n  b'\n", "a\nb"},
		{"- |+\n  a\n\n", "a\n\n"},
		{"- >\n  a\n    b\n  c\n", "a\n  b\nc\n"},
		{"- &a !local x\n- *a\n", "x|a"},
		{"[a:b, c: d, \"e\":f]", "a:b|c|d|e|f"},
		{"- x # comment\n- y#z\n", "x|y#z"},
		{`{"a":[1,2,{"b":null}],"c":"d","e":{}}`, "a|1|2|b|null|c|d|e"},
		{"{\n  \"k\": [\n    true,\n    \"v\"\n  ]\n}\n", "k|true|v"},
	}
	for _, tt := range tests {
		p := NewParser([]byte(tt.src), Limits{})
		var vals []string
		for {
			ev, err := p.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%q: %v", tt.src, err)
			}
			if ev.Kind == Scalar || ev.Kind == Alias {
				vals = append(vals, ev.Value)
			}
		}
		if got := strings.Join(vals, "|"); got != tt.want {
			t.Errorf("%q: got %q, want %q", tt.src, got, tt.want)
		}
	}
}

func ExampleParser() {
	p := NewParser([]byte("spec:\n  containers:\n  - name: web\n"), Limits{})
	for {
		ev, err := p.Next()
		if err != nil {
			break
		}
		if ev.Kind == Scalar {
			fmt.Printf("%d-%d %s\n", ev.Start, ev.End, ev.Value)
		}
	}
	// Output:
	// 0-4 spec
	// 8-18 containers
	// 24-28 name
	// 30-33 web
}
