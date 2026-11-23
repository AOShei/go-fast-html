# Changelog

## v0.4.1 (2026-11-24)

- **go-fast-core v0.1.4** (was v0.1.2): `Document.Text` and `Markdown`
  allocate their result once. The output is unchanged.

## v0.4.0 (2026-11-23)

Encodings the benchmark against Common Crawl found missing, a `<meta>`
declaration past the first 1024 bytes, and speed: about 1.75 times
faster with one worker and 2 times with 24 on the 9,835 pages of
`bench/`, the output of the speed work unchanged byte for byte.

### Encodings

- **Decoded now:** GB18030 (with its four-byte sequences), EUC-JP,
  IBM866, ISO-8859-2 to 8 (and 8-i), 10 and 13 to 16, KOI8-R, KOI8-U and
  x-mac-cyrillic, with the WHATWG Encoding Standard's labels, as declared
  in a `<meta>`, given as `Extractor.Charset`, or carried by a MIME
  `charset`. They were read as windows-1252 with
  `html.charset_unsupported`. The single-byte tables are generated from the
  standard's index files (`internal/charset/gen/gen_single.py`); GB18030
  and EUC-JP use go-fast-email's tables. ISO-2022-JP is still not decoded.
- **A `<meta>` past the first 1024 bytes.** When those bytes declare no
  encoding and the input is not valid UTF-8, a declaration in the first
  MiB is used, as the HTML standard's parser changes the encoding when it
  meets one, instead of the encoding recognised from the bytes.
- On the 9,835 Common Crawl pages of `bench/`, 16 read differently: 9
  pages in EUC-JP and 5 in ISO-8859-2 that were garbage, and 2 with a
  late `<meta>`; the pages refused for their encoding go from 10 to 7, and
  with each page's HTTP charset given, from 7 to 3 (pages that declare
  nothing).
- `TestDecodeDeclared` decodes texts written for the tests in each of
  these encodings back to the original exactly; `FuzzDoubleByte` covers
  GB18030 and EUC-JP.

### Faster

- **Memory reused from one document to the next.** The input buffer
  (HTML, Markdown, text, CSV, TSV, JSON, YAML, notebooks) and the blocks of
  the tree's nodes and attributes go back to pools when a page is done:
  a batch allocates 0.97 GB for 3,000 pages instead of 2.3 GB, and with 24
  workers reads the 9,835 pages of `bench/` in 0.82 s instead of 1.03 s,
  for a peak of about 300 to 400 MB. Nothing of a document refers to that
  memory: `TestInputReused` extracts a document of each format, then many
  that reuse its memory, and checks it did not change (and fails if a
  copy is taken out), also on eight goroutines at once. An EPUB's input is
  not reused.
- **Fewer passes over scripts, style sheets and attribute text:** a
  `<script>` or `<style>` of one text with nothing to drop is copied once;
  a `title`, `aria-label` or metadata value with nothing to collapse or
  drop is not rebuilt.
- **`GOGC` documented** (README): Go's default, 100, which the command
  and the library keep, uses the least memory; `GOGC=200` reads the batch
  with 24 workers in 0.70 s for a peak of about 730 MB, `GOGC=400` in
  0.63 s for about 1 GB. The choice of memory for speed is the user's.

- **The tokenizer copies nothing it need not.** Text, attribute values and
  names with no character reference are slices of the page, not copies,
  and a run of text is found with a byte search; the tree's text nodes are
  strings sharing that memory. What is kept past the extraction (link
  targets, anchors, names) is copied, so that a document does not hold its
  page; `TestNoAliasOfPage` checks every string of the result.
- **Fewer allocations:** the tree's nodes and the attributes of tags are
  allocated in blocks, and the list of blocks starts near the size a page
  of its length has. From 28 million allocations for 3,000 pages to 5.4
  million, and 3.6 GB to 2.5 GB.
- **The text buffer copies runs** of characters that need nothing done
  whole, found with a table of byte classes, instead of a character at a
  time.
- The scopes of the HTML standard's "has an element in scope" are bits on
  each element instead of maps; an address or name with no control
  character is not rebuilt; valid UTF-8 is checked once, not twice.
- With one worker the 9,835 pages take 5.8 to 6.0 s (10.3 s before), with
  24 workers 1.05 s (2.1 s); peak memory with 24 workers 199 to 231 MB (264
  MB). `BenchmarkCorpusPages` and `TestCorpusIdentity` (both on pages
  `GOFASTHTML_PAGES` lists, skipped without it) measure the speed and check
  the output against a run before a change.

## v0.3.0 (2026-11-23)

JSON and YAML as text; text that declares no encoding read in the one
its bytes are certainly in, or refused; the control characters removed
reported; and the links of inline elements that hold blocks given.
Requires go-fast-core v0.1.2.

For a file that is UTF-8 or declares its encoding, the output differs
from v0.2.0 only in spans (below) and in the new warning
`text.control_characters_removed`. A file named `.json`, `.jsonl`,
`.ndjson`, `.geojson`, `.yaml` or `.yml`, which `Detect` read as plain
text, is now read in its format.

