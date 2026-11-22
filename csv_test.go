package gofasthtml

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

// csvDoc extracts src with e and checks the document.
func csvDoc(t *testing.T, e *Extractor, name, src string, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	if opts == nil {
		opts = &gofastcore.Options{}
	}
	opts.Name = name
	doc, err := e.Extract(context.Background(), strings.NewReader(src), int64(len(src)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(doc.Parts) != 1 || doc.Parts[0].Kind != gofastcore.PartSheet || doc.Parts[0].Number != 1 {
		t.Fatalf("parts: %+v", doc.Parts)
	}
	return doc
}

// csvRows gives the rows of all the table blocks of the sheet, joined.
func csvRows(t *testing.T, doc *gofastcore.Document) [][]string {
	t.Helper()
	rows := [][]string{}
	for i, b := range doc.Parts[0].Blocks {
		if b.Kind != gofastcore.KindTable {
			t.Fatalf("block %d is %v", i, b.Kind)
		}
		if b.Continued != (i > 0) {
			t.Errorf("block %d: Continued %v", i, b.Continued)
		}
		rows = append(rows, b.Table.Rows...)
	}
	return rows
}

func csvWarnings(doc *gofastcore.Document, code string) []string {
	var out []string
	for _, w := range doc.Parts[0].Warnings {
		if w.Code == code {
			out = append(out, w.Message)
		}
	}
	for _, w := range doc.Warnings {
		if w.Code == code {
			out = append(out, w.Message)
		}
	}
	return out
}

func TestCSVDetect(t *testing.T) {
	for name, want := range map[string]string{"a.csv": "csv", "A.CSV": "csv", "b.tsv": "tsv", "c.tab": "tsv", "d.txt": "txt", "": "txt"} {
		if got := Detect(name, []byte("a,b,c\n1,2,3\n")); got != want {
			t.Errorf("Detect(%q) = %q, want %q", name, got, want)
		}
	}
	doc := csvDoc(t, &Extractor{}, "dir/data.file.csv", "a,b\n", nil)
	if doc.Source.Format != "csv" || doc.Source.MIME != "text/csv" || doc.Parts[0].Name != "data.file" {
		t.Errorf("source %+v, name %q", doc.Source, doc.Parts[0].Name)
	}
	doc = csvDoc(t, &Extractor{}, "x.tsv", "a\tb\n", nil)
	if doc.Source.MIME != "text/tab-separated-values" || !reflect.DeepEqual(csvRows(t, doc), [][]string{{"a", "b"}}) {
		t.Errorf("tsv: %+v %q", doc.Source, csvRows(t, doc))
	}
	if !reflect.DeepEqual((&Extractor{}).Formats(), []string{"html", "md", "txt", "epub", "csv", "tsv"}) {
		t.Error((&Extractor{}).Formats())
	}
}

func TestCSVDelimiter(t *testing.T) {
	for _, c := range []struct {
		name, format string
		delim        rune
		src          string
		want         [][]string
		d, how       string
	}{
		{"comma", "csv", 0, "a,b\n1,2\n", [][]string{{"a", "b"}, {"1", "2"}}, ",", "detected"},
		{"semicolon", "csv", 0, "Name;Price\nTea;1,50\nCake;2,75\n", [][]string{{"Name", "Price"}, {"Tea", "1,50"}, {"Cake", "2,75"}}, ";", "detected"},
		{"tab", "csv", 0, "a\tb,c\n1\t2\n", [][]string{{"a", "b,c"}, {"1", "2"}}, "\t", "detected"},
		{"pipe", "csv", 0, "a|b|c\n1|2|3\n", [][]string{{"a", "b", "c"}, {"1", "2", "3"}}, "|", "detected"},
		{"quoted commas", "csv", 0, "\"a;b\",\"c;d\"\n\"e;f\",g\n", [][]string{{"a;b", "c;d"}, {"e;f", "g"}}, ",", "detected"},
		{"one column", "csv", 0, "a\nb\n", [][]string{{"a"}, {"b"}}, ",", "detected"},
		{"tsv", "tsv", 0, "a,b\tc\n", [][]string{{"a,b", "c"}}, "\t", "format"},
		{"option", "csv", ';', "a;b,c\n", [][]string{{"a", "b,c"}}, ";", "option"},
		{"option tsv", "tsv", ',', "a\tb,c\n", [][]string{{"a\tb", "c"}}, ",", "option"},
		{"sep", "csv", ',', "sep=;\na;b,c\n", [][]string{{"a", "b,c"}}, ";", "declared"},
		{"sep crlf", "tsv", 0, "SEP=|\r\na|b\r\n", [][]string{{"a", "b"}}, "|", "declared"},
		{"sep alone", "csv", 0, "sep=;", nil, ";", "declared"},
		{"sep multibyte", "csv", 0, "sep=§\na§b\n", [][]string{{"a", "b"}}, "§", "declared"},
		{"not sep", "csv", 0, "sep=;;\n", [][]string{{"sep=", "", ""}}, ";", "detected"},
		{"sep quote", "csv", 0, "sep=\"\n", [][]string{{"sep=\""}}, ",", "detected"},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := csvDoc(t, &Extractor{Format: c.format, Delimiter: c.delim}, "", c.src, nil)
			if c.want == nil {
				c.want = [][]string{}
			}
			if got := csvRows(t, doc); !reflect.DeepEqual(got, c.want) {
				t.Errorf("rows %q, want %q", got, c.want)
			}
			if x := doc.Metadata.Extra; x["delimiter"] != c.d || x["delimiter_source"] != c.how {
				t.Errorf("extra %q", x)
			}
		})
	}
	for _, d := range []rune{'"', '\n', '\r', -1, 0xD800} {
		_, err := (&Extractor{Format: "csv", Delimiter: d}).Extract(context.Background(), strings.NewReader("a"), 1, nil)
		if !errors.Is(err, gofastcore.ErrUnsupported) {
			t.Errorf("delimiter %q: %v", d, err)
		}
	}
}

