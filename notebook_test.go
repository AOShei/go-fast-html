package gofasthtml

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

// nb is a notebook of format version 4.5 with these cells.
func nb(cells ...map[string]any) string {
	if cells == nil {
		cells = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{
		"nbformat": 4, "nbformat_minor": 5,
		"metadata": map[string]any{
			"kernelspec":    map[string]any{"name": "python3", "display_name": "Python 3", "language": "python"},
			"language_info": map[string]any{"name": "python", "version": "3.12.1"},
			"title":         "Analysis",
			"authors":       []any{map[string]any{"name": "Ada"}, map[string]any{"name": "Grace"}},
		},
		"cells": cells,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func mdCell(id string, src ...string) map[string]any {
	return map[string]any{"cell_type": "markdown", "id": id, "metadata": map[string]any{}, "source": src}
}

func codeCell(id, src string, outputs ...map[string]any) map[string]any {
	if outputs == nil {
		outputs = []map[string]any{}
	}
	return map[string]any{"cell_type": "code", "id": id, "metadata": map[string]any{}, "source": src,
		"execution_count": 1, "outputs": outputs}
}

func stream(name, text string) map[string]any {
	return map[string]any{"output_type": "stream", "name": name, "text": text}
}

func display(typ string, data map[string]any) map[string]any {
	return map[string]any{"output_type": typ, "data": data, "metadata": map[string]any{}}
}

// nbDoc extracts a notebook and checks the document.
func nbDoc(t *testing.T, src string, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	if opts == nil {
		opts = &gofastcore.Options{}
	}
	if opts.Name == "" {
		opts.Name = "x.ipynb"
	}
	doc, err := (&Extractor{}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if doc.Source.Format != "ipynb" || doc.Source.MIME != "application/x-ipynb+json" {
		t.Fatalf("source: %+v", doc.Source)
	}
	if doc.Parts[0].Kind != gofastcore.PartBody {
		t.Fatalf("part: %v", doc.Parts[0].Kind)
	}
	return doc
}

// shape lists the blocks as kind, output and text.
func shape(doc *gofastcore.Document) []string {
	var out []string
	for _, b := range doc.Parts[0].Blocks {
		s := b.Kind.String()
		if o := b.Attrs[AttrOutput]; o != "" {
			s += "[" + o + "]"
		}
		if l := b.Attrs[gofastcore.AttrLanguage]; l != "" {
			s += "(" + l + ")"
		}
		if b.Display != 0 {
			s += "{" + b.Display.String() + "}"
		}
		out = append(out, s+" "+b.Text)
	}
	return out
}

func sameLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestNotebookDetect(t *testing.T) {
	if got := Detect("a/B.IPYNB", nil); got != "ipynb" {
		t.Errorf("Detect: %q", got)
	}
	// Never by content: JSON without the name is plain text.
	if got := Detect("", []byte(nb())); got != "txt" {
		t.Errorf("Detect by content: %q", got)
	}
}

func TestNotebookCells(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake"))
	doc := nbDoc(t, nb(
		mdCell("intro", "# Title\n", "\n", "Some *text*."),
		codeCell("c1", "print('hi')\nx = 1",
			stream("stdout", "hi\n"), stream("stdout", "there\n"), stream("stderr", "warn\n"), stream("stdout", "again")),
		codeCell("c2", "df",
			display("execute_result", map[string]any{
				"text/plain": "   a\n0  1",
				"text/html":  "<table><tr><th>a</th></tr><tr><td>1</td></tr></table>",
			})),
		codeCell("c3", "plt.plot()",
			display("display_data", map[string]any{"text/plain": "<Figure>", "image/png": png + "\n"})),
		codeCell("c4", "1/0", map[string]any{"output_type": "error", "ename": "ZeroDivisionError", "evalue": "division by zero",
			"traceback": []any{"\x1b[0;31mZeroDivisionError\x1b[0m: division by zero"}}),
		codeCell("c5", "sym", display("execute_result", map[string]any{"text/latex": "$x^2$", "text/plain": "x**2"})),
		codeCell("c6", "md", display("display_data", map[string]any{"text/markdown": "**bold**"})),
		map[string]any{"cell_type": "raw", "metadata": map[string]any{}, "source": "raw text"},
	), &gofastcore.Options{Images: gofastcore.ImagesData})
	sameLines(t, shape(doc), []string{
		"heading Title",
		"paragraph Some text.",
		"code(python) print('hi')\nx = 1",
		"code[stdout] hi\nthere",
		"code[stderr] warn",
		"code[stdout] again",
		"code(python) df",
		"table[execute_result] ",
		"code(python) plt.plot()",
		"image[display_data] ",
		"code(python) 1/0",
		"code[error] ZeroDivisionError: division by zero",
		"code(python) sym",
		"formula[execute_result] $x^2$",
		"code(python) md",
		"paragraph[display_data] bold",
		"code raw text",
	})
	b := doc.Parts[0].Blocks
	if b[0].Loc.Anchor != "intro" || b[2].Loc.Anchor != "c1" || b[4].Loc.Anchor != "c1" || b[16].Loc.Anchor != "" {
		t.Errorf("anchors: %q %q %q %q", b[0].Loc.Anchor, b[2].Loc.Anchor, b[4].Loc.Anchor, b[16].Loc.Anchor)
	}
	if b[7].Table == nil || b[7].Table.Rows[1][0] != "1" {
		t.Errorf("table: %+v", b[7].Table)
	}
	if img := b[9].Image; img == nil || img.MIME != "image/png" || string(img.Data) != "\x89PNG\r\n\x1a\nfake" {
		t.Errorf("image: %+v", img)
	}
	if b[13].Attrs[gofastcore.AttrNotation] != gofastcore.NotationLaTeX {
		t.Errorf("formula attrs: %v", b[13].Attrs)
	}
	m := doc.Metadata
	if m.Title != "Analysis" || strings.Join(m.Authors, ",") != "Ada,Grace" || m.Extra["kernel"] != "Python 3" ||
		m.Extra["language"] != "python" || m.Extra["language_version"] != "3.12.1" || m.Extra["nbformat"] != "4.5" {
		t.Errorf("metadata: %+v", m)
	}
}

func TestNotebookHidden(t *testing.T) {
	hidden := codeCell("h", "secret()", stream("stdout", "out"))
	hidden["metadata"] = map[string]any{"jupyter": map[string]any{"source_hidden": true}}
	outs := codeCell("o", "shown()", stream("stdout", "out"))
	outs["metadata"] = map[string]any{"jupyter": map[string]any{"outputs_hidden": true}}
	coll := codeCell("c", "shown2()", stream("stdout", "out2"))
	coll["metadata"] = map[string]any{"collapsed": true}
	md := mdCell("m", "hidden *words*")
	md["metadata"] = map[string]any{"jupyter": map[string]any{"source_hidden": true}}
	doc := nbDoc(t, nb(hidden, outs, coll, md), nil)
	sameLines(t, shape(doc), []string{
		"code(python){hidden} secret()",
		"code[stdout] out",
		"code(python) shown()",
		"code[stdout]{hidden} out",
		"code(python) shown2()",
		"code[stdout]{hidden} out2",
		"paragraph{hidden} hidden words",
	})
}

func TestNotebookTerminal(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                                  "plain",
		"\x1b[1;31mred\x1b[0m text":              "red text",
		" 10%\r 50%\r100%\ndone":                 "100%\ndone",
		"abcdef\rXY":                             "XYcdef",
		"line\r\nnext":                           "line\nnext",
		"\x1b]8;;http://x\x07link\x1b]8;;\x1b\\": "link",
		"tail\x1b":                               "tail",
		"é\rx":                                   "x",
		"ab\r":                                   "ab",
	} {
		if got := terminal(in); got != want {
			t.Errorf("terminal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotebookAttachments(t *testing.T) {
	data := []byte("GIF89a-fake")
	c := mdCell("a", "![chart](attachment:chart.gif) ![gone](attachment:missing.png)")
	c["attachments"] = map[string]any{"chart.gif": map[string]any{"image/gif": base64.StdEncoding.EncodeToString(data)}}
	doc := nbDoc(t, nb(c), &gofastcore.Options{Images: gofastcore.ImagesData})
	var imgs []*gofastcore.Image
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind == gofastcore.KindImage {
			imgs = append(imgs, b.Image)
		}
	}
	if len(imgs) != 2 || imgs[0].ID != "cell-1/attachment:chart.gif" || imgs[0].MIME != "image/gif" || !bytes.Equal(imgs[0].Data, data) {
		t.Fatalf("images: %+v", imgs)
	}
	if imgs[1].Data != nil {
		t.Errorf("missing attachment has data")
	}
}

func TestNotebookV3(t *testing.T) {
	src := `{"nbformat": 3, "nbformat_minor": 0, "metadata": {"name": "old"},
	 "worksheets": [{"cells": [
	  {"cell_type": "heading", "level": 2, "source": ["Old ", "style"], "metadata": {}},
	  {"cell_type": "code", "language": "python", "input": "print(1)", "collapsed": true, "metadata": {},
	   "outputs": [{"output_type": "stream", "stream": "stdout", "text": "1"},
	               {"output_type": "pyout", "prompt_number": 2, "html": "<b>2</b>", "text": "2"},
	               {"output_type": "pyerr", "ename": "E", "evalue": "v", "traceback": []}]},
	  {"cell_type": "markdown", "source": "text", "metadata": {}}]}]}`
	doc := nbDoc(t, src, nil)
	sameLines(t, shape(doc), []string{
		"heading Old style",
		"code(python) print(1)",
		"code[stdout]{hidden} 1",
		"paragraph[execute_result]{hidden} 2",
		"code[error]{hidden} E: v",
		"paragraph text",
	})
	if doc.Metadata.Extra["nbformat"] != "3.0" {
		t.Errorf("metadata: %+v", doc.Metadata)
	}
}

func TestNotebookErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want error
	}{
		"not json":   {"{\"cells\": [", gofastcore.ErrMalformed},
		"array":      {"[]", gofastcore.ErrMalformed},
		"null":       {"null", gofastcore.ErrMalformed},
		"no cells":   {`{"nbformat": 4}`, gofastcore.ErrMalformed},
		"cells type": {`{"nbformat": 4, "cells": {}}`, gofastcore.ErrMalformed},
		"version 2":  {`{"nbformat": 2, "worksheets": []}`, gofastcore.ErrUnsupported},
	} {
		_, err := (&Extractor{Format: "ipynb"}).Extract(context.Background(), strings.NewReader(tc.src), int64(len(tc.src)), nil)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNotebookLenient(t *testing.T) {
	src := `{"cells": [{"cell_type": "code", "source": 7, "outputs": "x"}, "not a cell",
	  {"cell_type": "markdown", "source": ["ok"], "metadata": []}], "metadata": {"title": 5}}`
	doc := nbDoc(t, "\xEF\xBB\xBF"+src, nil)
	sameLines(t, shape(doc), []string{"paragraph ok"})
	w := csvWarnings(doc, gofastcore.WarnInputRepaired)
	if len(w) != 1 || !strings.HasPrefix(w[0], "5 values have") {
		t.Errorf("warnings: %q", w)
	}
	bad := nb(mdCell("x", "caf\xe9"))
	bad = strings.Replace(bad, `caf�`, "caf\xe9", 1)
	doc = nbDoc(t, bad, nil)
	if w := csvWarnings(doc, gofastcore.WarnTextUnmappedGlyphs); len(w) != 1 {
		t.Errorf("invalid UTF-8: %q", w)
	}
}

func TestNotebookEmpty(t *testing.T) {
	doc := nbDoc(t, nb(), nil)
	if len(doc.Parts[0].Blocks) != 0 || len(csvWarnings(doc, gofastcore.WarnPartNoText)) != 1 {
		t.Errorf("empty: %+v", doc.Parts[0])
	}
	if _, err := (&Extractor{}).Extract(context.Background(), strings.NewReader(nb()), int64(len(nb())),
		&gofastcore.Options{Name: "x.ipynb", Parts: gofastcore.PartRange{First: 2}}); !errors.Is(err, gofastcore.ErrNoPartsSelected) {
		t.Errorf("parts: %v", err)
	}
}

func TestNotebookStored(t *testing.T) {
	doc := nbDoc(t, nb(codeCell("s", "js", display("display_data", map[string]any{
		"text/html": "<script>alert(1)</script><p>shown</p>"}))), nil)
	if len(doc.Parts) != 2 || doc.Parts[1].Kind != gofastcore.PartStored {
		t.Fatalf("parts: %d", len(doc.Parts))
	}
	if w := csvWarnings(doc, gofastcore.WarnInputActiveContent); len(w) != 1 {
		t.Errorf("active: %q", w)
	}
}

func TestNotebookBudgets(t *testing.T) {
	var cells []map[string]any
	for range 50 {
		cells = append(cells, codeCell("", strings.Repeat("x", 100)))
	}
	doc := nbDoc(t, nb(cells...), &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 1000}})
	w := csvWarnings(doc, gofastcore.WarnLimitExceeded)
	if len(w) != 1 || !strings.HasPrefix(w[0], "the limit on the amount of text was reached; the body is cut off there (") ||
		!strings.HasSuffix(w[0], " cells not read)") {
		t.Errorf("text: %q", w)
	}
	doc = nbDoc(t, nb(cells...), &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 1000}})
	w = csvWarnings(doc, gofastcore.WarnLimitExceeded)
	if len(w) != 1 || !strings.HasPrefix(w[0], "the limit on decoded bytes was reached; the rest of the notebook was not read (") {
		t.Errorf("decode: %q", w)
	}
	long := codeCell(strings.Repeat("i", 2000), "x")
	doc = nbDoc(t, nb(long), nil)
	if w := csvWarnings(doc, gofastcore.WarnLimitExceeded); len(w) != 1 || doc.Parts[0].Blocks[0].Loc.Anchor != "" {
		t.Errorf("anchor: %q", w)
	}
}

// TestNotebookWorstCase reads notebooks built to cost much for their size.
func TestNotebookWorstCase(t *testing.T) {
	n := 1 << 18
	if testing.Short() || raceSlowdown > 1 {
		// The race detector makes each of the many small walks some 30
		// times slower; the inputs are smaller there.
		n = 1 << 14
	}
	many := `{"nbformat":4,"cells":[` + strings.Repeat(`{"cell_type":"code","source":"x","outputs":[{"output_type":"stream","name":"stdout","text":"y"}]},`, n) + `{}]}`
	streams := `{"nbformat":4,"cells":[{"cell_type":"code","source":"","outputs":[` +
		strings.Repeat(`{"output_type":"stream","name":"stdout","text":"a"},{"output_type":"stream","name":"stderr","text":"b"},`, n) + `{}]}]}`
	returns := `{"nbformat":4,"cells":[{"cell_type":"code","source":"","outputs":[{"output_type":"stream","name":"stdout","text":"` +
		strings.Repeat(`abcdefgh\r`, n) + `"}]}]}`
	nested := `{"nbformat":4,"cells":[{"cell_type":"markdown","source":"` + strings.Repeat(">", n) + `"}]}`
	for name, src := range map[string]string{"many cells": many, "alternating streams": streams, "carriage returns": returns, "nested quotes": nested} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			nbDoc(t, src, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 8 << 20}})
			if d := time.Since(start); d > 5*raceSlowdown*time.Second {
				t.Errorf("%d bytes took %v", len(src), d)
			}
		})
	}
}

func TestNotebookVersionString(t *testing.T) {
	doc := nbDoc(t, `{"nbformat": "4", "nbformat_minor": "2", "cells": [{"cell_type": "markdown", "source": "x"}]}`, nil)
	if doc.Metadata.Extra["nbformat"] != "4.2" || len(doc.Parts[0].Blocks) != 1 {
		t.Errorf("%+v", doc.Metadata)
	}
}
