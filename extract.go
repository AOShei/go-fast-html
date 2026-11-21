package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/blocks"
	"github.com/AOShei/go-fast-html/internal/charset"
	"github.com/AOShei/go-fast-html/internal/css"
	"github.com/AOShei/go-fast-html/internal/dom"
	"github.com/AOShei/go-fast-html/internal/md"
)

// Defaults for the limits of gofastcore.Options.Limits, used where a limit
// is zero. A negative limit means no limit.
const (
	// DefaultMaxInputBytes bounds the size of the file. The whole file is
	// held in memory while it is read.
	DefaultMaxInputBytes = 256 << 20
	// DefaultMaxDecodedBytes bounds the bytes decoded: the text the input
	// becomes in UTF-8, and the images given inline as data: URIs.
	DefaultMaxDecodedBytes = 1 << 30
	// DefaultMaxDepth is how deeply elements are nested in the tree that
	// is read; see Extractor.MaxDepth.
	DefaultMaxDepth = dom.DefaultMaxDepth
)

// Warning codes specific to this library. Each states a fact about the
// file. The codes shared by all go-fast libraries are in gofastcore; this
// library raises these of them besides the ones about parts and limits
// (part.no_text, part.image_only, part.failed, limit.exceeded):
//
//	input.active_content      the page has scripts, event handler attributes
//	                          or javascript: addresses; script text is read
//	                          as data into the stored part, and nothing is
//	                          run
//	input.external_reference  the page loads resources from elsewhere:
//	                          pictures, stylesheets, scripts, frames, a
//	                          refresh to another address, a form's target;
//	                          nothing is fetched
//	text.unmapped_glyphs      bytes that are not valid in the encoding read
//	                          appear as U+FFFD
//	image.unsupported_encoding  image data was asked for and an inline
//	                          data: image could not be decoded
//	input.repaired            an EPUB's zip archive has no readable central
//	                          directory and was read by its local headers
//	input.ambiguous           a member name occurs twice in an EPUB's
//	                          archive; the first is read
//	child.encrypted           an embedded document of an EPUB is encrypted
//	                          and was not opened
//
// An embedded document handed to gofastcore.Options.ExtractChild may also
// carry child.not_extracted, child.unsupported or child.failed, which that
// function sets. The EPUB codes are in epub.go.
const (
	// WarnCharsetUnsupported: the document declares a character encoding
	// this library does not decode, and was read as windows-1252, so
	// letters outside ASCII may be wrong; or Extractor.Charset names one,
	// and it was ignored. An EPUB has one such warning for all its
	// chapters, with their number and the labels.
	WarnCharsetUnsupported = "html.charset_unsupported"
	// WarnNestingFlattened: elements are nested deeper than the limit.
	// Below it, elements were placed beside each other instead of inside
	// each other; their text is all there, and the reasons it is hidden
	// are kept, but the blocks there may be joined or split differently.
	WarnNestingFlattened = "html.nesting_flattened"
	// WarnMetadataTruncated: a metadata value was longer than 64 KiB and
	// was cut there, or there were more than 256 authors or keyword
	// declarations and the rest were left out; or an EPUB's package
	// document has more than 4096 Dublin Core elements, and the rest were
	// left out.
	WarnMetadataTruncated = "html.metadata_truncated"
)

// Extractor extracts HTML, Markdown, plain text and EPUB. The zero value
// is ready to use and safe for concurrent use.
type Extractor struct {
	// Format is the format to read the input as: "html", "md", "txt" or
	// "epub". Empty means to decide it with Detect.
	Format string
	// MaxDepth bounds how deeply elements nest in the tree that is read.
	// Zero means DefaultMaxDepth.
	MaxDepth int
	// MaxArchiveMembers bounds the number of files in an EPUB's archive.
	// Zero means DefaultMaxArchiveMembers; negative means no limit.
	MaxArchiveMembers int
	// Charset is the character encoding the input comes with from outside
	// it, as a label such as the charset parameter of a MIME Content-Type
	// gives it ("windows-1251", "shift_jis"): the transport-layer encoding
	// of the WHATWG encoding sniffing. It applies to HTML, Markdown and
	// plain text, not to the chapters of an EPUB. A byte order mark
	// overrides it; it overrides a <meta> declaration and detection. A
	// label that names no encoding this library decodes is ignored, with
	// the warning html.charset_unsupported. Empty means none.
	Charset string
}

