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
// library raises these of them besides the ones about parts and limits:
//
//	input.active_content      the page has scripts, event handler attributes
//	                          or javascript: addresses; script text is read
//	                          as data, flagged invisible, and nothing is run
//	input.external_reference  the page loads resources from elsewhere:
//	                          pictures, stylesheets, scripts, frames, a
//	                          refresh to another address, a form's target;
//	                          nothing is fetched
//	text.unmapped_glyphs      bytes that are not valid in the encoding read
//	                          appear as U+FFFD
//	text.hidden               text a reader cannot see; the blocks and
//	                          spans concerned say why
//	image.unsupported_encoding  image data was asked for and an inline
//	                          data: image could not be decoded
const (
	// WarnCharsetUnsupported: the document declares a character encoding
	// this library does not decode. It was read as windows-1252, so
	// letters outside ASCII may be wrong.
	WarnCharsetUnsupported = "html.charset_unsupported"
	// WarnNestingFlattened: elements are nested deeper than the limit.
	// Below it, elements were placed beside each other instead of inside
	// each other; their text is all there, and the reasons it is hidden
	// are kept, but the blocks there may be joined or split differently.
	WarnNestingFlattened = "html.nesting_flattened"
	// WarnMetadataTruncated: a metadata value was longer than 64 KiB and
	// was cut there, or there were more than 256 authors or keyword
	// declarations and the rest were left out.
	WarnMetadataTruncated = "html.metadata_truncated"
)

// Extractor extracts HTML, Markdown and plain text. The zero value is
// ready to use and safe for concurrent use.
type Extractor struct {
	// Format is the format to read the input as: "html", "md" or "txt".
	// Empty means to decide it with Detect.
	Format string
	// MaxDepth bounds how deeply elements nest in the tree that is read.
	// Zero means DefaultMaxDepth.
	MaxDepth int
}

var _ gofastcore.Extractor = (*Extractor)(nil)

var mimeTypes = map[string]string{"html": "text/html", "md": "text/markdown", "txt": "text/plain"}

// Formats lists the formats this library extracts.
func (e *Extractor) Formats() []string { return []string{"html", "md", "txt"} }

// Detect decides the format of an input from its name, and failing that
// from its content: a name ending in .html, .htm, .xhtml or .shtml is
// HTML; .md, .markdown, .mdown, .mkd, .mkdn or .mdwn Markdown; .txt or
// .text plain text. Without one of those, an input that begins with
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

// Extract reads size bytes of HTML, Markdown or plain text from r, in the
// format e.Format names or Detect decides from Options.Name and the
// content, and returns the document: one part of kind body.
//
// An error wraps one of the gofastcore errors: ErrLimitExceeded when the
// input is larger than Limits.MaxInputBytes, ErrNoPartsSelected when
// Options.Parts does not select part 1, ErrMalformed when r cannot be
// read, ErrUnsupported for a Format this library does not have. Any input
// that can be read is a document of its format: tag soup is recovered
// from the way browsers recover from it.
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
	if !opts.Parts.Contains(1) || opts.Parts.Last != 0 && opts.Parts.First > opts.Parts.Last {
		return nil, fmt.Errorf("gofasthtml: a document has one part: %w", gofastcore.ErrNoPartsSelected)
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

	ctx, text := gofastcore.WithTextBudget(ctx, opts.Limits)
	defer text.Close()
	ctx, decode := gofastcore.WithDecodeBudget(ctx, opts.Limits, DefaultMaxDecodedBytes)
	defer decode.Close()

	doc := gofastcore.New(format)
	doc.Source = gofastcore.Source{Format: format, Name: opts.Name, MIME: mime, Size: size}
	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1}

	enc := charset.SniffText(src)
	if format == "html" {
		enc = charset.Sniff(src)
	}
	if enc.Unsupported {
		doc.Warn(gofastcore.SeverityWarning, WarnCharsetUnsupported,
			fmt.Sprintf("the document declares the encoding %q, which is not decoded; it was read as windows-1252", blocksLabel(enc.Declared)))
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
		spent := plainText(ctx, decoded, m.Source, text, &part)
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
		part.MarkHidden()
		doc.Parts = append(doc.Parts, part)
		doc.HiddenChecked = gofastcore.HiddenChars
		return doc, nil
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

	tree, err := dom.Parse(ctx, page, dom.Options{MaxDepth: e.MaxDepth})
	if err != nil {
		return nil, err
	}
	if tree.Limited {
		part.Warn(gofastcore.SeverityWarning, WarnNestingFlattened,
			"elements are nested deeper than the limit; below it they were read side by side")
	}
	res, err := blocks.Walk(ctx, tree.Root, blocks.Config{
		Spans: opts.Spans, Images: opts.Images, Budget: text, Decode: decode, Source: source,
	})
	if err != nil {
		return nil, err
	}
	part.Blocks = res.Blocks
	doc.Metadata = res.Meta
	if res.Cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on the amount of text was reached; the body is cut off there")
	}
	if res.Active > 0 {
		doc.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent,
			fmt.Sprintf("%d scripts, event handler attributes or javascript: addresses; script text is read as data, flagged invisible, and nothing is run", res.Active))
	}
	if res.External > 0 {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputExternalReference,
			fmt.Sprintf("%d resources loaded from elsewhere, not counting hyperlinks; nothing was fetched", res.External))
	}
	if res.ImageData > 0 && opts.Images == gofastcore.ImagesData {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnImageUnsupportedEncoding,
			fmt.Sprintf("%d inline data: images could not be decoded; their blocks have no data", res.ImageData))
	}
	if res.MetaCut {
		doc.Warn(gofastcore.SeverityInfo, WarnMetadataTruncated, "a metadata value was cut at 64 KiB, or declarations past 256 were left out")
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
	part.MarkHidden()
	doc.Parts = append(doc.Parts, part)

	// Without stylesheets or scripts, inline style and attributes are all
	// that decides what is displayed, and that is read in full.
	doc.HiddenChecked = gofastcore.HiddenChars
	if res.Stylesheets == 0 && res.Active == 0 {
		doc.HiddenChecked |= gofastcore.HiddenInvisible
	}
	if hiddenInMetadata(&doc.Metadata) {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextHidden, "the metadata contains invisible characters")
	}
	return doc, nil
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

// hiddenInMetadata reports whether any metadata value holds invisible
// characters, which a block flag cannot mark.
func hiddenInMetadata(m *gofastcore.Metadata) bool {
	found := func(s string) bool { return gofastcore.InvisibleChars(s) != nil }
	if found(m.Title) || found(m.Subject) || found(m.Language) || found(m.Application) {
		return true
	}
	for _, s := range m.Authors {
		if found(s) {
			return true
		}
	}
	for _, s := range m.Keywords {
		if found(s) {
			return true
		}
	}
	for k, v := range m.Extra {
		if found(k) || found(v) {
			return true
		}
	}
	return false
}
