# go-fast-html implementation plan

The development history of the library and the items still open. What
the library does is in the README.

Module `github.com/AOShei/go-fast-html`, package `gofasthtml`. Extracts
HTML, Markdown, plain text and EPUB into the go-fast-core model: one
`body` part for HTML, Markdown and plain text, a `chapter` part for each
chapter of an EPUB, and a `stored` part for stored data;
`Loc.Offset`/`Length` are byte offsets into the source (for an EPUB, into
the chapter's file); `Loc.Anchor` from `id`. Built on
go-fast-core v0.1.1, schema 0.2.

## Layout

| Path | What |
|---|---|
| `extract.go`, `doc.go` | Public API: `Extractor`, `Extract`, `ExtractFile`, `Detect`, warning codes, defaults |
| `epub.go` | EPUB: chapters, metadata, outline, the stored part and children |
| `internal/charset` | Encoding sniffing (BOM, caller's `Extractor.Charset`, `<meta>` prescan, UTF-8 validity, windows-1252) and decoding to UTF-8 (UTF-8, UTF-16, windows-874/1250-1258, macintosh, Shift_JIS, GBK, EUC-KR, Big5 with HKSCS; tables copied from go-fast-rtf b756076) with a map from decoded offsets back to source offsets |
| `internal/entity` | WHATWG named character references (`entities.txt`, embedded) and the numeric-reference rules; `gen.go` regenerates the table from `entities.json` |
| `internal/lexer` | HTML5 tokenizer (WHATWG section 13.2.5): data, RCDATA, RAWTEXT, script data (with escaped states), PLAINTEXT, tags, attributes, comments, bogus comments, DOCTYPE, CDATA in foreign content, character references |
| `internal/dom` | Tree builder for tag soup: implied end tags, misnested formatting (simplified adoption agency), active formatting reconstruction, foster parenting, foreign content breakout, a nesting depth limit; per-node declared display reasons (hidden attribute, template, SVG display/visibility attributes, inline style) |
| `internal/css` | Inline `style` declarations that matter: display, visibility, opacity, the alpha of color, white-space |
| `internal/blocks` | Walk the tree and emit core blocks, with whitespace collapsing, spans, display ranges, tables, lists, attribute text, `iframe srcdoc`, budget |
| `internal/md` | CommonMark parser with GFM tables, strikethrough, task lists and extended autolinks, rendering to HTML with a map back to Markdown offsets |
| `internal/epub` | Zip reading with a member limit and local-header recovery, OPF, navigation document and NCX |
| `text.go` | Plain text paragraphs |
| `cmd/gofasthtml` | Command printing JSON, Markdown or text |

## Done

- HTML: charset, entities, tokenizer, tree builder, mapping to blocks,
  public API, tests, fuzz targets, worst cases, source hygiene. The
  entity table was compared entry by entry with the WHATWG table (2,125
  names, 106 legacy names).
- Markdown: CommonMark 0.31 blocks and inlines, rendered to HTML with a
  source map and read by the HTML path. All 652 CommonMark 0.31.2 spec
  examples pass (the spec JSON is read from outside the repository). GFM
  tables, strikethrough, task-list items and extended autolinks.
- Plain text, format detection (`Detect`, `Extractor.Format`).
- Command `cmd/gofasthtml`, with tests, and the go-fast series' shared
  render flags (`-furniture`, `-stored[=kinds]`, `-part-markers`,
  `-children`, `-exclude-display[=reasons]`).
- Attribute text (aria-label, aria-description, aria-roledescription,
  aria-placeholder, title, placeholder, area alt) as `note` blocks of
  type `attribute`.
- `thematic_break` for `<hr>` and Markdown breaks.
- EPUB 2 and 3: zip with member limit and local-header recovery, OPF
  metadata, chapters per spine item in XHTML mode, nav/NCX outline,
  manifest images in the stored part, other items as children,
  `encryption.xml`. Unit tests in `internal/epub` and through the root
  package.
- Display: only what the file declares (core 0.2 `Display`, `hidden` and
  `no_ink`); `template` content is `hidden`. Markup comments, script and
  style text and hidden form values are stored data.
- Legacy encodings (windows-874, 1250-1258, macintosh, Shift_JIS, GBK,
  EUC-KR, Big5 with HKSCS) and `Extractor.Charset` / `-charset`.
- `iframe srcdoc`, read in place, with depth counted against
  `Extractor.MaxDepth` and bytes against the decode and text budgets.
- Subtitles: SubRip and WebVTT (`subtitles.go`, `internal/subtitle`), a
  paragraph per cue with its timings, settings, voices and byte offsets;
  WebVTT NOTE, STYLE, REGION and header; UTF-32 decoded for these two
  formats. Table tests, `FuzzSubtitles`, and `TestSubtitleCorpus` with
  two oracles (words at `Loc`; one cue per `-->` line) on the dev files.

## Open

- Subtitles: MicroDVD (`{start}{end}text`, frame numbers) is not read as
  cues; such a file read as `srt` is one paragraph. Arrows written other
  than `-->` (`- >`, `->`) are text. WebVTT cue settings are kept as
  written, not parsed; `X-TIMESTAMP-MAP` is kept in `vtt.header` as
  written. A cue counted with no text says so in `input.repaired`, which
  core describes as damage; a code of core's own for it may fit better.

- Encodings: GB18030, ISO-8859-2..16, KOI8, EUC-JP and ISO-2022-JP are
  read as windows-1252 with `html.charset_unsupported`.
- Markdown: GFM footnotes, extended email autolinks, front matter; the
  GFM spec's own examples as a test (hand-picked GFM cases are tested).
- EPUB: `media-overlay`, `page-list`/landmarks navigation, fixed layout
  and `rendition:*` properties are not read.
- `iframe srcdoc`: the srcdoc document's `title` and `meta` are not read.
- Tables are not split into several blocks when very long (core allows
  `Continued` pieces); only the text budget bounds them.
- Core proposals: a note type for attribute text, a stored value for
  manifest items (both are library-local strings today).
- A differential check of the tree builder against the html5lib-tests
  tree-construction data.