var _ gofastcore.Extractor = (*Extractor)(nil)

var mimeTypes = map[string]string{"html": "text/html", "md": "text/markdown", "txt": "text/plain", "epub": "application/epub+zip"}

// Formats lists the formats this library extracts.
func (e *Extractor) Formats() []string { return []string{"html", "md", "txt", "epub"} }

// Detect decides the format of an input from its name, and failing that
// from its content: a name ending in .html, .htm, .xhtml or .shtml is
// HTML; .md, .markdown, .mdown, .mkd, .mkdn or .mdwn Markdown; .txt or
// .text plain text; .epub EPUB. A zip archive whose first member is
// "mimetype" saying application/epub+zip is EPUB whatever its name.
// Without one of those, an input that begins with
// markup, or has a tag of a common HTML element in its first 4 KiB, is
// HTML, and any other plain text: Markdown is not guessed from content,
// since reading plain text as Markdown would take characters out of it.
func Detect(name string, src []byte) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm", ".xhtml", ".shtml":
		return "html"
	case ".md", ".markdown", ".mdown", ".mkd", ".mkdn", ".mdwn":
		return "md"
	case ".txt", ".text":
		return "txt"
	case ".epub":
		return "epub"
	}
	if bytes.HasPrefix(src, []byte("PK\x03\x04")) && len(src) >= 58 && string(src[30:58]) == "mimetypeapplication/epub+zip" {
		return "epub"
	}
	head := src[:min(len(src), 4096)]
	if bytes.HasPrefix(head, []byte{0xFF, 0xFE}) || bytes.HasPrefix(head, []byte{0xFE, 0xFF}) {
		head = bytes.ReplaceAll(head[2:], []byte{0}, nil)
	}
	head = bytes.ToLower(bytes.TrimLeft(bytes.TrimPrefix(head, []byte("\xEF\xBB\xBF")), " \t\r\n\f"))
	for _, p := range []string{"<!doctype", "<html", "<?xml", "<!--", "<head", "<body"} {
		if bytes.HasPrefix(head, []byte(p)) {
			return "html"
		}
	}
	for i := bytes.IndexByte(head, '<'); i >= 0; {
		j := i + 1
		if j < len(head) && head[j] == '/' {
			j++
		}
		k := j
		for k < len(head) && (head[k] >= 'a' && head[k] <= 'z' || head[k] >= '0' && head[k] <= '9') {
			k++
		}
		if k < len(head) && htmlTags[string(head[j:k])] && strings.IndexByte(" \t\r\n/>", head[k]) >= 0 {
			return "html"
		}
		n := bytes.IndexByte(head[i+1:], '<')
		if n < 0 {
			break
		}
		i += 1 + n
	}
	return "txt"
}

var htmlTags = map[string]bool{}

func init() {
	for _, t := range strings.Fields("html head body title meta link script style div p br span a table tr td th " +
		"b i em strong u ul ol li h1 h2 h3 h4 h5 h6 img pre code blockquote form input section article nav header footer main") {
		htmlTags[t] = true
	}
}

// Extract reads a document with default settings. It is
// (&Extractor{}).Extract.
func Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).Extract(ctx, r, size, opts)
}

// ExtractFile opens the named file and extracts it with default settings.
func ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).ExtractFile(ctx, path, opts)
}

// ExtractFile opens the named file and extracts it. The file name is
// recorded as the document's source name unless opts.Name is set.
func (e *Extractor) ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	o := gofastcore.Options{}
	if opts != nil {
		o = *opts
	}
	if o.Name == "" {
		o.Name = path
	}
	return e.Extract(ctx, f, st.Size(), &o)
}

