package yaml

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// checkOffsets checks the offset invariants of the events of src.
func checkOffsets(src []byte, evs []Event) error {
	type open struct {
		ev     Event
		maxEnd int
	}
	var stack []open
	for i, ev := range evs {
		if ev.Start < 0 || ev.End > len(src) || ev.Start > ev.End || ev.PropStart > ev.Start || ev.PropStart < 0 {
			return fmt.Errorf("event %d %v: bad span [%d,%d) prop %d", i, ev.Kind, ev.Start, ev.End, ev.PropStart)
		}
		span := src[ev.Start:ev.End]
		switch ev.Kind {
		case Scalar:
			if len(span) == 0 {
				break
			}
			first := span[0]
			switch ev.Style {
			case SingleQuoted:
				if first != '\'' || span[len(span)-1] != '\'' {
					return fmt.Errorf("event %d: single-quoted span %q", i, span)
				}
			case DoubleQuoted:
				if first != '"' || span[len(span)-1] != '"' {
					return fmt.Errorf("event %d: double-quoted span %q", i, span)
				}
			case Literal:
				if first != '|' {
					return fmt.Errorf("event %d: literal span %q", i, span)
				}
			case Folded:
				if first != '>' {
					return fmt.Errorf("event %d: folded span %q", i, span)
				}
			case Plain:
				if ev.Value == "" || first != ev.Value[0] {
					return fmt.Errorf("event %d: plain span %q value %q", i, span, ev.Value)
				}
				if !strings.ContainsAny(string(span), "\r\n") && string(span) != ev.Value {
					return fmt.Errorf("event %d: plain span %q value %q", i, span, ev.Value)
				}
			}
			if last := span[len(span)-1]; last == '\n' || last == '\r' {
				return fmt.Errorf("event %d: scalar span %q ends in a line break", i, span)
			}
		case Alias:
			if string(span) != "*"+ev.Value {
				return fmt.Errorf("event %d: alias span %q", i, span)
			}
		case MappingStart, SequenceStart:
			c := byte(0)
			if ev.Start < len(src) {
				c = src[ev.Start]
			}
			ok := false
			switch {
			case ev.Flow && ev.Kind == SequenceStart:
				ok = c == '['
			case ev.Flow && c == '{':
				ok = true
			case ev.Kind == SequenceStart:
				ok = c == '-'
			default:
				ok = c == '?' || c == ':' || i+1 < len(evs) && evs[i+1].PropStart == ev.Start
			}
			if !ok {
				return fmt.Errorf("event %d %v: start %d at %q", i, ev.Kind, ev.Start, src[ev.Start:min(len(src), ev.Start+10)])
			}
		}
		// nesting
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			switch ev.Kind {
			case MappingEnd, SequenceEnd, DocumentEnd:
			default:
				if ev.PropStart < top.ev.Start && top.ev.Kind != StreamStart {
					return fmt.Errorf("event %d %v at %d starts before its parent %v at %d", i, ev.Kind, ev.PropStart, top.ev.Kind, top.ev.Start)
				}
				top.maxEnd = max(top.maxEnd, ev.End)
			}
		}
		switch ev.Kind {
		case StreamStart, DocumentStart, MappingStart, SequenceStart:
			stack = append(stack, open{ev: ev, maxEnd: ev.End})
		case StreamEnd, DocumentEnd, MappingEnd, SequenceEnd:
			if len(stack) == 0 {
				return fmt.Errorf("event %d: unbalanced %v", i, ev.Kind)
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if ev.Kind != StreamEnd && top.maxEnd > ev.End {
				return fmt.Errorf("event %d %v at %d: a child ends at %d", i, ev.Kind, ev.End, top.maxEnd)
			}
			if len(stack) > 0 {
				p := &stack[len(stack)-1]
				p.maxEnd = max(p.maxEnd, ev.End)
			}
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("%d collections left open", len(stack))
	}
	return nil
}

func TestSuiteOffsets(t *testing.T) {
	cases := loadSuite(t)
	if cases == nil {
		t.Skip("YAML test suite not found; set GOFASTHTML_YAML_SUITE")
	}
	n := 0
	for _, c := range cases {
		_, evs, err := renderEvents(c.in)
		if err != nil {
			continue
		}
		n++
		if err := checkOffsets(c.in, evs); err != nil {
			t.Errorf("%s (%s): %v\n--- input\n%s", c.id, c.name, err, c.in)
		}
	}
	t.Logf("offsets checked on %d cases", n)
}

// span is an expected event: kind, its text in the input (by Start, End),
// and the text from PropStart to End when it differs.
type span struct {
	kind EventKind
	text string
	prop string
}

func eventsOf(t *testing.T, src string) []Event {
	t.Helper()
	p := NewParser([]byte(src), Limits{})
	var evs []Event
	for {
		ev, err := p.Next()
		if err == io.EOF {
			return evs
		}
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		evs = append(evs, ev)
	}
}

func TestOffsets(t *testing.T) {
	tests := []struct {
		src  string
		want []span
	}{
		{"key: value\n", []span{
			{StreamStart, "", ""}, {DocumentStart, "", ""}, {MappingStart, "", ""},
			{Scalar, "key", ""}, {Scalar, "value", ""}, {MappingEnd, "", ""},
			{DocumentEnd, "", ""}, {StreamEnd, "", ""}}},
		{"a: 'x''y'\nb: \"q\\n\"\n", []span{
			{StreamStart, "", ""}, {DocumentStart, "", ""}, {MappingStart, "", ""},
			{Scalar, "a", ""}, {Scalar, "'x''y'", ""}, {Scalar, "b", ""}, {Scalar, `"q\n"`, ""},
			{MappingEnd, "", ""}, {DocumentEnd, "", ""}, {StreamEnd, "", ""}}},
		{"- one\n  two\n- &a !!str three # c\n- *a\n", []span{
			{StreamStart, "", ""}, {DocumentStart, "", ""}, {SequenceStart, "", ""},
			{Scalar, "one\n  two", ""}, {Scalar, "three", "&a !!str three"}, {Alias, "*a", ""},
			{SequenceEnd, "", ""}, {DocumentEnd, "", ""}, {StreamEnd, "", ""}}},
		{"lit: |-\n  a\n  b\n\nfold: >\n  c\n", []span{
			{StreamStart, "", ""}, {DocumentStart, "", ""}, {MappingStart, "", ""},
			{Scalar, "lit", ""}, {Scalar, "|-\n  a\n  b", ""}, {Scalar, "fold", ""}, {Scalar, ">\n  c", ""},
			{MappingEnd, "", ""}, {DocumentEnd, "", ""}, {StreamEnd, "", ""}}},
		{"%YAML 1.2\n--- {a: [b, c]}\n...\n", []span{
			{StreamStart, "", ""}, {DocumentStart, "%YAML 1.2\n---", ""}, {MappingStart, "", ""},
			{Scalar, "a", ""}, {SequenceStart, "", ""}, {Scalar, "b", ""}, {Scalar, "c", ""},
			{SequenceEnd, "", ""}, {MappingEnd, "", ""}, {DocumentEnd, "...", ""}, {StreamEnd, "", ""}}},
		{"key:\nother: !!map\n  x: y\n", []span{
			{StreamStart, "", ""}, {DocumentStart, "", ""}, {MappingStart, "", ""},
			{Scalar, "key", ""}, {Scalar, "", ""}, {Scalar, "other", ""}, {MappingStart, "", "!!map\n  "},
			{Scalar, "x", ""}, {Scalar, "y", ""}, {MappingEnd, "", ""}, {MappingEnd, "", ""},
			{DocumentEnd, "", ""}, {StreamEnd, "", ""}}},
	}
	for _, tt := range tests {
		evs := eventsOf(t, tt.src)
		if len(evs) != len(tt.want) {
			t.Errorf("%q: %d events, want %d: %+v", tt.src, len(evs), len(tt.want), evs)
			continue
		}
		for i, ev := range evs {
			w := tt.want[i]
			got := tt.src[ev.Start:ev.End]
			prop := tt.src[ev.PropStart:ev.End]
			if ev.Kind != w.kind || got != w.text || w.prop != "" && prop != w.prop {
				t.Errorf("%q event %d: %v %q (prop %q), want %v %q (prop %q)", tt.src, i, ev.Kind, got, prop, w.kind, w.text, w.prop)
			}
		}
	}
}

// TestOffsetPositions checks exact offsets of the start of collections,
// the ends and the empty nodes.
func TestOffsetPositions(t *testing.T) {
	type pos struct {
		kind       EventKind
		start, end int
	}
	tests := []struct {
		src  string
		want []pos
	}{
		// 0123456789
		// a:\n- b\n
		{"a:\n- b\n", []pos{{StreamStart, 0, 0}, {DocumentStart, 0, 0}, {MappingStart, 0, 0},
			{Scalar, 0, 1}, {SequenceStart, 3, 3}, {Scalar, 5, 6}, {SequenceEnd, 6, 6},
			{MappingEnd, 6, 6}, {DocumentEnd, 6, 6}, {StreamEnd, 7, 7}}},
		// [a: , {b}]
		{"[a: , {b}]", []pos{{StreamStart, 0, 0}, {DocumentStart, 0, 0}, {SequenceStart, 0, 0},
			{MappingStart, 1, 1}, {Scalar, 1, 2}, {Scalar, 3, 3}, {MappingEnd, 3, 3},
			{MappingStart, 6, 6}, {Scalar, 7, 8}, {Scalar, 8, 8}, {MappingEnd, 9, 9},
			{SequenceEnd, 10, 10}, {DocumentEnd, 10, 10}, {StreamEnd, 10, 10}}},
		// ? a\n: b\n
		{"? a\n: b\n", []pos{{StreamStart, 0, 0}, {DocumentStart, 0, 0}, {MappingStart, 0, 0},
			{Scalar, 2, 3}, {Scalar, 6, 7}, {MappingEnd, 7, 7}, {DocumentEnd, 7, 7}, {StreamEnd, 8, 8}}},
		// - - x\n  - y\n
		{"- - x\n  - y\n", []pos{{StreamStart, 0, 0}, {DocumentStart, 0, 0}, {SequenceStart, 0, 0},
			{SequenceStart, 2, 2}, {Scalar, 4, 5}, {Scalar, 10, 11}, {SequenceEnd, 11, 11},
			{SequenceEnd, 11, 11}, {DocumentEnd, 11, 11}, {StreamEnd, 12, 12}}},
		// ---\n--- &x\n
		{"---\n--- &x\n", []pos{{StreamStart, 0, 0}, {DocumentStart, 0, 3}, {Scalar, 3, 3},
			{DocumentEnd, 3, 3}, {DocumentStart, 4, 7}, {Scalar, 10, 10}, {DocumentEnd, 10, 10},
			{StreamEnd, 11, 11}}},
		// \uFEFFa  (BOM skipped)
		{"\uFEFFa", []pos{{StreamStart, 0, 0}, {DocumentStart, 3, 3}, {Scalar, 3, 4},
			{DocumentEnd, 4, 4}, {StreamEnd, 4, 4}}},
	}
	for _, tt := range tests {
		evs := eventsOf(t, tt.src)
		var got []pos
		for _, ev := range evs {
			got = append(got, pos{ev.Kind, ev.Start, ev.End})
		}
		if fmt.Sprint(got) != fmt.Sprint(tt.want) {
			t.Errorf("%q:\n got %v\nwant %v", tt.src, got, tt.want)
		}
	}
}
