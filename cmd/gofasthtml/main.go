// Command gofasthtml extracts HTML, Markdown, plain text, EPUB, CSV,
// TSV, notebooks, JSON, YAML and MHTML files into the shared document model of the go-fast libraries and
// prints them as JSON, Markdown or plain text.
//
//	gofasthtml [flags] file-or-directory...
//
// The format of each input is decided by its extension (.html, .htm,
// .md, .markdown, .txt, .epub, .csv, .tsv, .ipynb, .json, .yaml, .mht,
// .srt, .vtt, ...) and, failing that, by its content: an EPUB's mimetype
// member, a WebVTT signature, or markup; -as sets it. A directory
// stands for the HTML, Markdown, text, EPUB, CSV, TSV, notebook, JSON,
// YAML, MHTML, SubRip and WebVTT files in it and
// below it; "-" is
// standard input. With one input the
// JSON is indented; with several, each document is one line. Only the
// documents go to standard output: everything said about them goes to
// standard error, and the exit status is 1 if any input could not be read.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// The extensions of the files this command picks out of a directory.
var extensions = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true, ".shtml": true,
	".md": true, ".markdown": true, ".mdown": true, ".mkd": true, ".mkdn": true, ".mdwn": true,
	".txt": true, ".text": true,
	".epub": true,
	".csv":  true, ".tsv": true, ".tab": true, ".ipynb": true,
	".json": true, ".jsonl": true, ".ndjson": true, ".geojson": true, ".yaml": true, ".yml": true,
	".mht": true, ".mhtml": true,
	".srt": true, ".vtt": true, ".webvtt": true,
}