- **New formats `json` and `yaml`** (`application/json`,
  `application/yaml`), detected by the name only. One part of kind `body`
  of `code` blocks whose language is the format, their text as written
  (indentation and ligatures kept, control characters dropped, line ends
  LF, CR LF or CR), nothing parsed. A YAML file has a block per document
  (`---`, and after `...`); a long document is split at line ends into
  pieces of about 1,000 lines and 256 KiB, the later ones `Continued`,
  which joined with line feeds give it back. A line is never cut. Each
  block is located by byte offset. Where the text budget runs out, the
  number of lines not read is given.
- **Command.** `-as json|yaml`; a directory includes these files.
- `FuzzData`, in CI's fuzz smoke step.

### Text that declares no encoding

Input with no byte order mark, no `Extractor.Charset` and no `<meta>`
declaration that is not valid UTF-8 was read as windows-1252, as the
Encoding standard says, which made Japanese, Chinese, Korean and Russian
text garbage. Its encoding is now recognised from its bytes where one is
certain, and the input is refused where none is. Input that is valid
UTF-8 or declares its encoding is not judged, and reads as before.

- **Recognised**: UTF-8 with damaged bytes, UTF-16 without a byte order
  mark (but Han text), Shift_JIS, EUC-KR, GBK, Big5, windows-1251, and
  windows-1252 for text whose accented letters stand alone in words and
  show nothing of another Latin code page. The tests are in the README;
  the 1,000 most frequent Chinese characters come from the Unicode
  Consortium's Unihan database (`internal/charset/common.go`, see
  `NOTICE`).
- **`text.encoding_detected`** (warning; a go-fast-core v0.1.2 code) names
  the encoding a document was read in when it was recognised from its
  bytes.
- **`ErrEncodingUnknown`** (wraps `gofastcore.ErrUnsupported`) for input
  whose encoding is not certain, with a message that says to give it with
  `Extractor.Charset` (command: `-charset`). A chapter of an EPUB is read
  as windows-1252 instead, with the warning.
- **Measured** on Wikipedia articles in 30 languages in the code pages of
  each: no file and no window of 1 KiB or more read in a wrong encoding.
  The time is bounded: at most about 0.25 ms a file, 2 to 14 µs for 1 KiB.
- **Input that is valid UTF-8 but has zero bytes** is tested for UTF-16
  first, since UTF-16 of ASCII and Cyrillic is valid UTF-8.
- A page of the public corpus (`tika-big-preamble.html`) that declares
  windows-1251 after its first 1,024 bytes, too late for the prescan, was
  garbage and now reads as Russian.
- The double-byte code pages' tables are found by a switch instead of a
  map, for every character decoded.

### Control characters removed are reported

