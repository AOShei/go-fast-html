package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// subExtract extracts src as format and checks the document.
func subExtract(t *testing.T, format, src string, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	if opts == nil {
		opts = &gofastcore.Options{}
	}
	doc, err := (&Extractor{Format: format}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	return doc
}

// brief writes a block in one line: kind, text, where it came from,
// attributes and spans.
func brief(b gofastcore.Block) string {
	s := fmt.Sprintf("%s %q @%d+%d", b.Kind, b.Text, b.Loc.Offset, b.Loc.Length)
	if b.Loc.Anchor != "" {
		s += " #" + b.Loc.Anchor
	}
	var ks []string
	for k, v := range b.Attrs {
		ks = append(ks, k+"="+v)
	}
	sort.Strings(ks)
	if len(ks) > 0 {
		s += " {" + strings.Join(ks, " ") + "}"
	}
	for _, sp := range b.Spans {
		s += fmt.Sprintf(" [%d,%d)%s", sp.Start, sp.End, sp.Style)
	}
	return s
}

func briefs(p gofastcore.Part) []string {
	var out []string
	for _, b := range p.Blocks {
		out = append(out, brief(b))
	}
	return out
}

func warnCodes(doc *gofastcore.Document) string {
	var out []string
	for _, w := range doc.AllWarnings() {
		out = append(out, w.Code+"("+w.Severity.String()+"): "+w.Message)
	}
	return strings.Join(out, "\n")
}

func TestSubtitleBlocks(t *testing.T) {
	for _, tc := range []struct {
		name, format, src string
		spans             bool
		want              []string
		warn              []string // substrings of the warnings, in order
	}{
		{name: "srt basic", format: "srt",
			src: "1\n00:00:01,000 --> 00:00:02,500\nHello\nworld\n\n2\r\n00:00:03,000 --> 00:00:04,000\r\nBye\r\n",
			want: []string{
				`paragraph "Hello\nworld" @0+43 #1 {end=00:00:02.500 start=00:00:01.000}`,
				`paragraph "Bye" @45+37 #2 {end=00:00:04.000 start=00:00:03.000}`,
			}},
		{name: "srt omitted hours, point, short fraction, coordinates", format: "srt",
			src:  "7\n01:02.5 --> 1:02:03.25 X1:10 X2:20 Y1:30 Y2:40\nx\n",
			want: []string{`paragraph "x" @0+50 #7 {end=01:02:03.250 settings=X1:10 X2:20 Y1:30 Y2:40 start=00:01:02.500}`}},
		{name: "srt no number, CR line ends", format: "srt",
			src: "00:00:01,000 --> 00:00:02,000\rA\r\r00:00:03,000 --> 00:00:04,000\rB",
			want: []string{
				`paragraph "A" @0+31 {end=00:00:02.000 start=00:00:01.000}`,
				`paragraph "B" @33+31 {end=00:00:04.000 start=00:00:03.000}`,
			}},
		{name: "srt markup and override codes", format: "srt", spans: true,
			src:  "1\n00:00:01,000 --> 00:00:02,000\n{\\an8}<b>Bold</b> <font color=\"red\">red</font> {i}it{/i} <U>u</U>\na < b {x}\n",
			want: []string{`paragraph "Bold red it u\na < b {x}" @0+107 #1 {end=00:00:02.000 start=00:00:01.000} [0,4)bold [9,11)italic [12,13)underline`}},
		{name: "srt unclosed tag", format: "srt", spans: true,
			src:  "1\n00:00:01,000 --> 00:00:02,000\n<i>open\n",
			want: []string{`paragraph "open" @0+39 #1 {end=00:00:02.000 start=00:00:01.000} [0,4)italic`}},
		{name: "srt blank lines in text, number line that is text", format: "srt",
			src: "1\n00:00:01,000 --> 00:00:02,000\nOne\n\n\n42\n\nTwo\n2\n00:00:03,000 --> 00:00:04,000\nThree\n",
			want: []string{
				`paragraph "One\n\n42\n\nTwo" @0+45 #1 {end=00:00:02.000 start=00:00:01.000}`,
				`paragraph "Three" @46+37 #2 {end=00:00:04.000 start=00:00:03.000}`,
			}},
		{name: "srt text before the first cue, unparsed timings, empty cue", format: "srt",
			src: "Title\n\n1\nsoon --> later\nWords\n\n2\n00:00:05,000 --> 00:00:06,000\n\n3\n00:00:07,000 --> 00:00:08,000\nEnd\n",
			want: []string{
				`paragraph "Title" @0+5`,
				`paragraph "Words" @7+22 #1 {end=later start=soon}`,
				`paragraph "End" @64+35 #3 {end=00:00:08.000 start=00:00:07.000}`,
			},
			warn: []string{"1 cue has timings that do not parse", "1 cue has no text", "text before the first cue"}},
		{name: "srt ligature", format: "srt",
			src:  "1\n00:00:01,000 --> 00:00:02,000\n" + string(rune(0xFB01)) + "ne\n",
			want: []string{`paragraph "fine" @0+37 #1 {end=00:00:02.000 start=00:00:01.000}`}},
		{name: "vtt basic with ids and settings", format: "vtt",
			src: "WEBVTT\n\nintro\n00:01.000 --> 00:02.000 align:start line:0\nHi &amp; bye &lt;3&gt;\n\n00:00:03.000 --> 00:00:04.000\nNext\n",
			want: []string{
				`paragraph "Hi & bye <3>" @8+71 #intro {end=00:00:02.000 settings=align:start line:0 start=00:00:01.000}`,
				`paragraph "Next" @81+34 {end=00:00:04.000 start=00:00:03.000}`,
			}},
		{name: "vtt voices, ruby, classes, timestamps, lang, spans", format: "vtt", spans: true,
			src: "WEBVTT\n\n00:01.000 --> 00:02.000\n<v.loud Mary>Hi <b>there</b></v> <v Bob>yo</v> <v Mary>again\n<ruby>漢<rt>kan</rt></ruby> <c.x><i>it</i></c> <00:01.500>la<lang en>ter</lang> &nbsp;&#x41;&#66;&lrm;\n",
			want: []string{
				`paragraph ` + strconv.Quote("Hi there yo again\n漢kan it later "+string(rune(0xA0))+"AB"+string(rune(0x200E))) +
					` @8+188 {end=00:00:02.000 start=00:00:01.000 voice=Mary, Bob} [3,8)bold [25,27)italic`,
			}},
		{name: "vtt nested and unclosed tags", format: "vtt", spans: true,
			src: "WEBVTT\n\n00:01.000 --> 00:02.000\n<b><i>a</b>b</i>c<u>d\n",
			// As the spec's tree has it, </b> does not close the open
			// <i>, and is ignored: the bold runs to the end.
			want: []string{`paragraph "abcd" @8+45 {end=00:00:02.000 start=00:00:01.000} [0,2)italic [3,4)underline [0,4)bold`}},
		{name: "vtt unknown tag and lone <", format: "vtt",
			src:  "WEBVTT\n\n00:01.000 --> 00:02.000\n<x>a</x> < b\n",
			want: []string{`paragraph "a < b" @8+36 {end=00:00:02.000 start=00:00:01.000}`}},
		{name: "vtt payload line with an arrow ends the cue", format: "vtt",
			src: "WEBVTT\n\n00:01.000 --> 00:02.000\nfirst\n00:03.000 --> 00:04.000\nsecond\n",
			want: []string{
				`paragraph "first" @8+29 {end=00:00:02.000 start=00:00:01.000}`,
				`paragraph "second" @38+30 {end=00:00:04.000 start=00:00:03.000}`,
			}},
		{name: "vtt NOTE, STYLE, REGION, other block", format: "vtt",
			src: "WEBVTT\n\nSTYLE\n::cue { color: red }\n\nREGION\nid:r1 width:40%\n\nNOTE a comment\nover two lines\n\nstray text\n\n00:01.000 --> 00:02.000 region:r1\nx\n",
			want: []string{
				`note "a comment\nover two lines" @60+29 {stored=markup_comment}`,
				`paragraph "stray text" @91+10`,
				`paragraph "x" @103+35 {end=00:00:02.000 settings=region:r1 start=00:00:01.000}`,
			},
			warn: []string{"1 block is not a cue"}},
		{name: "vtt missing signature", format: "vtt",
			src:  "00:01.000 --> 00:02.000\nx\n",
			want: []string{`paragraph "x" @0+25 {end=00:00:02.000 start=00:00:01.000}`},
			warn: []string{"does not begin with the WebVTT signature"}},
		{name: "srt CR CR LF is one line end", format: "srt",
			src: "1\r\r\n00:00:01,000 --> 00:00:02,000\r\r\nA\r\r\nB\r\r\n\r\r\n2\r\r\n00:00:03,000 --> 00:00:04,000\r\r\nC\r\r\n",
			want: []string{
				`paragraph "A\nB" @0+41 #1 {end=00:00:02.000 start=00:00:01.000}`,
				`paragraph "C" @47+37 #2 {end=00:00:04.000 start=00:00:03.000}`,
			}},
		{name: "srt byte order mark before a cue number", format: "srt",
			src:  "\r\n" + string(rune(0xFEFF)) + "1\r\n00:00:01,000 --> 00:00:02,000\r\nA\r\n",
			want: []string{`paragraph "A" @2+38 #1 {end=00:00:02.000 start=00:00:01.000}`}},
		{name: "vtt CR CR LF is one line end", format: "vtt",
			src: "WEBVTT\r\r\n\r\r\n00:01.000 --> 00:02.000\r\r\nA\r\r\nB\r\r\n\r\r\n00:03.000 --> 00:04.000\r\r\nC\r\r\n",
			want: []string{
				`paragraph "A\nB" @12+31 {end=00:00:02.000 start=00:00:01.000}`,
				`paragraph "C" @49+27 {end=00:00:04.000 start=00:00:03.000}`,
			}},
		{name: "vtt line of spaces is a line of the cue", format: "vtt",
			src:  "WEBVTT\n\n00:01.000 --> 00:02.000\nA\n \t \nB\n",
			want: []string{`paragraph "A\n\nB" @8+31 {end=00:00:02.000 start=00:00:01.000}`}},
		{name: "vtt unclosed < and ones before a >", format: "vtt",
			src:  "WEBVTT\n\n00:01.000 --> 00:02.000\na<<b>c < d <\n",
			want: []string{`paragraph "ac < d <" @8+36 {end=00:00:02.000 start=00:00:01.000}`}},
		{name: "srt unclosed override and font", format: "srt", spans: true,
			src:  "1\n00:00:01,000 --> 00:00:02,000\n{\\an8}a {\\b <i>b</i> <font x\n",
			want: []string{`paragraph "a {\\b b <font x" @0+60 #1 {end=00:00:02.000 start=00:00:01.000} [6,7)italic`}},
		{name: "vtt header line with an arrow begins a cue", format: "vtt",
			src:  "WEBVTT\nKind: captions\n00:01.000 --> 00:02.000\nx\n",
			want: []string{`paragraph "x" @22+25 {end=00:00:02.000 start=00:00:01.000}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := subExtract(t, tc.format, tc.src, &gofastcore.Options{Spans: tc.spans})
			got := briefs(doc.Parts[0])
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			ws := warnCodes(doc)
			for _, w := range tc.warn {
				if !strings.Contains(ws, w) {
					t.Errorf("no warning %q in:\n%s", w, ws)
				}
			}
			if len(tc.warn) == 0 && ws != "" {
				t.Errorf("warnings:\n%s", ws)
			}
			for _, b := range doc.Parts[0].Blocks {
				if end := b.Loc.Offset + b.Loc.Length; end > int64(len(tc.src)) {
					t.Errorf("Loc past the input: %s", brief(b))
				}
			}
		})
	}
}

func TestVTTHeaderAndStored(t *testing.T) {
	src := "\xEF\xBB\xBFWEBVTT - Some title\nKind: captions\nLanguage: en-GB\nX-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:0\n\n" +
		"STYLE\n::cue(b) { color: blue }\n\nREGION\nid:a\nwidth:50%\n\nREGION\nid:b\n\n00:01.000 --> 00:02.000\nx\n"
	doc := subExtract(t, "vtt", src, nil)
	if doc.Metadata.Language != "en-GB" {
		t.Errorf("language %q", doc.Metadata.Language)
	}
	want := map[string]string{"vtt.header": "- Some title\nX-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:0",
		"vtt.kind": "captions", "vtt.language": "en-GB", "vtt.regions": "id:a\nwidth:50%\nid:b"}
	for k, v := range want {
		if doc.Metadata.Extra[k] != v {
			t.Errorf("Extra[%s] = %q, want %q", k, doc.Metadata.Extra[k], v)
		}
	}
	if len(doc.Parts) != 2 || doc.Parts[1].Kind != gofastcore.PartStored {
		t.Fatalf("parts: %d", len(doc.Parts))
	}
	if got := brief(doc.Parts[1].Blocks[0]); got != `code "::cue(b) { color: blue }" @99+30 {language=css stored=stylesheet}` {
		t.Errorf("stored: %s", got)
	}
	// With Options.Parts the stored part is left out.
	doc = subExtract(t, "vtt", src, &gofastcore.Options{Parts: gofastcore.PartRange{First: 1, Last: 1}})
	if len(doc.Parts) != 1 || len(doc.Parts[0].Blocks) != 1 {
		t.Errorf("Parts 1-1: %d parts", len(doc.Parts))
	}
	_, err := (&Extractor{Format: "vtt"}).Extract(context.Background(), strings.NewReader(src), int64(len(src)),
		&gofastcore.Options{Parts: gofastcore.PartRange{First: 2, Last: 2}})
	if !errors.Is(err, gofastcore.ErrNoPartsSelected) {
		t.Errorf("Parts 2-2: %v", err)
	}
	// Not a language tag.
	doc = subExtract(t, "vtt", "WEBVTT\nLanguage: English (auto)\n\n00:01.000 --> 00:02.000\nx\n", nil)
	if doc.Metadata.Language != "" || doc.Metadata.Extra["vtt.language"] != "English (auto)" {
		t.Errorf("language %q, extra %q", doc.Metadata.Language, doc.Metadata.Extra["vtt.language"])
	}
}

func TestSubtitleDetect(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a.srt", "", "srt"},
		{"a.SRT", "", "srt"},
		{"a.vtt", "", "vtt"},
		{"a.webvtt", "", "vtt"},
		{"", "WEBVTT\n\n00:01.000 --> 00:02.000\nx", "vtt"},
		{"", "\xEF\xBB\xBFWEBVTT", "vtt"},
		{"", "WEBVTT\tx", "vtt"},
		{"", "\xFF\xFEW\x00E\x00B\x00V\x00T\x00T\x00\r\x00", "vtt"},
		{"", "WEBVTTX\n", "txt"},
		{"", "WEBVTT\f\n", "txt"},
		{"", "1\n00:00:01,000 --> 00:00:02,000\nx\n", "txt"},
		{"a.txt", "WEBVTT\n", "txt"},
	} {
		if got := Detect(tc.name, []byte(tc.src)); got != tc.want {
			t.Errorf("Detect(%q, %q) = %s, want %s", tc.name, tc.src, got, tc.want)
		}
	}
	doc := subExtract(t, "", "WEBVTT\n\n00:01.000 --> 00:02.000\nx", nil)
	if doc.Source.Format != "vtt" || doc.Source.MIME != "text/vtt" {
		t.Errorf("source %+v", doc.Source)
	}
}

func TestSubtitleEncodings(t *testing.T) {
	// UTF-16 with a byte order mark: offsets are of the input's bytes.
	src := "\xFF\xFE" + utf16le("1\r\n00:00:01,000 --> 00:00:02,000\r\nhé\r\n")
	doc := subExtract(t, "srt", src, nil)
	if got := briefs(doc.Parts[0]); len(got) != 1 || got[0] != `paragraph "hé" @2+72 #1 {end=00:00:02.000 start=00:00:01.000}` {
		t.Errorf("utf-16: %q", got)
	}
	// UTF-32, both byte orders, with a character outside the BMP.
	for _, be := range []bool{false, true} {
		var b []byte
		if be {
			b = []byte{0, 0, 0xFE, 0xFF}
		} else {
			b = []byte{0xFF, 0xFE, 0, 0}
		}
		for _, r := range "1\n00:00:01,000 --> 00:00:02,000\nx" + string(rune(0x1F600)) + "y\n" {
			if be {
				b = append(b, byte(r>>24), byte(r>>16), byte(r>>8), byte(r))
			} else {
				b = append(b, byte(r), byte(r>>8), byte(r>>16), byte(r>>24))
			}
		}
		doc := subExtract(t, "srt", string(b), nil)
		want := `paragraph "x` + string(rune(0x1F600)) + `y" @4+140 #1 {end=00:00:02.000 start=00:00:01.000}`
		if got := briefs(doc.Parts[0]); len(got) != 1 || got[0] != want || warnCodes(doc) != "" {
			t.Errorf("utf-32 be=%v: %q %s", be, got, warnCodes(doc))
		}
	}
	// Bytes of no certain encoding: refused, as plain text is.
	src = "1\n00:00:01,000 --> 00:00:02,000\na\x81\x8D\x8F\x90\x9Db\n"
	if _, err := (&Extractor{Format: "srt"}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), nil); !errors.Is(err, ErrEncodingUnknown) {
		t.Errorf("undetermined: %v", err)
	}
	// windows-1252 given as the transport encoding.
	src = "1\n00:00:01,000 --> 00:00:02,000\ncaf\xE9\n"
	doc, err := (&Extractor{Format: "srt", Charset: "windows-1252"}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Parts[0].Blocks[0].Text != "café" {
		t.Errorf("windows-1252: %q", doc.Parts[0].Blocks[0].Text)
	}
	// Control characters are dropped and counted.
	doc = subExtract(t, "srt", "1\n00:00:01,000 --> 00:00:02,000\na\x00b\x1Bc\n", nil)
	if doc.Parts[0].Blocks[0].Text != "abc" || !doc.Parts[0].Has(gofastcore.WarnTextControlCharactersRemoved) {
		t.Errorf("controls: %q %s", doc.Parts[0].Blocks[0].Text, warnCodes(doc))
	}
}

func utf16le(s string) string {
	var b []byte
	for _, r := range s {
		b = append(b, byte(r), byte(r>>8))
	}
	return string(b)
}

func TestSubtitleNoText(t *testing.T) {
	for _, format := range []string{"srt", "vtt"} {
		doc := subExtract(t, format, "", nil)
		if !doc.Parts[0].Has(gofastcore.WarnPartNoText) {
			t.Errorf("%s: %s", format, warnCodes(doc))
		}
	}
}

func TestSubtitleBudget(t *testing.T) {
	var b strings.Builder
	for i := range 100 {
		fmt.Fprintf(&b, "%d\n00:00:%02d,000 --> 00:00:%02d,500\nline %d\n\n", i+1, i%60, i%60, i)
	}
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 10 * (gofastcore.BlockCost + 60)}}
	doc := subExtract(t, "srt", b.String(), opts)
	n := len(doc.Parts[0].Blocks)
	if n == 0 || n >= 100 {
		t.Errorf("%d blocks", n)
	}
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("no limit.exceeded: %s", warnCodes(doc))
	}
	// The decode budget.
	opts = &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 100}}
	doc = subExtract(t, "srt", b.String(), opts)
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) || len(doc.Parts[0].Blocks) == 0 {
		t.Errorf("decode: %d blocks, %s", len(doc.Parts[0].Blocks), warnCodes(doc))
	}
}