func TestCSVQuoting(t *testing.T) {
	zwsp := string(rune(0x200B)) // kept as it is: invisible, not a control character
	for _, c := range []struct {
		name, src string
		want      [][]string
		repaired  int
	}{
		{"rfc", "a,\"b,c\",\"d\"\"e\",\"\"\n", [][]string{{"a", "b,c", "d\"e", ""}}, 0},
		{"line breaks in quotes", "\"a\nb\",\"c\r\nd\",\"e\rf\"\n", [][]string{{"a\nb", "c\nd", "e\nf"}}, 0},
		{"crlf", "a,b\r\nc,d\r\n", [][]string{{"a", "b"}, {"c", "d"}}, 0},
		{"cr", "a,b\rc,d\r", [][]string{{"a", "b"}, {"c", "d"}}, 0},
		{"no final newline", "a,b\nc,", [][]string{{"a", "b"}, {"c", ""}}, 0},
		{"spaces kept", " a , \" b \" \n", [][]string{{" a ", " \" b \" "}}, 1},
		{"stray quote", "a\"b,c\nd,e\"f\"\ng,h\n", [][]string{{"a\"b", "c"}, {"d", "e\"f\""}, {"g", "h"}}, 2},
		{"after closing quote", "\"ab\"c,d\n", [][]string{{"abc", "d"}}, 1},
		{"after closing quote, quote", "\"ab\"\"\"c\"d,e\n", [][]string{{"ab\"c\"d", "e"}}, 1},
		{"unclosed", "a,b\n\"c,d\ne,f\n", [][]string{{"a", "b"}, {"c,d\ne,f\n", ""}}, 1},
		{"formula", "=SUM(A1:A2),=cmd|' /C calc'!A0\n", [][]string{{"=SUM(A1:A2)", "=cmd|' /C calc'!A0"}}, 0},
		{"controls and ligatures", "a\x00b,\"\x1b[31m\u0085x\"," + zwsp + "\xef\xac\x81\xef\xac\x86\t\n", [][]string{{"ab", "[31mx", zwsp + "fist\t"}}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := csvDoc(t, &Extractor{Format: "csv", Delimiter: ','}, "", c.src, nil)
			if got := csvRows(t, doc); !reflect.DeepEqual(got, c.want) {
				t.Errorf("rows %q, want %q", got, c.want)
			}
			w := csvWarnings(doc, gofastcore.WarnInputRepaired)
			if c.repaired == 0 && len(w) != 0 || c.repaired > 0 && (len(w) != 1 || !strings.HasPrefix(w[0], strconv.Itoa(c.repaired)+" row")) {
				t.Errorf("repaired: %q", w)
			}
		})
	}
}