// Extract reads size bytes of HTML, Markdown, plain text or EPUB from r,
// in the format e.Format names or Detect decides from Options.Name and the
// content, and returns the document. HTML, Markdown and plain text are one
// part of kind body; an EPUB is a part of kind chapter for each chapter
// of its spine. With the whole document (Options.Parts unset), script and
// style text, hidden form values and an EPUB's images no chapter shows
// follow in a part of kind stored.
//
// An error wraps one of the gofastcore errors: ErrLimitExceeded when the
// input is larger than Limits.MaxInputBytes, an EPUB's archive has more
// members than MaxArchiveMembers, its container.xml or package document
// is larger than 16 MiB or the decode budget, or its encryption.xml is
// too large to be read in full; ErrNoPartsSelected when Options.Parts
// selects no part (part 1 of HTML, Markdown or plain text, a chapter of
// an EPUB); ErrMalformed when r cannot be read, or an EPUB's archive,
// package document or encryption.xml cannot be read; ErrUnsupported for a
// Format this library does not have, or an EPUB whose selected chapters
// are all encrypted. An EPUB whose encryption.xml cannot be read in full
// has no chapter read, since any may be encrypted. Any input that can be
// read is a document of its format: tag soup is recovered from the way
// browsers recover from it.
func (e *Extractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &gofastcore.Options{}
	}
	if size < 0 {
		return nil, fmt.Errorf("gofasthtml: negative size: %w", gofastcore.ErrMalformed)
	}
	if size > gofastcore.ResolveLimit(opts.Limits.MaxInputBytes, DefaultMaxInputBytes) {
		return nil, fmt.Errorf("gofasthtml: input of %d bytes is larger than the limit: %w", size, gofastcore.ErrLimitExceeded)
	}
	src := make([]byte, size)
	if n, err := r.ReadAt(src, 0); n < len(src) {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("gofasthtml: reading input: %v: %w", err, gofastcore.ErrMalformed)
	}

	format := e.Format
	if format == "" {
		format = Detect(opts.Name, src)
	}
	mime, known := mimeTypes[format]
	if !known {
		return nil, fmt.Errorf("gofasthtml: format %q: %w", format, gofastcore.ErrUnsupported)
	}
	if format != "epub" && (!opts.Parts.Contains(1) || opts.Parts.Last != 0 && opts.Parts.First > opts.Parts.Last) {
		return nil, fmt.Errorf("gofasthtml: a document has one part: %w", gofastcore.ErrNoPartsSelected)
	}

	ctx, text := gofastcore.WithTextBudget(ctx, opts.Limits)
	defer text.Close()
	ctx, decode := gofastcore.WithDecodeBudget(ctx, opts.Limits, DefaultMaxDecodedBytes)
	defer decode.Close()

	doc := gofastcore.New(format)
	doc.Source = gofastcore.Source{Format: format, Name: opts.Name, MIME: mime, Size: size}
	if format == "epub" {
		return e.epub(ctx, doc, src, opts, text, decode)
	}
	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1}
	t := &tally{}
	res, err := e.page(ctx, src, format, pageConfig{opts: opts, text: text, decode: decode, charset: e.Charset}, &part, t)
	if err != nil {
		return nil, err
	}
	if res != nil {
		doc.Metadata = res.Meta
	}
	doc.Parts = append(doc.Parts, part)
	if len(t.stored) > 0 && opts.Parts == (gofastcore.PartRange{}) {
		stored := gofastcore.Part{Kind: gofastcore.PartStored, Number: 2, Blocks: t.stored}
		stored.Finish()
		doc.Parts = append(doc.Parts, stored)
	}
	t.finish(doc)
	return doc, nil
}

// tally collects what the pages of a document found that concerns the
// document as a whole.
type tally struct {
	active, external int
	metaCut          bool
	charsets         []string           // declared encodings not decoded, each once
	charsetCount     map[string]int     // how many pages declare each
	transport        string             // Extractor.Charset, when not decoded
	stored           []gofastcore.Block // for the stored part
}