// TestVTTBounds checks that the bounds which keep WebVTT reading linear
// still cut, and say so: tag nesting, header text, a repeated header key,
// the voices of a cue.
func TestVTTBounds(t *testing.T) {
	t.Run("nesting", func(t *testing.T) {
		src := "WEBVTT\n\n00:01.000 --> 00:02.000\n<b><i><u>x</u>y</i>z</b>\n"
		doc, err := (&Extractor{Format: "vtt", MaxDepth: 2}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), &gofastcore.Options{Spans: true})
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(briefs(doc.Parts[0]), "\n")
		if want := `paragraph "xyz" @8+48 {end=00:00:02.000 start=00:00:01.000} [0,2)italic [0,3)bold`; got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
		if ws := warnCodes(doc); !strings.Contains(ws, "limit.exceeded(error): 1 cue has tags nested more than 2 deep") {
			t.Errorf("warnings:\n%s", ws)
		}
	})
	limited := func(t *testing.T, doc *gofastcore.Document, what string, n, bound int) {
		t.Helper()
		if n > bound || n < bound-8 {
			t.Errorf("%s: %d bytes, bound %d", what, n, bound)
		}
		if !strings.Contains(warnCodes(doc), "limit.exceeded(error)") {
			t.Errorf("%s: no limit.exceeded:\n%s", what, warnCodes(doc))
		}
	}
	t.Run("header text", func(t *testing.T) {
		doc := subExtract(t, "vtt", "WEBVTT\n"+strings.Repeat("xxxxxxxx\n", 20000)+"\n00:01.000 --> 00:02.000\nx\n", nil)
		limited(t, doc, "vtt.header", len(doc.Metadata.Extra["vtt.header"]), 64<<10)
	})
	t.Run("repeated key", func(t *testing.T) {
		doc := subExtract(t, "vtt", "WEBVTT\n"+strings.Repeat("a: bcdefgh\n", 20000)+"\n00:01.000 --> 00:02.000\nx\n", nil)
		limited(t, doc, "vtt.a", len(doc.Metadata.Extra["vtt.a"]), 64<<10)
	})
	t.Run("voices", func(t *testing.T) {
		var sb strings.Builder
		for i := range 3000 {
			fmt.Fprintf(&sb, "<v %d>", i)
		}
		doc := subExtract(t, "vtt", "WEBVTT\n\n00:01.000 --> 00:02.000\n"+sb.String()+"x\n", nil)
		v := doc.Parts[0].Blocks[0].Attrs["voice"]
		limited(t, doc, "voice", len(v), maxSubtitleAttr)
		if !strings.HasPrefix(v, "0, 1, 2, ") {
			t.Errorf("voice %.40q", v)
		}
	})
}