func TestCSVGrid(t *testing.T) {
	doc := csvDoc(t, &Extractor{Format: "csv"}, "", "a,b\n1\n\n,,,,\n2,3,4\n\n,,,,,,,,\n\r\n", nil)
	want := [][]string{{"a", "b", "", "", ""}, {"1", "", "", "", ""}, {"", "", "", "", ""}, {"", "", "", "", ""}, {"2", "3", "4", "", ""}}
	if got := csvRows(t, doc); !reflect.DeepEqual(got, want) {
		t.Errorf("rows %q", got)
	}
	b := doc.Parts[0].Blocks[0]
	if b.Loc.Range != "A1:E5" || b.Table.HeaderRows != 0 || b.Origin != gofastcore.OriginNative {
		t.Errorf("range %q header %d origin %v", b.Loc.Range, b.Table.HeaderRows, b.Origin)
	}
	doc = csvDoc(t, &Extractor{Format: "csv", Header: true}, "", "h1,h2\nx,y\n", nil)
	if doc.Parts[0].Blocks[0].Table.HeaderRows != 1 {
		t.Error("Header: no header row")
	}
	doc = csvDoc(t, &Extractor{Format: "csv"}, "", "x", nil)
	if b := doc.Parts[0].Blocks[0]; b.Loc.Range != "A1" {
		t.Errorf("range %q", b.Loc.Range)
	}
}

func TestCSVEmpty(t *testing.T) {
	for _, src := range []string{"", "\n\n\r\n", ",,,\n\"\",\n", "\xEF\xBB\xBF", "sep=;\n"} {
		doc := csvDoc(t, &Extractor{Format: "csv"}, "", src, nil)
		if len(doc.Parts[0].Blocks) != 0 || len(csvWarnings(doc, gofastcore.WarnPartNoText)) != 1 {
			t.Errorf("%q: %+v", src, doc.Parts[0])
		}
	}
}

func TestCSVEncoding(t *testing.T) {
	utf16 := func(s string, be bool) string {
		b := []byte{0xFF, 0xFE}
		if be {
			b = []byte{0xFE, 0xFF}
		}
		for _, r := range s {
			if be {
				b = append(b, byte(r>>8), byte(r))
			} else {
				b = append(b, byte(r), byte(r>>8))
			}
		}
		return string(b)
	}
	want := [][]string{{"café", "€"}}
	for name, c := range map[string]struct{ src, charset string }{
		"utf-8":            {"café;€\n", ""},
		"utf-8 bom":        {"\xEF\xBB\xBFcafé;€\n", ""},
		"utf-16le":         {utf16("café;€\r\n", false), ""},
		"utf-16be":         {utf16("café;€\r\n", true), ""},
		"windows-1252":     {"caf\xE9;\x80\n", ""},
		"transport":        {"caf\xE9;\x80\n", "windows-1252"},
		"bom beats label":  {"\xEF\xBB\xBFcafé;€\n", "iso-8859-5"},
		"sep after bom":    {"\xEF\xBB\xBFsep=;\ncafé;€\n", ""},
		"utf-16 sep first": {utf16("sep=;\ncafé;€\n", false), ""},
	} {
		t.Run(name, func(t *testing.T) {
			doc := csvDoc(t, &Extractor{Format: "csv", Charset: c.charset}, "", c.src, nil)
			if got := csvRows(t, doc); !reflect.DeepEqual(got, want) {
				t.Errorf("rows %q", got)
			}
		})
	}
}

func TestCSVSplit(t *testing.T) {
	var sb strings.Builder
	for i := range 2500 {
		sb.WriteString(strconv.Itoa(i) + ",x\n")
	}
	doc := csvDoc(t, &Extractor{Format: "csv", Header: true}, "", sb.String(), nil)
	blocks := doc.Parts[0].Blocks
	if len(blocks) != 3 {
		t.Fatalf("%d blocks", len(blocks))
	}
	for i, r := range []string{"A1:B1000", "A1001:B2000", "A2001:B2500"} {
		if blocks[i].Loc.Range != r || blocks[i].Continued != (i > 0) {
			t.Errorf("block %d: %q %v", i, blocks[i].Loc.Range, blocks[i].Continued)
		}
	}
	if blocks[0].Table.HeaderRows != 1 || blocks[1].Table.HeaderRows != 0 {
		t.Error("header rows")
	}
	if rows := csvRows(t, doc); len(rows) != 2500 || rows[2499][0] != "2499" {
		t.Errorf("%d rows", len(rows))
	}
	// A wide table has fewer rows to a block.
	row := strings.Repeat("x,", 4095) + "x\n"
	doc = csvDoc(t, &Extractor{Format: "csv"}, "", strings.Repeat(row, 300), nil)
	if b := doc.Parts[0].Blocks; len(b) != 2 || b[0].Loc.Range != "A1:FAN256" || b[1].Loc.Range != "A257:FAN300" {
		t.Errorf("wide: %d blocks", len(b))
	}
}