// finish adds the document's warnings.
func (t *tally) finish(doc *gofastcore.Document) {
	if t.transport != "" {
		doc.Warn(gofastcore.SeverityWarning, WarnCharsetUnsupported,
			fmt.Sprintf("the encoding %q was given with the input and is not decoded; it was ignored", t.transport))
	}
	if len(t.charsets) == 1 && t.charsetCount[t.charsets[0]] == 1 {
		doc.Warn(gofastcore.SeverityWarning, WarnCharsetUnsupported,
			fmt.Sprintf("the document declares the encoding %q, which is not decoded; it was read as windows-1252", t.charsets[0]))
	} else if len(t.charsets) > 0 {
		// One warning for the document, however many chapters declare
		// encodings: each label once, with the number of chapters.
		pages := 0
		var list []string
		for i, c := range t.charsets {
			pages += t.charsetCount[c]
			if i < 8 {
				list = append(list, fmt.Sprintf("%q in %d", c, t.charsetCount[c]))
			}
		}
		if n := len(t.charsets) - len(list); n > 0 {
			list = append(list, fmt.Sprintf("and %d more", n))
		}
		doc.Warn(gofastcore.SeverityWarning, WarnCharsetUnsupported,
			fmt.Sprintf("%d chapters declare an encoding that is not decoded (%s); they were read as windows-1252", pages, strings.Join(list, ", ")))
	}
	if t.active > 0 {
		doc.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent,
			fmt.Sprintf("%d scripts, event handler attributes or javascript: addresses; script text is read as data into the stored part, and nothing is run", t.active))
	}
	if t.external > 0 {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputExternalReference,
			fmt.Sprintf("%d resources loaded from elsewhere, not counting hyperlinks; nothing was fetched", t.external))
	}
	if t.metaCut {
		doc.Warn(gofastcore.SeverityInfo, WarnMetadataTruncated, "a metadata value was cut at 64 KiB, or declarations past 256 were left out")
	}
}

// pageConfig is what reading one page needs besides its bytes.
type pageConfig struct {
	opts   *gofastcore.Options
	text   *gofastcore.TextBudget
	decode *gofastcore.DecodeBudget
	// xhtml reads self-closing tags as XML does (EPUB content).
	xhtml bool
	// image resolves an image address, as for EPUB, where images are in
	// the archive.
	image    func(src string) (blocks.ImageRef, bool)
	internal func(ref string) bool
	// charset is the caller's transport-layer encoding label.
	charset string
	// noDataElsewhere counts the images whose data image reported
	// missing for a reason warned of elsewhere (a limit, encryption),
	// which image.unsupported_encoding leaves out.
	noDataElsewhere func() int
}