func TestSubtitleCueBound(t *testing.T) {
	if testing.Short() {
		t.Skip("holds 8 MiB")
	}
	src := "1\n00:00:01,000 --> 00:00:02,000\n" + strings.Repeat("word ", 1<<20) + "\n" + strings.Repeat("more ", 1<<20) + "\n"
	doc := subExtract(t, "srt", src, nil)
	if len(doc.Parts[0].Blocks) != 1 || !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("%d blocks, %s", len(doc.Parts[0].Blocks), warnCodes(doc))
	}
}

func TestSubtitleCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := strings.Repeat("1\n00:00:01,000 --> 00:00:02,000\nx\n\n", 5000)
	for _, format := range []string{"srt", "vtt"} {
		_, err := (&Extractor{Format: format}).Extract(ctx, strings.NewReader(src), int64(len(src)), nil)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v", format, err)
		}
	}
	// Cancelled while reading: the parsers check the context as they go.
	var cut bytes.Buffer
	for range 5000 {
		cut.WriteString("00:01.000 --> 00:02.000\nx\n\n")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	calls := 0
	ctx3 := &countCtx{Context: ctx2, after: 3, cancel: cancel2, calls: &calls}
	_, err := (&Extractor{Format: "vtt"}).Extract(ctx3, bytes.NewReader(cut.Bytes()), int64(cut.Len()), nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("vtt while reading: %v", err)
	}
}

