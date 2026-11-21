# Changelog

## v0.1.0 (not yet tagged)

The first release. Built on go-fast-core v0.1.0, schema `0.2`.
Standard library and go-fast-core only.

- **HTML.** An HTML5 tokenizer and tree builder of its own that recover
  from tag soup the way browsers do. One `body` part; blocks with their
  byte range in the source and the `id` of their element; metadata from
  `<title>`, `<meta>` and `<html lang>`; `iframe srcdoc` read in place.
  Script and style text and `<input type=hidden>` values go to a `stored`
  part; markup comments are stored data where they stand.
- **Markdown.** CommonMark 0.31.2 (all 652 spec examples pass) with the
  GFM tables, strikethrough, task lists and extended autolinks, rendered
  to HTML and read by the HTML path, located in the Markdown source.
- **Plain text.** Paragraphs split at blank lines and form feeds, line
  breaks kept.
- **EPUB 2 and 3.** A `chapter` part for each spine item, read as XHTML;
  Dublin Core metadata; the navigation document or NCX as `Outline`;
  images no chapter shows in the `stored` part; other manifest items as
  children; encrypted resources reported and not read.
- **Display.** Only what the file declares: `hidden` (the `hidden`
  attribute, inline `display: none`, `visibility: hidden`/`collapse`, SVG
  `display`/`visibility` attributes, `template` content) and `no_ink`
  (inline `opacity: 0`, a colour with alpha 0). Stylesheets and scripts
  are not evaluated; nothing is fetched.
- **Encodings.** UTF-8, UTF-16LE, UTF-16BE, windows-1252, windows-874,
  windows-1250 to windows-1258, macintosh, Shift_JIS, GBK, EUC-KR and
  Big5 with HKSCS, by their WHATWG labels; chosen by byte order mark,
  `Extractor.Charset`, `<meta>` prescan (HTML), UTF-8 validity, else
  windows-1252.
- **Format detection.** `Detect` by file extension, else by content.
- **Command** `cmd/gofasthtml`: JSON, Markdown or text for files,
  directories and standard input. Flags: `-format`, `-as`, `-charset`,
  `-parts`, `-max-archive-members`, `-o`, `-images`, `-spans`,
  `-max-depth`, `-max-input-bytes`, `-max-decoded-bytes`,
  `-max-text-bytes`, `-quiet`, `-version`, and the go-fast series' shared
  render flags `-furniture`, `-stored[=kinds]`, `-part-markers`,
  `-children` and `-exclude-display[=reasons]`.

## Before v0.1.0

### Core v0.1.0

- go-fast-core pinned at its v0.1.0 tag. Nothing changes here: since
  `4499357` core changed documentation, and `TextBudget.Take` now takes a
  negative amount as 0, which this library never passes.

### Core 4499357: closed value sets, shared render flags

- go-fast-core pinned at `4499357`. The stored kinds `manifest_item` and
  `stylesheet` and the note type `attribute` are now core's
  (`gofastcore.StoredManifestItem`, `StoredStylesheet`, `NoteAttribute`);
  `StoredManifestItem` and `StoredStylesheet` here are the same values.
  `StoredKindNames` is removed: core's `StoredKindNames()` lists every kind.
- Command: the render flags are core's `AddRenderFlags`; their names,
  values and errors are unchanged, and `-stored`'s help text gives core's
  examples.

Development history. Nothing was released before v0.1.0.

### Core d4cd8e2

- go-fast-core pinned at `d4cd8e2`: `Validate` now rejects an inferred
  `thematic_break`; this library emits only declared ones and passes.

### Declared, not judged

- go-fast-core pinned at 05bbddb, schema `0.2`. `Hidden` is now `Display`, with the reasons `hidden` and `no_ink`; JSON key `display`.
- Only what the page declares is reported: the `hidden` attribute, inline `display: none`, `visibility: hidden`/`collapse`, `opacity: 0`, a colour with alpha 0, SVG `display`/`visibility` attributes, and `template` content (newly `hidden`).
- Removed, with their code: the `tiny`, `low_contrast`, `off_page` and `clipped` checks and the inline-style parsing that served only them (font size, background, offsets, clip); the out-of-proportion (outlier) rule for comments, attribute text and form values, with its single-token exemption; `Document.HiddenChecked`; the warnings `text.hidden` and `text.unshown_outlier`.
- The `hidden` attribute on the EPUB 3 navigation document's `nav` and `ol` elements is now reported like any other declaration (the exemption is removed).
- Invisible characters are no longer scanned for or reported; they stay in the text.
- Command: `-exclude-hidden` is replaced by `-exclude-display[=hidden,no_ink]`.
- Command: `-furniture`, `-stored` and `-part-markers` added, the render flags shared by every go-fast command; `-exclude-display`'s help text matches theirs, and its error for an unknown reason names only `hidden, no_ink`.
- go-fast-core pinned at 787b056 (adds `RenderOptions.Children`). Command: `-children` (default true) renders embedded documents, an EPUB's documents beside its chapters, in Markdown and text, each after a line naming it; `-children=false` leaves them out. An EPUB with such documents now prints them in text and Markdown by default.
- go-fast-core pinned at ebecf3c (adds `RenderOptions.StoredKinds`, `StoredMarkupComment`, `StoredKindNames`).
- A markup comment (`<!-- -->`, in HTML, raw HTML in Markdown and EPUB chapters) is stored data: still a `note` where it stands, now with `Attrs["stored"]` `markup_comment` and without `type=comment` (core's `NoteComment` is a reviewer's comment). Comments are no longer in default text and Markdown. New public `StoredStylesheet` and `StoredKindNames` (`manifest_item`, `stylesheet`).
- Command: `-stored[=kinds]`: alone, `=true` or `=all` every kind; `=false` or `=` off; `=k1,k2` (or `k1+k2`) those kinds only; any other name is an error listing the kinds, exit status 2.
- Encodings: windows-874, windows-1250 to windows-1258, macintosh, Shift_JIS (code page 932), GBK (936), EUC-KR (949) and Big5 (950) are decoded, with their WHATWG labels, from tables copied from go-fast-rtf (b756076). They were read as windows-1252 with `html.charset_unsupported` before. `gb18030` is still not decoded.
- Big5 includes HKSCS: pairs code page 950 leaves undefined are read from `big5-hkscs.txt` (generated from Python's `big5hkscs` codec), including characters above U+FFFF, and 0x8862, 0x8864, 0x88A3, 0x88A5 decode to a letter and a combining mark. They were U+FFFD before.
- `iframe srcdoc`: the document in the attribute is parsed as HTML and its blocks come in place, where the iframe stands, located at the iframe; the iframe's own display reasons apply to all of it. Nesting counts against `Extractor.MaxDepth` (`limit.exceeded` when a srcdoc is at the limit), and its bytes and text against the decode and text budgets. Its `title`, `meta` and `lang` are not taken as the page's metadata. Before, srcdoc was not read.
- `Extractor.Charset` (command: `-charset`): the encoding the input comes with, as a MIME charset label; after a byte order mark, before `<meta>` and detection. A label not decoded is ignored with `html.charset_unsupported`.
