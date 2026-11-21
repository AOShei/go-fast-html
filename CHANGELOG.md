# Changelog

## v0.1.1 (not yet tagged)

Fixed bounds that cut or dropped content without saying so, and a warning
that could repeat without bound. Output for input that reaches none of
these bounds is unchanged, with two exceptions: an EPUB whose
`encryption.xml` is damaged, truncated or otherwise unreadable now has
its content withheld (below) instead of being read as if it had no
encryption, and an EPUB with encrypted images now says so where they
stand.

- **EPUB `encryption.xml` that cannot be read in full** (larger than
  16 MiB or the decode budget, listing more than 2^20 members, damaged,
  or XML that cannot be read to its end) no longer counts as no
  encryption, which read encrypted chapters as text. Which members are
  encrypted is then not known, so every member but `mimetype`,
  `META-INF/` and the package document is treated as possibly encrypted:
  no chapter, navigation document, NCX, image data or embedded document
  is read. With every selected chapter withheld the result is
  `ErrLimitExceeded` (too large, too many members) or `ErrMalformed`
  (damaged); a chapter withheld in a document that is returned carries
  `part.failed` and `epub.encrypted`, the stored part `epub.encrypted`
  for its images without data, and the document `limit.exceeded` or
  `epub.encrypted`.
- **EPUB text budget.** When the text budget stops the reading of
  chapters, the document carries `limit.exceeded` naming the chapter it
  was reached in and what was not read, as the publication has them: the
  later chapters, the stored part (the scripts, styles and form values of
  the chapters read), the images of the manifest no chapter read shows
  (some may be ones a later chapter shows) and the embedded documents. A chapter
  whose name was left out for the budget carries `limit.exceeded`, once:
  when the budget also cut the chapter's text, that warning says the
  name was left out too. Before, a budget spent on a chapter's name ended
  the publication there silently. When the budget stops the manifest's
  other items, the document says how many images and embedded documents
  were not read (the item it stopped at counted there only), and carries
  the stored part's warnings when there is no stored part.
- **EPUB chapter limit.** Past `Limits.MaxParts`, the document's
  `limit.exceeded` names what was not read, as the text budget's does,
  no longer only "the rest".
- **EPUB embedded documents past the decode budget** give the document
  one `limit.exceeded` with their number, besides each child's
  `child.not_extracted`.
- **EPUB table of contents.** A navigation document or NCX larger than
  16 MiB (or the decode budget), more than 100,000 entries, lists nested
  deeper than 64 levels and titles longer than 4 KiB are reported with
  `limit.exceeded`, once each. A title is cut at 4 KiB whether its text
  is one node or several (before, one long text node was not cut, and the
  NCX's last entry at the bound lost its title).
- **EPUB metadata.** The text of a metadata element is cut at exactly
  64 KiB however it is split (before, only text in several pieces was
  cut), and Dublin Core elements past 4096 are counted; both carry
  `html.metadata_truncated`.
- **EPUB images.** An image whose data passes the decode budget gives
  its chapter, or the stored part for an image no chapter shows,
  `limit.exceeded` with the count (before, a stored image's data was
  dropped silently). An image given as a `data:` URI whose bytes pass the
  decode budget is not decoded, so it has no ID and no data; its part
  now carries `limit.exceeded` with the count, in every images mode
  (before, with `ImagesData` it counted under `image.unsupported_encoding`,
  and otherwise it had no warning). `image.unsupported_encoding` now
  counts only images that could not be read or decoded for other reasons
  (a malformed `data:` URI, a damaged member).
- **EPUB encrypted images.** An image a chapter shows that
  `encryption.xml` lists is no longer read as data: its block has no
  data, and the chapter carries `epub.encrypted` with the count. An
  image no chapter shows that is (or, when `encryption.xml` was not read
  in full, may be) encrypted gives the stored part `epub.encrypted` with
  the count.
- **EPUB `container.xml` or package document larger than 16 MiB** is
  `ErrLimitExceeded`, no longer `ErrMalformed`.
- **`html.charset_unsupported`** is one warning for an EPUB, with the
  number of chapters and each label (the first 8) with its count, no
  longer one per chapter.
- **Tree builder bounds.** More than 64 formatting elements open at once
  (the earliest are not reopened after a block), the bound on formatting
  elements reopened (past it they are not put in the tree), and a `style`
  attribute longer than 16 KiB (declarations past it are not read) each
  give the part `limit.exceeded` once: these lose spans, anchors or a
  declared display, never text. The bounds are unchanged.

## v0.1.0 (2026-11-21)

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