// run is the command: it reads args and writes documents to stdout and
// messages to stderr, and returns the exit status.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gofasthtml", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		format   = flags.String("format", "json", "what to print: json, markdown or text")
		as       = flags.String("as", "", "read every input as html, md, txt, epub, csv, tsv, ipynb, json, yaml, mht, srt or vtt (default: by extension, else by content)")
		delim    = flags.String("delimiter", "", "the delimiter of csv and tsv input: one character, or \\t or tab for tab (default: a sep= first line, else tab for tsv, else sniffed for csv); a sep= first line overrides it")
		noQuotes = flags.Bool("no-quotes", false, "read csv and tsv input with no quoting: a quote is a character like any other, and fields and rows end only at the delimiter and line ends")
		header   = flags.Bool("header", false, "the first row of csv and tsv input is a header row, as the MIME parameter header=present declares")
		cs       = flags.String("charset", "", "the encoding of every input, as a MIME charset label gives it (windows-1251, shift_jis); a byte order mark overrides it. Give it for text that declares no encoding and whose encoding is not recognised")
		parts    = flags.String("parts", "", "chapters of an EPUB to read, by number: 3, or 2-5")
		members  = flags.Int("max-archive-members", 0, "files in an EPUB's archive (0: the default, 100,000; -1: no limit)")
		output   = flags.String("o", "", "write to this file instead of standard output")
		images   = flags.String("images", "meta", "pictures: meta (a block for each, with its alternative text), data (the bytes of data: URIs too) or none")
		spans    = flags.Bool("spans", false, "report bold, italic and the like, and links, as spans")
		maxDepth = flags.Int("max-depth", 0, "how deeply elements may nest (0: the default, 512)")
		maxInput = flags.Int64("max-input-bytes", 0, "size of a file read (0: the default, 256 MiB; -1: no limit)")
		maxBytes = flags.Int64("max-decoded-bytes", 0, "bytes decoded from a file (0: the default, 1 GiB; -1: no limit)")
		maxText  = flags.Int64("max-text-bytes", 0, "bytes of text a document may hold (0: the default, 256 MiB; -1: no limit)")
		quiet    = flags.Bool("quiet", false, "say nothing about files that were read; failures are still reported")
		version  = flags.Bool("version", false, "print the version and exit")
	)
	// The render flags shared by every go-fast command (COMMAND_FLAGS.md).
	renderFlags := gofastcore.AddRenderFlags(flags)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: gofasthtml [flags] file-or-directory...")
		fmt.Fprintln(stderr, "Extracts HTML, Markdown, plain text, EPUB, CSV and TSV. A directory stands for the files of those kinds in it and below it; - is standard input.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *version {
		fmt.Fprintln(stdout, "gofasthtml", moduleVersion())
		return 0
	}
	fail := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "gofasthtml: "+format+"\n", args...)
		return 2
	}
	if flags.NArg() == 0 {
		flags.Usage()
		return 2
	}
	if *format != "json" && *format != "markdown" && *format != "text" {
		return fail("unknown format %q: json, markdown or text", *format)
	}
	switch *as {
	case "", "html", "md", "txt", "epub", "csv", "tsv", "ipynb", "json", "yaml", "mht", "srt", "vtt":
	default:
		return fail("unknown value %q for -as: html, md, txt, epub, csv, tsv, ipynb, json, yaml, mht, srt or vtt", *as)
	}
	var delimiter rune
	switch *delim {
	case "":
	case `\t`, "tab":
		delimiter = '\t'
	default:
		r, n := utf8.DecodeRuneInString(*delim)
		if n != len(*delim) || r == '"' || r == '\r' || r == '\n' || r == utf8.RuneError {
			return fail("-delimiter wants one character other than a quote or a line end, not %q", *delim)
		}
		delimiter = r
	}
	opts := gofastcore.Options{Spans: *spans}
	opts.Limits.MaxInputBytes, opts.Limits.MaxDecodedBytes, opts.Limits.MaxTextBytes = *maxInput, *maxBytes, *maxText
	switch *images {
	case "none":
		opts.Images = gofastcore.ImagesNone
	case "meta":
		opts.Images = gofastcore.ImagesMeta
	case "data":
		opts.Images = gofastcore.ImagesData
	default:
		return fail("unknown value %q for -images: none, meta or data", *images)
	}
	if *parts != "" {
		first, last, ok := strings.Cut(*parts, "-")
		if !ok {
			last = first
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(first))
		b, err2 := strconv.Atoi(strings.TrimSpace(last))
		if err1 != nil || err2 != nil || a < 1 || b < a {
			return fail("-parts wants a number or a range such as 2-5, not %q", *parts)
		}
		opts.Parts = gofastcore.PartRange{First: a, Last: b}
	}
	extractor := &gofasthtml.Extractor{Format: *as, MaxDepth: *maxDepth, MaxArchiveMembers: *members, Charset: *cs,
		Delimiter: delimiter, Header: *header, NoQuotes: *noQuotes}
	// A document an EPUB holds besides its chapters, or an MHTML file
	// besides its page (a frame), is read too if it is one of the formats
	// of this library, by its media type; the charset parameter of a text
	// type is the transport encoding.
	epubs := &gofasthtml.Extractor{MaxDepth: *maxDepth, MaxArchiveMembers: *members}
	opts.Child = func(ctx context.Context, name, mediatype string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		mt, params, err := mime.ParseMediaType(mediatype)
		if err != nil {
			mt, _, _ = strings.Cut(strings.ToLower(mediatype), ";")
			mt = strings.TrimSpace(mt)
		}
		cs := params["charset"]
		switch mt {
		case "application/xhtml+xml", "text/html":
			return (&gofasthtml.Extractor{Format: "html", MaxDepth: *maxDepth, Charset: cs}).Extract(ctx, r, size, o)
		case "text/markdown":
			return (&gofasthtml.Extractor{Format: "md", MaxDepth: *maxDepth, Charset: cs}).Extract(ctx, r, size, o)
		case "text/plain":
			return (&gofasthtml.Extractor{Format: "txt", Charset: cs}).Extract(ctx, r, size, o)
		case "application/epub+zip":
			return epubs.Extract(ctx, r, size, o)
		}
		return nil, gofastcore.ErrUnsupported
	}

	var inputs []string
	status := 0
	for _, arg := range flags.Args() {
		if arg == "-" {
			inputs = append(inputs, arg)
			continue
		}
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(stderr, "gofasthtml: %v\n", err)
			status = 1
			continue
		}
		if !info.IsDir() {
			inputs = append(inputs, arg)
			continue
		}
		var found []string
		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(stderr, "gofasthtml: %v\n", err)
				status = 1
				return nil
			}
			if !d.IsDir() && extensions[strings.ToLower(filepath.Ext(path))] {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gofasthtml: %v\n", err)
			status = 1
		}
		sort.Strings(found)
		inputs = append(inputs, found...)
	}

	out := stdout
	if *output != "" {
		file, err := os.Create(*output)
		if err != nil {
			fmt.Fprintf(stderr, "gofasthtml: %v\n", err)
			return 1
		}
		defer file.Close()
		out = file
	}
	w := bufio.NewWriter(out)
	defer w.Flush()
	// The same options render every document this command prints, an
	// EPUB's included; with Children core renders each embedded document
	// after a line naming it, with these same options.
	render := &gofastcore.RenderOptions{}
	renderFlags.Apply(render)

	written := 0
	for _, path := range inputs {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "gofasthtml: interrupted")
			return 1
		}
		o := opts
		var doc *gofastcore.Document
		var err error
		if path == "-" {
			o.Name = ""
			doc, err = readStdin(ctx, extractor, stdin, &o)
		} else {
			o.Name = filepath.Base(path)
			doc, err = extractor.ExtractFile(ctx, path, &o)
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", path, err)
			status = 1
			continue
		}
		if !*quiet {
			if note := remarks(doc); note != "" {
				fmt.Fprintf(stderr, "%s: %s\n", path, note)
			}
		}
		switch *format {
		case "json":
			enc := json.NewEncoder(w)
			enc.SetEscapeHTML(false)
			if len(inputs) == 1 {
				enc.SetIndent("", "  ")
			}
			err = enc.Encode(doc)
		case "markdown":
			if len(inputs) > 1 {
				if written > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "<!-- %s -->\n\n", strings.ReplaceAll(path, "--", "- -"))
			}
			if err = doc.WriteMarkdown(w, render); err == nil {
				_, err = fmt.Fprintln(w)
			}
		default:
			if len(inputs) > 1 {
				if written > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "==> %s <==\n", path)
			}
			if err = doc.WriteText(w, render); err == nil {
				_, err = fmt.Fprintln(w)
			}
		}
		if err == nil {
			err = w.Flush()
		}
		if err != nil {
			fmt.Fprintf(stderr, "gofasthtml: writing %s: %v\n", path, err)
			return 1
		}
		written++
	}
	return status
}

// readStdin reads standard input, up to the input limit, and extracts it.
func readStdin(ctx context.Context, e *gofasthtml.Extractor, stdin io.Reader, o *gofastcore.Options) (*gofastcore.Document, error) {
	limit := gofastcore.ResolveLimit(o.Limits.MaxInputBytes, gofasthtml.DefaultMaxInputBytes)
	if limit < 1<<62 {
		limit++
	}
	b, err := io.ReadAll(io.LimitReader(stdin, limit))
	if err != nil {
		return nil, err
	}
	return e.Extract(ctx, bytes.NewReader(b), int64(len(b)), o)
}

// remarks sums up what a document's warnings say that a person running
// the command would want to know without reading the JSON: anything of
// severity error.
func remarks(doc *gofastcore.Document) string {
	counts := map[string]int{}
	for _, w := range doc.AllWarnings() {
		if w.Severity == gofastcore.SeverityError {
			counts[w.Code]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for i, code := range codes {
		if n := counts[code]; n > 1 {
			codes[i] = fmt.Sprintf("%s (%d)", code, n)
		}
	}
	return "warnings: " + strings.Join(codes, ", ")
}

// moduleVersion is the version this command was built from.
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
