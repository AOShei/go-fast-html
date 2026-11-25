package yaml

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// suiteCase is one case of the YAML test suite.
type suiteCase struct {
	id, name string
	in       []byte
	events   string // test.event
	isError  bool
}

// suiteDir returns the directory of the YAML test suite, or "".
func suiteDir() string {
	if d := os.Getenv("GOFASTHTML_YAML_SUITE"); d != "" {
		return d
	}
	for _, d := range []string{"../../../.testdata/yaml-test-suite", "../../.testdata/yaml-test-suite"} {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

// loadSuite reads every case of the suite; nil when it is absent.
func loadSuite(t testing.TB) []suiteCase {
	dir := suiteDir()
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var cases []suiteCase
	read := func(id, d string) {
		in, err := os.ReadFile(filepath.Join(d, "in.yaml"))
		if err != nil {
			return
		}
		ev, _ := os.ReadFile(filepath.Join(d, "test.event"))
		name, _ := os.ReadFile(filepath.Join(d, "==="))
		_, errStat := os.Stat(filepath.Join(d, "error"))
		cases = append(cases, suiteCase{id: id, name: strings.TrimSpace(string(name)), in: in,
			events: string(ev), isError: errStat == nil})
	}
	for _, e := range ents {
		if !e.IsDir() || e.Name() == "name" || e.Name() == "tags" {
			continue
		}
		d := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(d, "in.yaml")); err == nil {
			read(e.Name(), d)
			continue
		}
		subs, _ := os.ReadDir(d)
		for _, s := range subs {
			if s.IsDir() {
				read(e.Name()+"/"+s.Name(), filepath.Join(d, s.Name()))
			}
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].id < cases[j].id })
	return cases
}

// escapeSuite writes a value as the suite's event format does.
func escapeSuite(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// renderEvents parses src and renders its events in the suite's format.
func renderEvents(src []byte) (string, []Event, error) {
	p := NewParser(src, Limits{})
	var b strings.Builder
	var evs []Event
	for {
		ev, err := p.Next()
		if err == io.EOF {
			return b.String(), evs, nil
		}
		if err != nil {
			return b.String(), evs, err
		}
		evs = append(evs, ev)
		props := func() {
			if ev.Anchor != "" {
				b.WriteString(" &" + ev.Anchor)
			}
			if ev.Tag != "" {
				b.WriteString(" <" + ev.Tag + ">")
			}
		}
		switch ev.Kind {
		case StreamStart:
			b.WriteString("+STR")
		case StreamEnd:
			b.WriteString("-STR")
		case DocumentStart:
			b.WriteString("+DOC")
			if ev.Explicit {
				b.WriteString(" ---")
			}
		case DocumentEnd:
			b.WriteString("-DOC")
			if ev.Explicit {
				b.WriteString(" ...")
			}
		case MappingStart:
			b.WriteString("+MAP")
			if ev.Flow {
				b.WriteString(" {}")
			}
			props()
		case MappingEnd:
			b.WriteString("-MAP")
		case SequenceStart:
			b.WriteString("+SEQ")
			if ev.Flow {
				b.WriteString(" []")
			}
			props()
		case SequenceEnd:
			b.WriteString("-SEQ")
		case Alias:
			b.WriteString("=ALI *" + ev.Value)
		case Scalar:
			b.WriteString("=VAL")
			props()
			b.WriteString(" " + string(":'\"|>"[ev.Style]) + escapeSuite(ev.Value))
		}
		b.WriteByte('\n')
	}
}

// suiteExceptions lists the cases that are known to fail, with the reason.
var suiteExceptions = map[string]string{}

func TestSuite(t *testing.T) {
	cases := loadSuite(t)
	if cases == nil {
		t.Skip("YAML test suite not found; set GOFASTHTML_YAML_SUITE")
	}
	var pass, fail, errPass, errTotal int
	for _, c := range cases {
		got, _, err := renderEvents(c.in)
		ok := false
		var why string
		if c.isError {
			errTotal++
			ok = err != nil
			why = "no error"
		} else {
			ok = err == nil && got == c.events
			if err != nil {
				why = err.Error()
			} else {
				why = "events differ:\n--- want\n" + c.events + "--- got\n" + got
			}
		}
		if _, ex := suiteExceptions[c.id]; ex {
			if ok {
				t.Errorf("%s (%s): listed as an exception but passes", c.id, c.name)
			}
			continue
		}
		if ok {
			pass++
			if c.isError {
				errPass++
			}
			continue
		}
		fail++
		t.Errorf("%s (%s): %s\n--- input\n%s", c.id, c.name, why, c.in)
	}
	t.Logf("suite: %d/%d passed (error cases %d/%d), %d exceptions", pass, len(cases), errPass, errTotal, len(suiteExceptions))
}

var _ = errors.Is