// FuzzSubtitles reads arbitrary input as SubRip and as WebVTT under small
// limits: it never panics, the document passes Validate, and every Loc is
// inside the input.
//
//	go test -run XXX -fuzz FuzzSubtitles -fuzztime 20s -fuzzminimizetime 0s
func FuzzSubtitles(f *testing.F) {
	for _, s := range []string{
		"1\n00:00:01,000 --> 00:00:02,000\n<b>a</b>{\\an8}{i}b\n\nc\n2\n00:00:03,000 --> x\n",
		"WEBVTT x\nKind: captions\nLanguage: en\n\nSTYLE\n::cue{}\n\nREGION\nid:a\n\nNOTE n\n\nid\n00:01.000 --> 00:02.000 line:0\n<v A>x &amp;<ruby>y<rt>z</rt></ruby><00:01.5>\n",
		"\xFF\xFE1\x00\n\x00",
		"\xEF\xBB\xBF\xEF\xAC\x81<i>\r\r-->\r",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		for i, opts := range []*gofastcore.Options{
			{Spans: true},
			{Limits: gofastcore.Limits{MaxTextBytes: 1024, MaxDecodedBytes: 2048}},
		} {
			for _, format := range []string{"srt", "vtt"} {
				e := &Extractor{Format: format}
				doc, err := e.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
				if errors.Is(err, ErrEncodingUnknown) {
					e.Charset = "windows-1252"
					if format == "vtt" {
						continue // vtt takes no transport encoding
					}
					doc, err = e.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), opts)
				}
				if err != nil {
					t.Fatalf("%s %d: %v", format, i, err)
				}
				if err := doc.Validate(); err != nil {
					t.Fatalf("%s %d: %v", format, i, err)
				}
				for _, p := range doc.Parts {
					for _, blk := range p.Blocks {
						if blk.Loc.Offset < 0 || blk.Loc.Length < 0 || blk.Loc.Offset+blk.Loc.Length > int64(len(b)) {
							t.Fatalf("%s %d: Loc outside the input: %s", format, i, brief(blk))
						}
					}
				}
			}
		}
	})
}

// countCtx cancels itself after Err has been asked a number of times.
type countCtx struct {
	context.Context
	after  int
	cancel context.CancelFunc
	calls  *int
}

func (c *countCtx) Err() error {
	if *c.calls++; *c.calls > c.after {
		c.cancel()
	}
	return c.Context.Err()
}
