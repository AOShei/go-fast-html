# go-fast-html implementation plan

Module `github.com/AOShei/go-fast-html`, package `gofasthtml`. Extracts
HTML, Markdown and plain text into the go-fast-core model (one `body` part;
`Loc.Offset`/`Length` are byte offsets into the source; `Loc.Anchor` from
`id`). go-fast-core pinned at commit ebecf3c, schema 0.2
(`v0.0.0-20261120130045-ebecf3cf2089`).

## Layout

| Path | What |
|---|---|
| `extract.go`, `doc.go` | Public API: `Extractor`, `Extract`, `ExtractFile`, warning codes, defaults |
| `internal/charset` | Encoding sniffing (BOM, caller's `Extractor.Charset`, `<meta>` prescan, UTF-8 validity, windows-1252) and decoding to UTF-8 (UTF-8, UTF-16, windows-874/1250-1258, macintosh, Shift_JIS, GBK, EUC-KR, Big5; tables copied from go-fast-rtf b756076) with a map from decoded offsets back to source offsets |
| `internal/entity` | WHATWG named character references (`entities.txt`, embedded) and the numeric-reference rules; `gen.go` regenerates the table from `entities.json` |
| `internal/lexer` | HTML5 tokenizer (WHATWG section 13.2.5): data, RCDATA, RAWTEXT, script data (with escaped states), PLAINTEXT, tags, attributes, comments, bogus comments, DOCTYPE, CDATA in foreign content, character references |
| `internal/dom` | Tree builder for tag soup: implied end tags, misnested formatting (simplified adoption agency), active formatting reconstruction, foster parenting, foreign content breakout, a nesting depth limit; per-node declared display reasons (hidden attribute, template, SVG display/visibility attributes, inline style) |
| `internal/css` | Inline `style` declarations that matter: display, visibility, opacity, the alpha of color, white-space |
| `internal/blocks` | Walk the tree and emit core blocks, with whitespace collapsing, spans, display ranges, tables, lists, budget |
| `internal/md` | CommonMark parser rendering to HTML with a map back to Markdown offsets |
| `text.go` | Plain text paragraphs |
| `cmd/gofasthtml` | CLI printing JSON, Markdown or text |

## Phases

1. **HTML (run 1)**: charset, entities, tokenizer, tree builder, mapping,
   public API, tests, fuzz targets, README. Status: done.
2. **Markdown, plain text, CLI (run 2)**: CommonMark parser to the same
   blocks, plain text (paragraphs by blank lines), `cmd/gofasthtml`, and
   anything left from phase 1. Status: done; open items below.

## Status

- [x] Repository setup, plan
- [x] internal/charset (tests, fuzz)
- [x] Legacy encodings (windows-874, 1250-1258, macintosh, Shift_JIS, GBK,
      EUC-KR, Big5) and `Extractor.Charset` / `-charset`; FuzzDoubleByte.
      Big5 with HKSCS (big5-hkscs.txt after cp950.txt, four two-code-point
      pairs in code). Open: GB18030, ISO-8859-2..16, KOI8, EUC-JP and
      ISO-2022-JP are still read as windows-1252 with the warning.
- [x] internal/entity (compared entry by entry with the WHATWG table by
      the orchestrator: 2,125 names and 106 legacy names, all correct)
- [x] internal/lexer (tests, fuzz)
- [x] internal/dom (tests, fuzz, depth/stack bounds)
- [x] internal/css (tests, fuzz)
- [x] internal/blocks + public API (`extract.go`, `doc.go`)
- [x] Tests: structure, display, metadata, locations, charset, spans,
      warnings, images, errors, budgets, worst cases, FuzzExtract with
      Validate, source hygiene, imports
- [x] README, NOTICE
- [x] Unit tests for internal/blocks (run 2)
- [x] Markdown (run 2): `internal/md`, CommonMark 0.31 blocks and inlines,
      GFM tables; rendered to HTML with a source map and read by the
      HTML path. Tests, fuzz target, worst cases in all three formats.
- [x] Plain text (run 2): `text.go`
- [x] Format detection (`Detect`, `Extractor.Format`)
- [x] CLI `cmd/gofasthtml` (run 2), with tests
- [x] CommonMark 0.31.2 spec examples as a test (run 4): 652/652 pass;
      the spec JSON is read from outside the repository
- [x] GFM strikethrough, task-list items, extended autolinks (run 4)
- [ ] GFM footnotes; front matter; the GFM spec's own examples (not
      available offline; hand-picked GFM cases are tested)

## Phase 3 (run 3)

- [x] `aria-hidden` no longer a hidden reason (user decision 2026-11-20)
- [x] Unshown attribute text (aria-label, aria-description,
      aria-roledescription, aria-placeholder, title, placeholder, area
      alt) as `note` blocks of type `attribute` (no display reason since
      the declared-not-judged change)
- [x] `KindThematicBreak` for `<hr>` and Markdown breaks; core pinned at
      df335f0
- [x] EPUB 2/3 (`epub.go`, `internal/epub`): zip with member limit and
      local-header recovery, OPF metadata, chapters per spine item in
      XHTML mode, nav/NCX outline, manifest images in a stored part,
      other items as children, encryption.xml; command accepts .epub
- [ ] Unit tests in `internal/epub` (covered through the root package)
- [ ] EPUB: `media-overlay`, `page-list`/landmarks navigation, fixed
      layout and `rendition:*` properties are not read
- [ ] Core proposals: a note type for attribute text, a stored value for
      manifest items (both are library-local strings today)

## Declared, not judged (core 0.2)

- [x] Pin core 05bbddb; `Display` (`hidden`, `no_ink`), `Part.Finish`
- [x] Report only declarations: the hidden attribute, inline display,
      visibility, opacity 0, colour alpha 0, SVG display/visibility
      attributes, template content (template: decided by Andrew, 2026-11-20)
- [x] Removed: the size, contrast, position and clipping checks and
      their parsers; the out-of-proportion rule; HiddenChecked; two
      warnings; the EPUB navigation exemption (details in CHANGELOG.md)
- [x] Command: `-exclude-hidden` replaced by `-exclude-display`
- [x] Command: the shared render flags of COMMAND_FLAGS.md
      (`-furniture`, `-stored`, `-part-markers`), and the
      `-exclude-display` help text and error aligned with it (2026-11-20)
- [x] Pin core 787b056; command `-children` (default true) sets
      `RenderOptions.Children`, so an EPUB's other documents render
      after a line naming each (2026-11-20)
- [x] Pin core ebecf3c; markup comments are stored data
      (`stored=markup_comment`, no note type) in HTML, Markdown and
      EPUB; command `-stored[=kinds]` per COMMAND_FLAGS.md, with this
      library's kinds `manifest_item` and `stylesheet` (2026-11-20)
- [x] Tests: each declaration, one former inferred case per removed
      check, characters kept (HTML, Markdown, plain text, EPUB)

## Open items from phase 1

- Layout tables (a whole page in table cells) are emitted as one table;
  recognizing them and emitting their content as blocks is not done.
- [x] `iframe srcdoc`: done (2026-11-20), in place rather than as a
  child, since it is shown in the page; depth counted with
  `Extractor.MaxDepth`, bytes with the decode and text budgets. Open:
  the srcdoc document's `title`/`meta` are not read.
- `placeholder`, `title` attributes and `area alt` are not read.
- Tables are not split into several blocks when very long (core allows
  `Continued` pieces); only the text budget bounds them.
- Run a differential check of the tree builder against html5lib-tests
  tree-construction data if it can be obtained (not available offline).
