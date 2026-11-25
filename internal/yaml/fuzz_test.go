package yaml

import (
	"errors"
	"io"
	"testing"
)

var inlineSeeds = []string{
	"key: value\n",
	"- a\n- - b\n  - c\n- d: e\n  f: g\n",
	"{a: [b, {c: d}], e: f}\n",
	"? |\n  block\n: >-\n  folded\n\n  more\n",
	"\"esc \\x41\\u00e9\\U0001F600 \\\n  cont\"\n",
	"'it''s'\n",
	"%YAML 1.2\n%TAG !e! tag:example.com,2000:\n--- !e!x &a [*a, !!str b]\n...\n",
	"a: &x 1\nb: *x\n",
	"--- |2-\n   x\n--- >+\n\n",
	"[a: b, : c, d: ]\n",
	"key:\n- a\n- b\nk2: v\n",
	"\t- x\n",
	"plain\n  multi\n\n  line # c\n",
}

func FuzzParser(f *testing.F) {
	for _, s := range inlineSeeds {
		f.Add([]byte(s))
	}
	for _, c := range loadSuite(f) {
		f.Add(c.in)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 1<<16 {
			return
		}
		p := NewParser(src, Limits{MaxDepth: 64})
		var stack []EventKind
		budget := 8*len(src) + 64
		for {
			ev, err := p.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				var se *SyntaxError
				if !errors.As(err, &se) || se.Offset < 0 || se.Offset > len(src) {
					t.Fatalf("error %v (%T)", err, err)
				}
				return
			}
			if budget--; budget < 0 {
				t.Fatalf("too many events for %d bytes", len(src))
			}
			if ev.Start < 0 || ev.End > len(src) || ev.Start > ev.End || ev.PropStart > ev.Start || ev.PropStart < 0 {
				t.Fatalf("%v: bad span [%d,%d) prop %d of %d", ev.Kind, ev.Start, ev.End, ev.PropStart, len(src))
			}
			switch ev.Kind {
			case StreamStart, DocumentStart, MappingStart, SequenceStart:
				stack = append(stack, ev.Kind)
			case StreamEnd, DocumentEnd, MappingEnd, SequenceEnd:
				want := map[EventKind]EventKind{StreamEnd: StreamStart, DocumentEnd: DocumentStart,
					MappingEnd: MappingStart, SequenceEnd: SequenceStart}[ev.Kind]
				if len(stack) == 0 || stack[len(stack)-1] != want {
					t.Fatalf("unbalanced %v", ev.Kind)
				}
				stack = stack[:len(stack)-1]
			}
		}
		if len(stack) != 0 {
			t.Fatalf("%d left open", len(stack))
		}
	})
}