Control characters with no meaning as text (NUL, ESC, vertical tab, the
C1 controls) were removed from text silently, as the text conventions
require. They are now counted, and the part (or, for the metadata and an
EPUB's table of contents, the document) has `text.control_characters_removed`
(info, a go-fast-core v0.1.2 code) with the count. Tab, line ends and form
feed are not counted, nor what is removed from addresses and names. This
library now requires go-fast-core v0.1.2.

### Faster

- **Text not in UTF-8 is located as fast as UTF-8.** Finding each
  block's byte range in a windows-1252, UTF-16, Cyrillic, Shift_JIS,
  GBK, EUC-KR or Big5 file decoded up to 4 KiB of the file again for
  each block; the map back to the source now goes on from where it last
  stopped. 4 MiB of short paragraphs in windows-1252 took 1.3 s and
  takes 48 ms (UTF-8: 31 ms); in UTF-16, 1.7 s and 57 ms. The output is
  the same, byte for byte.

### Fixed

- **Spans of an inline element that holds a block.** A link or style
  around block content (`<a><h3>…</h3><p>…</p></a>`, the "card" of news
  and government sites, or `<b>…<p>…</p>…</b>`) had a span only on its
  last block, from the offset its text had in the block before: the part
  in the earlier blocks had none, and the span could start in the middle
  of a character, which `Validate` rejects. Each block now has the
  element's span over its own part. On four pages of the public corpus,
  193 links that were missing are given; no span moved or went. Output
  without spans is unchanged.

## v0.2.0 (2026-11-22)

Comma- and tab-separated values, and Jupyter notebooks. Output for the
other formats is unchanged; a file named `.csv`, `.tsv`, `.tab` or
`.ipynb` that `Detect` read as plain text (or HTML) is now read in its
format.

### CSV and TSV

- **New formats `csv` and `tsv`** (`text/csv`, `text/tab-separated-values`),
  detected by the name only (`.csv`; `.tsv`, `.tab`), never by content.
  One part of kind `sheet`, named by the file; its rows are `table`
  blocks, padded to the widest row, split every 1,000 rows (or 2^20
  cells) with `Continued` and an A1 `Loc.Range` on each piece. Trailing
  rows with no text are left out. No header row is inferred; cells are
  text.
- **The delimiter**: an Excel `sep=` first line, the new
  `Extractor.Delimiter`, tab for `tsv`, or sniffed among `,` `;` tab `|`
  for `csv`; recorded in `Metadata.Extra["delimiter"]` and
  `["delimiter_source"]`.
  The delimiter is written `U+XXXX` when it is a control character other
  than tab or a ligature. A first row longer than the 64 KiB sniffing
  window is skipped so that the rows after it decide.
- **`Extractor.Header`** declares a header row (MIME `header=present`).
- **Quoting**: on by default for csv and tsv alike (Excel, pandas,
  Python's `excel-tab`); the new `Extractor.NoQuotes` (`-no-quotes`)
  reads quotes as characters, as IANA's `text/tab-separated-values` has
  it.
- **Rows with no text** are decided after control characters are
  dropped: a trailing row of control characters is left out, and a file
  of them alone has no table and `part.no_text`.
- **Lenient RFC 4180 reading**, with one `input.repaired` per part giving
  the number of rows output with a quote outside the rules (rows the
  text budget refuses are not counted, nor are their cut fields).
- **Bounds**: fields past column 16,384 (`MaxCSVColumns`) and field text
  past 1 MiB (`MaxCSVField`) give `limit.exceeded` with a count; the text
  budget cutting the sheet gives the number of rows not read.
- **Command**: `-as csv|tsv`, `-delimiter`, `-header`; directories give
  their `.csv`, `.tsv` and `.tab` files. An EPUB's embedded CSV is not
  handed to the CSV reader (unchanged).
- `FuzzCSV`.

### Jupyter notebooks

- **New format `ipynb`** (`application/x-ipynb+json`), detected by the
  name only, never by content: notebook format versions 4 (4.0 to 4.5)
  and 3. One part of kind `body`, the cells in order, each read on its
  own. Versions 1 and 2 give `ErrUnsupported`; input that is not a JSON
  object with cells, `ErrMalformed`.
- **Cells.** Markdown cells are read as Markdown, with their attached
  images (`attachment:name`). Code cells are a `code` block with the
  notebook's language. Raw cells are a `code` block with no language.
- **Outputs** follow their cell, each block with `Attrs["output"]`
  (`AttrOutput`): `stdout`, `stderr`, `execute_result`, `display_data` or
  `error`. Of an output's representations the one JupyterLab prefers is
  read: HTML (a DataFrame is its table), Markdown, LaTeX (a `formula`
  block), an image, then plain text. Printed text and tracebacks are read
  as JupyterLab shows them: escape sequences left out, backspaces
  taking out a character, carriage returns writing over their line.
- **What the notebook declares.** A collapsed input or collapsed outputs
  are `hidden`.
- **Location and metadata.** A cell's `id` is `Loc.Anchor` of its blocks.
  The title, authors, kernel, language and its version, and the format
  version go to `Metadata`.
- **Bounds.** Where the text or decode budget runs out the body is cut
  off, with the number of cells not read; a value of the wrong JSON type
  is left out, with one `input.repaired` and the count.
- **Command.** `-as ipynb`; a directory includes `.ipynb` files.
- `FuzzNotebook`, in CI's fuzz smoke step.
- **Internal.** Reading an HTML page into blocks and reporting what the
  walk found are now two steps (`walk`, `report`), so that a notebook's
  cells can be walked one by one and reported once; the output of every
  other format is byte-identical over the corpus.

## v0.1.2 (2026-11-22)

Every fixed bound that cuts a string of the output now reports it with
`limit.exceeded` (severity error), once per bound per part, with a
count, as the series' rule says. Output for input that reaches none of
these bounds is unchanged.

- **`html.metadata_truncated` is no longer raised**; the constant
  `WarnMetadataTruncated` stays, deprecated. Its cases are
  `limit.exceeded` on the document: metadata values over 64 KiB (HTML
  `<title>` and `meta` values, EPUB package metadata text), `author` and
  `keywords` declarations past 256, Dublin Core elements past 4,096. A
  value is counted only where it is used (a second `description` is not).
- **`<html lang>` over 64 bytes**, left out of `Metadata.Language`, now
  gives the document `limit.exceeded`.
- **An `id` (or an `a`'s `name`) over 1,024 bytes**, not used as
  `Loc.Anchor`, and **a code block's language over 64 bytes** (HTML class
  or Markdown info string), left out of `Attrs["language"]`, now give the
  part `limit.exceeded` with the count.
- **An EPUB chapter's `<title>` over 64 KiB**, cut as the chapter's name,
  gives the chapter `limit.exceeded`. The chapter's other metadata, which
  the document does not use, gives no warning (before, any of it gave the
  document `html.metadata_truncated`).
- **EPUB manifest items and spine entries past 2^20**, left out, now give
  the document `limit.exceeded`.
- **Number attributes** (`start`, `value`, `colspan`, `rowspan`, `width`,
  `height`) are no longer cut to their first 12 characters, which read a
  longer number as a different one: up to 18 digits are read in full,
  and a longer number is invalid and ignored.

## v0.1.1 (2026-11-22)

- Built on go-fast-core v0.1.1: a part or document keeps at most 1,000
  warnings, and the rest are counted in one `warnings.omitted` warning.
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