// TestCSVBounds checks that each bound gives exactly one warning, with
// the right count.
func TestCSVBounds(t *testing.T) {
	// One huge field, quoted, with a character across the bound, and one
	// unquoted.
	straddle := strings.Repeat("y", MaxCSVField-1) + "é" + strings.Repeat("y", 9)
	huge := strings.Repeat("y", MaxCSVField+10)
	doc := csvDoc(t, &Extractor{Format: "csv"}, "", "\""+straddle+"\",b\nx,"+huge+"\n", nil)
	w := csvWarnings(doc, gofastcore.WarnLimitExceeded)
	if want := "fields longer than 1048576 bytes were cut there (2 fields, 21 bytes left out)"; len(w) != 1 || w[0] != want {
		t.Errorf("field: %q", w)
	}
	rows := csvRows(t, doc)
	if len(rows) != 2 || len(rows[0][0]) != MaxCSVField-1 || rows[0][1] != "b" || len(rows[1][1]) != MaxCSVField {
		t.Errorf("field: %d rows", len(rows))
	}

	// A row wider than the bound.
	wide := strings.Repeat("a,", MaxCSVColumns+4) + "z\n1,2\n"
	doc = csvDoc(t, &Extractor{Format: "csv"}, "", wide, nil)
	w = csvWarnings(doc, gofastcore.WarnLimitExceeded)
	if want := "fields past column 16384 were not read (5 fields)"; len(w) != 1 || w[0] != want {
		t.Errorf("columns: %q", w)
	}
	if rows := csvRows(t, doc); len(rows) != 2 || len(rows[0]) != MaxCSVColumns || rows[1][1] != "2" {
		t.Errorf("columns: %d rows", len(rows))
	}

	// The text budget: rows of 2 cells of 1 byte cost 34 bytes each.
	src := strings.Repeat("a,b\n", 100)
	doc = csvDoc(t, &Extractor{Format: "csv"}, "", src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: gofastcore.BlockCost + 34*10}})
	w = csvWarnings(doc, gofastcore.WarnLimitExceeded)
	if want := "the limit on the amount of text was reached; the sheet is cut off there (90 rows not read)"; len(w) != 1 || w[0] != want {
		t.Errorf("budget: %q", w)
	}
	if rows := csvRows(t, doc); len(rows) != 10 || len(csvWarnings(doc, gofastcore.WarnPartNoText)) != 0 {
		t.Errorf("budget: %d rows", len(rows))
	}

	// The decode budget.
	doc = csvDoc(t, &Extractor{Format: "csv"}, "", src, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 40}})
	w = csvWarnings(doc, gofastcore.WarnLimitExceeded)
	if len(w) != 1 || !strings.Contains(w[0], "decoded bytes") || len(csvRows(t, doc)) != 10 {
		t.Errorf("decode: %q", w)
	}
}

// TestCSVWorstCase reads inputs built to cost much for their size.
func TestCSVWorstCase(t *testing.T) {
	n := 1 << 20
	if testing.Short() {
		n = 1 << 16
	}
	for name, src := range map[string]string{
		"unclosed quote": "\"" + strings.Repeat("a,\n", n),
		"quotes":         strings.Repeat("\"", n),
		"doubled quotes": "\"" + strings.Repeat("\"\"", n),
		"stray quotes":   strings.Repeat("a\"", n),
		"delimiters":     strings.Repeat(",", n),
		"blank lines":    strings.Repeat("\r\n", n) + "x",
		"wide then long": strings.Repeat(",", MaxCSVColumns) + "\n" + strings.Repeat("x\n", n/4),
		"small rows":     strings.Repeat("a\n", n),
		"sniff":          strings.Repeat("a;b|c\td,e\n", n/8),
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			doc := csvDoc(t, &Extractor{Format: "csv"}, "", src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 8 << 20}})
			if d := time.Since(start); d > 5*raceSlowdown*time.Second {
				t.Errorf("%d bytes took %v", len(src), d)
			}
			_ = doc
		})
	}
}