// page reads one document, an HTML page, Markdown or plain text, or a
// chapter of an EPUB, into part, and calls Finish on it. What
// concerns the whole document goes to t. It returns the result of the
// walk, nil for plain text.
func (e *Extractor) page(ctx context.Context, src []byte, format string, pc pageConfig, part *gofastcore.Part, t *tally) (*blocks.Result, error) {
	opts, text, decode := pc.opts, pc.text, pc.decode
	enc := charset.SniffText(src, pc.charset)
	if format == "html" {
		enc = charset.Sniff(src, pc.charset)
	}
	if enc.Transport != "" {
		t.transport = blocksLabel(enc.Transport)
	}
	if enc.Unsupported {
		label := blocksLabel(enc.Declared)
		if t.charsetCount[label] == 0 {
			if t.charsetCount == nil {
				t.charsetCount = map[string]int{}
			}
			t.charsets = append(t.charsets, label)
		}
		t.charsetCount[label]++
	}
	decoded, m, invalid, cut := charset.Decode(src, enc, max(decode.Left(), 0))
	decode.Take(int64(len(decoded)))
	if invalid > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs,
			fmt.Sprintf("%d byte sequences are not valid %s and appear as U+FFFD", invalid, enc.Enc))
	}
	if cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; the rest of the input was not read")
	}

	if format == "txt" {
		spent := plainText(ctx, decoded, m.Source, text, part)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if spent {
			part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				"the limit on the amount of text was reached; the body is cut off there")
		}
		cut = cut || spent
		if len(part.Blocks) == 0 && !cut {
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "no text")
		}
		part.Finish()
		return nil, nil
	}

	page, source := decoded, m.Source
	if format == "md" {
		d, err := md.Parse(ctx, string(decoded))
		if err != nil {
			return nil, err
		}
		// The HTML the Markdown becomes counts as decoded bytes.
		h := d.Render(int(min(max(decode.Left(), 0), math.MaxInt32)))
		decode.Take(int64(len(h.B)))
		if h.Cut && !cut {
			cut = true
			part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				"the limit on decoded bytes was reached while the Markdown was rendered; the rest was not read")
		}
		page = h.B
		source = func(i int) int64 { return m.Source(h.Source(i)) }
	}

	tree, err := dom.Parse(ctx, page, dom.Options{MaxDepth: e.MaxDepth, XHTML: pc.xhtml})
	if err != nil {
		return nil, err
	}
	if tree.Limited {
		part.Warn(gofastcore.SeverityWarning, WarnNestingFlattened,
			"elements are nested deeper than the limit; below it they were read side by side")
	}
	res, err := blocks.Walk(ctx, tree.Root, blocks.Config{
		Spans: opts.Spans, Images: opts.Images, Budget: text, Decode: decode, Source: source, Image: pc.image, Internal: pc.internal,
		MaxDepth: e.MaxDepth,
	})
	if err != nil {
		return nil, err
	}
	if res.FrameDepth {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"an iframe srcdoc document is nested at the depth limit and was not read")
	}
	if res.FrameFlattened && !tree.Limited {
		part.Warn(gofastcore.SeverityWarning, WarnNestingFlattened,
			"elements are nested deeper than the limit; below it they were read side by side")
	}
	if res.FrameDecode && !cut {
		cut = true
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached in an iframe srcdoc document; the rest of it was not read")
	}
	caps := tree.Caps
	caps.Add(res.FrameCaps)
	builderCaps(part, caps)
	part.Blocks = res.Blocks
	if res.Cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on the amount of text was reached; the "+part.Kind.String()+" is cut off there")
	}
	t.active += res.Active
	t.external += res.External
	t.metaCut = t.metaCut || res.MetaCut
	t.stored = append(t.stored, res.Stored...)
	noData := res.ImageData
	if pc.noDataElsewhere != nil {
		noData -= pc.noDataElsewhere()
	}
	if noData > 0 && opts.Images == gofastcore.ImagesData {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnImageUnsupportedEncoding,
			fmt.Sprintf("%d images could not be decoded or read; their blocks have no data", noData))
	}
	if !res.Cut && !cut {
		hasText := false
		for i := range part.Blocks {
			if part.Blocks[i].Kind != gofastcore.KindImage || part.Blocks[i].Text != "" {
				hasText = true
				break
			}
		}
		switch {
		case hasText:
		case res.Images > 0:
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartImageOnly, fmt.Sprintf("%d images, no text", res.Images))
		default:
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "no text and no images")
		}
	}
	part.Finish()
	return res, nil
}

// builderCaps warns of the bounds of the tree builder that were reached,
// once each in the part.
func builderCaps(part *gofastcore.Part, c dom.Caps) {
	if c.Formatting {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("more than %d formatting elements (a, b, i, font and the like) were open at once; the earliest were dropped from the list of those reopened after a block, so text after a block that closed them does not have their spans, anchors or the display they declare", dom.MaxFormatting))
	}
	if c.Clones {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the bound on formatting elements reopened after blocks (a quarter of the input's size plus 4096) was reached; past it they apply to what follows but are not put in the tree, so that text does not have their spans or anchors")
	}
	if c.Styles > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("%d style attributes are longer than %d KiB; the declarations past %d KiB were not read", c.Styles, css.MaxLen>>10, css.MaxLen>>10))
	}
}

// blocksLabel makes a declared encoding label safe to quote.
func blocksLabel(s string) string {
	out := []rune{}
	for _, r := range s {
		if r < 0x20 || r == 0x7F || r >= 0x80 && r < 0xA0 {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
