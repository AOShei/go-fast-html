package gofasthtml

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// mhtOf is an MHTML file of a page and the parts given, each written
// whole (header section, blank line, body), with boundary "B".
func mhtOf(page string, parts ...string) string {
	var sb strings.Builder
	sb.WriteString("From: <Saved by Blink>\r\nSnapshot-Content-Location: https://example.org/p.html\r\n" +
		"Content-Type: multipart/related; type=\"text/html\"; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\nContent-Location: https://example.org/p.html\r\n\r\n")
	sb.WriteString(page)
	for _, p := range parts {
		sb.WriteString("\r\n--B\r\n")
		sb.WriteString(p)
	}
	sb.WriteString("\r\n--B--\r\n")
	return sb.String()
}

// reviewWarnings lists a document's warnings, of the document and its
// parts, as "code: message".
func reviewWarnings(doc *gofastcore.Document) []string {
	var out []string
	for _, w := range doc.AllWarnings() {
		out = append(out, w.Code+": "+w.Message)
	}
	return out
}

// Review finding: mhtml.go:152-171, a part is decoded from its transfer
// encoding in full before the decode budget is asked, and it is decoded
// again for every <img> that refers to it. Once the budget is spent each
// further reference still decodes the whole part and throws it away, so
// a small file that refers to one base64 image many times costs work and
// allocation in proportion to (size x references), which no limit bounds.
// The budget should be asked (from the encoded length, or a decoded size
// remembered per part) before decoding, and a decoded part kept for the
// next reference.
func TestReviewMHTMLDecodeBeforeBudget(t *testing.T) {
	img := base64.StdEncoding.EncodeToString(make([]byte, 192<<10)) // 256 KiB of base64
	page := strings.Repeat(`<img src="i.png">`, 400)
	src := mhtOf(page, "Content-Type: image/png\r\nContent-Transfer-Encoding: base64\r\nContent-Location: https://example.org/i.png\r\n\r\n"+img)
	opts := &gofastcore.Options{Images: gofastcore.ImagesData, Limits: gofastcore.Limits{MaxDecodedBytes: 1 << 20}}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	doc, err := (&Extractor{}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), opts)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	// The budget lets about 5 decodes through; 400 references at 192 KiB
	// each allocate 75 MiB.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 16<<20 {
		t.Fatalf("a %d-byte file under a 1 MiB decode budget allocated %d MiB", len(src), alloc>>20)
	}
}

// Review finding: mhtml.go:240 and :96 ignore Problems.TooMany. A header
// section with more than mimepart.MaxFields (10,000) fields has the rest
// left out without a warning; the file's header fields are content kept
// in Metadata.Extra. Every cut by a limit is limit.exceeded at severity
// error.
func TestReviewMHTMLHeaderFieldsCutSilently(t *testing.T) {
	src := "From: <Saved by Blink>\r\n" + strings.Repeat("X-A: 1\r\n", 10_000) + "X-Last: kept\r\n" +
		"Content-Type: multipart/related; boundary=\"B\"\r\n\r\n--B\r\nContent-Type: text/html\r\n\r\n<p>x</p>\r\n--B--\r\n"
	doc := extractMHT(t, src, &gofastcore.Options{Name: "a.mht"})
	if doc.Metadata.Extra["x-last"] == "" && !anyWarning(doc, gofastcore.WarnLimitExceeded) {
		t.Fatalf("header fields past the 10,000th were left out with no limit.exceeded; warnings %+v", doc.Warnings)
	}
}

// Review finding: mhtml.go:417, mhtmlRest returns early when the context
// is cancelled (line 542), and mhtml then returns the document it has with
// a nil error: a cancelled extraction looks like a complete one with
// fewer children. Extract must return the context's error.
func TestReviewMHTMLCancelDuringChildren(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child := func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		cancel() // the caller gives up while the first embedded document is read
		return gofastcore.New("txt"), nil
	}
	src := mhtOf("<p>page</p>",
		"Content-Type: text/plain\r\nContent-Location: https://example.org/a.txt\r\n\r\na",
		"Content-Type: text/plain\r\nContent-Location: https://example.org/b.txt\r\n\r\nb")
	doc, err := (&Extractor{}).Extract(ctx, strings.NewReader(src), int64(len(src)), &gofastcore.Options{Child: child})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, %d children", err, len(doc.Children))
	}
}

// Review finding: mhtml.go:162-163 charges the decode budget with the
// whole of a 7bit, 8bit or binary part although nothing is decoded, and
// the page is then charged again when it is decoded to UTF-8 (extract.go
// 559-560). The page of an MHTML file is cut at about half the decode
// budget that the same page read as HTML gets.
func TestReviewMHTMLIdentityPartChargedTwice(t *testing.T) {
	page := "<p>" + strings.Repeat("a", 3000) + "</p><p>end</p>"
	lim := gofastcore.Limits{MaxDecodedBytes: 4096}
	h, err := (&Extractor{}).Extract(context.Background(), strings.NewReader(page), int64(len(page)), &gofastcore.Options{Name: "a.html", Limits: lim})
	if err != nil || anyWarning(h, gofastcore.WarnLimitExceeded) {
		t.Fatalf("as HTML: %v %q", err, reviewWarnings(h))
	}
	src := "From: <Saved by Blink>\r\nContent-Type: multipart/related; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\nContent-Transfer-Encoding: binary\r\n\r\n" + page + "\r\n--B--\r\n"
	doc := extractMHT(t, src, &gofastcore.Options{Name: "a.mht", Limits: lim})
	if anyWarning(doc, gofastcore.WarnLimitExceeded) || !strings.Contains(doc.Text(), "end") {
		t.Fatalf("the same page in an MHTML file was cut: %q", reviewWarnings(doc))
	}
}
