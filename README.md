# go-fast-html

Extracts HTML, Markdown, plain text and EPUB into the shared go-fast document model, `gofastcore.Document` (`github.com/AOShei/go-fast-core`). Standard library and go-fast-core only.

```go
import "github.com/AOShei/go-fast-html" // package gofasthtml

doc, err := gofasthtml.ExtractFile(ctx, "page.html", nil)
if err != nil { ... }
fmt.Print(doc.Markdown())
```

`Source.Format` is `html`, `md`, `txt` or `epub`. `Extractor.Format` sets the format; when it is empty, `Detect` decides it from `Options.Name` (`.html`, `.htm`, `.xhtml`, `.shtml`; `.md`, `.markdown`, `.mdown`, `.mkd`, `.mkdn`, `.mdwn`; `.txt`, `.text`; `.epub`) and, failing that, from the content: a zip archive whose `mimetype` member comes first and says `application/epub+zip` is EPUB, input that begins with markup, or has a tag of a common HTML element in its first 4 KiB, is HTML, and anything else plain text. Markdown is never guessed from content, since reading plain text as Markdown takes characters out of it.

The command `cmd/gofasthtml` prints JSON, Markdown or text for files, directories and standard input (`-`); `-as html|md|txt|epub` sets the format. It offers the render flags every go-fast command shares; they affect Markdown and text only, for the document and an EPUB's documents alike, and JSON always has everything:

| Flag | Effect in Markdown and text |
|---|---|
| `-furniture` | include running headers and footers |
| `-stored[=kinds]` | include stored data (data the file keeps but shows nowhere): all of it (`-stored`, `=true`, `=all`), or with `=kinds` (comma or `+`) only those kinds, wherever they are; `=false` or `=` turn it off. The kinds are core's `gofastcore.StoredKindNames()` (such as `markup_comment`, `macro`, `stylesheet`, `manifest_item`); any other name is an error listing them (exit status 2) |
| `-part-markers` | put a line before each part saying which part it is |
| `-children` (default true) | include embedded documents (an EPUB's documents beside its chapters), each after a line naming it (`[child 1: name]` in text); `-children=false` leaves them out |
| `-exclude-display[=reasons]` | leave out what the file declares not displayed: all of it (alone, `=true` or `=all`), or only the reasons named (`=hidden`, `=no_ink`, `=hidden,no_ink`); `=false` or `=` is off, and any other value is an error |

This library produces no running headers and footers itself; `-furniture` is there for the documents other extractors may return as children. Its stored part holds scripts, styles, `<input type=hidden>` values and EPUB images no chapter shows; markup comments are stored data too, kept where they stand. `-stored=markup_comment` shows the comments alone (Outlook's conditional comments, for example), `-stored=macro` the scripts, `-stored=stylesheet` the styles.

**Status: v0.1.2 (2026-11-22).** The changes are in [`CHANGELOG.md`](CHANGELOG.md); the development history and the items still open are in [`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md).

## What is read

The library has its own HTML5 parser under `internal/`, since the standard library has none:

- **Encoding** (`internal/charset`), as the WHATWG encoding sniffing decides it: a byte order mark, then `Extractor.Charset` (command: `-charset`), then a `<meta charset>` or `<meta http-equiv="Content-Type">` found by the prescan of the first 1024 bytes, then UTF-8 if the whole input is valid UTF-8, otherwise windows-1252. Decoded are UTF-8, UTF-16LE, UTF-16BE, windows-1252 (following the Encoding standard, the ISO-8859-1 and US-ASCII labels are read as windows-1252), windows-874 (also `iso-8859-11`, `tis-620`), windows-1250 to windows-1258 (also `cp1251`, `x-cp1251` and the like; `iso-8859-9` and `latin5` are windows-1254), `macintosh`, `shift_jis` (also `sjis`, `ms932`, `windows-31j`), `gbk` (also `gb2312` and its labels), `euc-kr` (also `ks_c_5601-1987`, `windows-949`) and `big5`, each with the labels the Encoding standard gives it. A document that declares any other encoding is read as windows-1252, with the warning `html.charset_unsupported`.
- **`Extractor.Charset`** is the encoding the input comes with from outside it, the transport-layer encoding of the sniffing algorithm, given as a label the way a MIME `Content-Type` `charset` parameter gives it (quotes are allowed): go-fast-email passes the charset of an HTML or text body this way. It applies to HTML, Markdown and plain text, not to the chapters of an EPUB, which declare their own. A byte order mark overrides it; it overrides `<meta>` and detection. A label that names no encoding decoded here is ignored, with the warning `html.charset_unsupported`, and the encoding is decided as if it had not been given.
- **Where the legacy encodings differ from the Encoding standard.** The tables are Microsoft's code pages as the Unicode Consortium publishes them (copied from go-fast-rtf, see `NOTICE`), not the standard's indexes. Big5 is code page 950, then, for the pairs 950 leaves undefined, Big5 with HKSCS as the standard's index has it (`big5-hkscs.txt`, from Python's `big5hkscs` codec; many of its characters are above U+FFFF), and the four pairs 0x8862, 0x8864, 0x88A3 and 0x88A5 decode to a letter and a combining mark as in the standard; where 950 defines a pair it agrees with the standard's index. GBK is code page 936; `gb18030` is not one of its labels and is not decoded, and GB18030's four-byte sequences would not be decoded either. Shift_JIS and EUC-KR are code pages 932 and 949, which agree with the standard on nearly all characters; private-use and user-defined areas the standard maps are U+FFFD here. In the single-byte encodings, a byte from 0x80 to 0x9F the code page leaves undefined is the C1 control of the same value, as in the standard's indexes; any other undefined byte is U+FFFD. Bytes that are not a character are U+FFFD and counted in `text.unmapped_glyphs`; a lead byte followed by an ASCII byte that does not make a character gives U+FFFD and the ASCII byte is read again. Offsets in block locations point into the source bytes in every encoding.
- **Tokenizer** (`internal/lexer`), following the WHATWG tokenization rules for data, RCDATA (`title`, `textarea`), RAWTEXT (`style`, `xmp`, `iframe`, `noembed`, `noframes`), script data with its escaped and double-escaped states, PLAINTEXT, tags and attributes, comments, bogus comments, DOCTYPE, CDATA in SVG and MathML, and character references: the full table of named references (`internal/entity`) with the rule for legacy names without a semicolon, and numeric references with the standard's replacements.
- **Tree** (`internal/dom`), built the way browsers recover from tag soup: implied end tags (`p`, `li`, `dd`, `dt`, `option`, table rows and cells), headings that close headings, misnested formatting elements (a simplified adoption agency algorithm), formatting reopened after a block closes it, content misplaced in a table moved in front of it (foster parenting), SVG and MathML with the HTML elements that break out of them. `noscript` is read with scripting off, as its content.

### Markdown

Markdown (`internal/md`) is parsed with the block and inline structure of CommonMark 0.31: ATX and setext headings, paragraphs, block quotes, bullet and ordered lists with nesting and tight/loose lists, fenced and indented code, thematic breaks, HTML blocks, link reference definitions; emphasis by the delimiter algorithm, code spans, inline and reference links and images, autolinks, raw inline HTML, entity and numeric references, backslash escapes, hard breaks.

**Conformance: all 652 examples of the CommonMark 0.31.2 spec pass**, compared after the normalization of the reference test runner (`normalize.py`). The test (`commonmark_test.go`) reads the spec's JSON from outside the repository (`$GOFASTHTML_COMMONMARK_SPEC`, by default `../.testdata/commonmark-0.31.2-spec.json`; the spec is CC-BY-SA 4.0) and is skipped without it; examples that fail are listed in it with the reason, and the list is empty. The spec is checked with link addresses percent-encoded as the reference renderer encodes them; in extraction a link's address is kept as the source writes it.

GitHub Flavored Markdown extensions read: tables; strikethrough (`~~a~~` or `~a~`, a `strike` span); task-list items (`- [ ]`, `- [x]`: the marker is taken out of the text and the item has `Attrs["checked"]` `"false"` or `"true"`; an HTML list item that begins with a checkbox gets the same); extended autolinks (bare `www.`, `http://` and `https://` addresses, `www.` ones linked as `http://`). Not read: footnotes, extended email autolinks (a bare address such as `user@example.com` stays text; `<user@example.com>` is a CommonMark autolink and is read) and the disallowed-raw-HTML filter (raw HTML is read as a page's is). Front matter is not recognized.

The document is rendered to HTML as a CommonMark renderer does and read by the HTML path above, so the blocks come out as in the table below (emphasis markers are gone; a link is a link span; code is declared code, verbatim, with the fence's language). Raw HTML in Markdown is read exactly as in a page: its declarations give the same display reasons, and an HTML comment is a `note` marked `Attrs["stored"]` `markup_comment`. `Loc.Offset` and `Loc.Length` are into the Markdown source: the lines of the Markdown block a block came from, and for raw HTML the exact bytes of its element.

Encoding: a byte order mark, then `Extractor.Charset`, else UTF-8 if the whole input is valid UTF-8, else windows-1252. Markdown has no `<meta>` prescan.

### Plain text

Plain text is decoded as Markdown is, and split into `paragraph` blocks at blank lines and form feeds. Line breaks inside a paragraph are kept as `\n`: a text file does not say whether its lines are wrapped prose or lines that mean something (an address, a verse, a list), and keeping them loses nothing. Trailing white space on each line and control characters other than tab are dropped. `Loc` gives each paragraph's byte range. Plain text declares nothing about display, so no block has a display reason.

### EPUB

EPUB 2 and 3 publications (`Source.Format` `epub`, `.epub` or a zip whose first member says `application/epub+zip`) are read here rather than in a library of their own, since their content is XHTML and extractors do not import each other.

- **Container.** The zip archive is read with a limit on members (`Extractor.MaxArchiveMembers`, default 100,000; more is `ErrLimitExceeded`) and every member decompressed within `Limits.MaxDecodedBytes`, shared with embedded documents. An archive whose central directory is missing (a file cut short) is read by the members' local headers, with `input.repaired`; a name that occurs twice gives `input.ambiguous` and the first is read. A missing or wrong `mimetype` member gives `epub.mimetype` (info) and the archive is read anyway. `META-INF/container.xml` leads to the package document (OPF); without one the input is `ErrMalformed`. `container.xml`, the package document, the navigation document, the NCX and `encryption.xml` are read up to 16 MiB each (`epub.MaxXML`): a larger `container.xml` or package document is `ErrLimitExceeded`.
- **Metadata.** `dc:title` (the first) to `Title`; each `dc:creator` to `Authors`; `dc:language` to `Language`; each `dc:subject` to `Keywords`; `dc:date` to `Created` when it parses as ISO 8601, and as written to `Extra["date"]`; `meta property="dcterms:modified"` to `Modified`; `dc:description`, `dc:publisher`, `dc:contributor`, `dc:rights`, `dc:source`, `dc:type`, `dc:format`, `dc:relation`, `dc:coverage` to `Extra` under their names; the identifiers to `Extra["identifier"]`, one a line, the package's unique identifier first; the package version to `Extra["epub_version"]`. The text of one metadata element is read up to 64 KiB, up to 4096 Dublin Core elements, and up to 2^20 manifest items and spine entries; past any of these, the document carries `limit.exceeded` with the count.
- **Chapters.** One `chapter` part per spine item, numbered by its place in the spine, read by the HTML path in XHTML mode (a self-closing tag is an empty element; the XML declaration is not a comment). `Loc` offsets are into that chapter's file. `Part.Name` is the chapter's first entry in the table of contents, else its `<title>`. An item with `linear="no"` is extracted in its place and carries `epub.non_linear` (info). Images a chapter shows are resolved in the archive: `Image.ID` is the member's path, `MIME` the manifest's media type, and with `ImagesData` the bytes. References to members of the archive are not counted as external. `Options.Parts` selects chapters; `Limits.MaxParts` (default 100,000) bounds them. When `Limits.MaxParts` or the text budget stops the chapters, the rest of the manifest (below) is not read either, and the document's `limit.exceeded` names what was not read, as the publication has them: the later chapters, the stored part (the scripts, styles and form values of the chapters read), the images of the manifest that no chapter read shows (some may be ones a later chapter would have shown) and the embedded documents.
- **Navigation.** The EPUB 3 navigation document (`nav` with `epub:type="toc"`), else the EPUB 2 NCX, becomes `Outline`: title, nesting level and the number of the chapter it points to (0 when it points outside the spine). Titles count against the text budget before the first chapter. The table of contents is read up to 100,000 entries, lists nested up to 64 levels and titles up to 4 KiB (cut at the same byte however the title's text is split); past any of these, and when the navigation document or NCX is larger than 16 MiB or the decode budget and is not read, the document carries `limit.exceeded`.
- **The rest of the manifest**, with the whole document only (`Options.Parts` unset): images no chapter shows go to the `stored` part (after the chapters' scripts and styles), with no display reason, as `image` blocks with `Attrs["stored"]` `manifest_item` and `Attrs["name"]` the member path (a cover image has the text `cover`); stylesheets are skipped; every other item (documents outside the spine, fonts, audio, scripts) is handed to `Options.ExtractChild` and listed in `Children`. Scripts count as active content.
- **Encryption.** Resources `META-INF/encryption.xml` lists, font obfuscation (IDPF and Adobe) aside, are not read: an encrypted chapter is a part with `part.failed` and `epub.encrypted`, an encrypted image an image block without data (its chapter, or the stored part, carries `epub.encrypted` with the count), an encrypted embedded file a child with `child.encrypted`, and the document carries `epub.encrypted` with the count. When every selected chapter is encrypted (DRM), nothing usable is left and the result is `ErrUnsupported`. An `encryption.xml` that cannot be read in full (larger than 16 MiB or the decode budget, listing more than 2^20 members, damaged, or XML that cannot be read to its end) leaves which members are encrypted unknown, and none is read as text: every member but `mimetype`, `META-INF/` and the package document is treated as possibly encrypted (a chapter gets `part.failed` and `epub.encrypted` saying so, an embedded file `child.encrypted`, and the navigation document and NCX are not read). Since that leaves no chapter to read, the result is `ErrLimitExceeded` (too large, too many members) or `ErrMalformed` (damaged).
- **Display.** Each chapter's declarations give display reasons as in a page. That includes the navigation document when it is in the spine: the `hidden` attribute EPUB 3 puts on its `nav` and `ol` elements (landmarks, page list, deeper levels of the table of contents) is a declaration, and those blocks get `hidden`.

## How HTML maps

One part of kind `body`, numbered 1, and, when the page has script or style text or `<input type=hidden>` values and `Options.Parts` is unset, a part of kind `stored`, numbered 2, that holds them. Every block has `Loc.Offset` and `Loc.Length`, its byte range in the source file (also when the file is UTF-16 or windows-1252), and `Loc.Anchor` from the `id` of its element, or of an element in it (`<a name>` counts).

| HTML | Block |
|---|---|
| `h1`–`h6` | `heading`, level 1–6 |
| `p`, `div` and other block elements, text between blocks | `paragraph` |
| `li` in `ul`, `ol`, `menu`, `dir` | `list_item`, `Level` the list depth, `Ordered`, `Marker`: `3.`, `c.`, `iv.` per `start`, `value`, `type`, `reversed`; a bullet (`•`, `◦`, `▪` by depth, or per `type`) for unordered lists. Block elements inside an item are line breaks in its text; text after a nested list is a paragraph |
| `table` | `table`; `rowspan`/`colspan` as `Merges` (clamped as the standard clamps them; `rowspan=0` runs to the end); leading `thead` rows, or rows of `th` only, as `HeaderRows`; with `Options.Spans`, a cell's first link in `Table.Cells`. A table inside a cell is written into the cell's text, a line a row |
| `caption`, `figcaption`, `legend` | `caption` |
| `pre`, `listing`, `xmp`, `plaintext` | `code`, verbatim; `Attrs["language"]` from a `language-x` or `lang-x` class on it or its `code` |
| `blockquote` | `quote`, `Level` the nesting depth |
| `img` | `image` with the alt text; `Image.ID` the `src` (a `sha256:` hash for a `data:` URI or a very long address), `Width`/`Height` from the attributes; with `ImagesData`, the bytes of `data:` URIs |
| `a href` | a link span, with `Options.Spans` |
| `b`, `strong`, `i`, `em`, `u`, `s`, `del`, `code`, `kbd`, `sub`, `sup`, ... | style spans, with `Options.Spans` |
| `br` | `\n` in the text |
| `hr` (and a Markdown thematic break, `---`, `***`, `___`) | `thematic_break`, no text, with the display reasons of the element; inside a table cell only a line break in the cell's text |
| `select` | `note` of type `choices`, the options a line each, `Attrs["field"]` its name |
| `textarea`, `input` with a value | `paragraph` with `Attrs["field"]` the control's name; the value of `<input type=hidden>` goes to the `stored` part with `Attrs["stored"]` `form_field` |
| `<!-- comment -->` | `note` with `Attrs["stored"]` `markup_comment` and no note type, after the block it stands in; no display reason; left out of rendered text unless `RenderOptions.Stored` or `StoredKinds` asks for it (also in raw HTML in Markdown and in EPUB chapters) |
| `script`, `style` | in the `stored` part as `code` (language `javascript`, `json` or `css`), verbatim, read as data and never run, with `Attrs["stored"]` `macro` (core's `StoredMacro`) for a script and `stylesheet` for a style element; no display reason |
| `template` | read as text where it stands, with the reason `hidden` (its content is inert by the element's definition) |
| `noscript`, `noembed`, `noframes`, `iframe` fallback, closed `details`/`dialog`, `rp` | read as text where they stand; no display reason |
| `iframe srcdoc` | the document in the attribute is parsed as HTML and its blocks come where the iframe stands, before any fallback text; `Loc` is the (outermost) iframe element in the page; what the iframe declares (`hidden`, inline `display:none`, ...) is added to all of it, what the inner document declares applies inside; its `title`, `meta` and `lang` are not the page's metadata and are not read. Its elements count from the iframe's depth against `Extractor.MaxDepth`: a srcdoc document whose iframe is at the limit is not read (`limit.exceeded`). Its bytes count against the decode budget (cut with `limit.exceeded`) and its text against the text budget. |
| `iframe src`, `frame`, `embed`, `object`, `audio`, `video` | the address is not fetched and counts toward `input.external_reference` (a `data:` or `javascript:` address does not); the element's own content, such as an iframe's fallback text, is read where it stands |
| `<title>` | `Metadata.Title` |
| `<meta name=author>` | `Metadata.Authors`, one entry each, not split |
| `<meta name=keywords>` | `Metadata.Keywords`, one entry, not split |
| `<meta name=description>` | `Metadata.Extra["description"]` |
| `<meta name=generator>` | `Metadata.Application` |
| `<html lang>` | `Metadata.Language` |

Outside `pre` (and `white-space: pre` in inline style), white space is collapsed as CSS `white-space: normal` does. Ligatures U+FB00–U+FB06 are written as their letters everywhere but in code.

## What the file declares

Everything is extracted; nothing is dropped. Where the page itself declares that text is not displayed, or is drawn with no ink, the block (or the span, or the table cells) carries that reason in `Display`, following the page's own inheritance as CSS does. Only the element's attributes and its `style` attribute are read: **stylesheets (`<style>` and linked ones) and scripts are not evaluated**.

`hidden` is reported for:

- the `hidden` attribute, including `hidden="until-found"` (unless inline style gives another `display`);
- inline `display: none`;
- inline `visibility: hidden` or `collapse`, inherited, and undone by `visibility: visible` inside;
- the SVG presentation attribute `display="none"`;
- the SVG presentation attribute `visibility="hidden"` (or `collapse`), undone by `visible` inside;
- the content of `template`, which is inert by the element's definition.

`no_ink` is reported for:

- inline `opacity: 0` exactly;
- inline `color: transparent`, or a colour whose alpha is exactly 0 (undone by another colour inside).

The same rules apply to raw HTML in Markdown and to every EPUB chapter.

**The boundary.** Colour, size, position and overlap are not judged. Nothing compares two values or applies a threshold: a text colour equal to its background, a font size of 0 or 1 pixel, a large negative offset, clipping, `opacity: 0.01` or a colour with alpha 0.01 give no reason. A rule in a stylesheet that hides a class gives no reason either, since stylesheets are not evaluated. A block with no `Display` value says nothing about whether a reader can see it.

**Not reported, no reason:** `noscript` (shown under a condition), closed `details` and `dialog` (shown on interaction), `rp`, and `aria-hidden` (which concerns assistive technology; it neither adds a reason nor removes one).

**Kept where they are, with no reason:** comments and attribute text are `note` blocks; a comment is stored data (`Attrs["stored"]` `markup_comment`), as are script and style text and `<input type=hidden>` values in the `stored` part, all left out by the renderers unless `RenderOptions.Stored` is set or `RenderOptions.StoredKinds` names their kind. A comment or attribute inside a hidden element still has no reason.

**Characters are kept.** Zero-width characters, Unicode tag characters, bidirectional controls and variation selectors stay in the text as written, with no reason and no warning.

**Attribute text.** Text a page keeps in attributes without drawing it in the flow of the page is extracted: `aria-label`, `aria-description`, `aria-roledescription`, `aria-placeholder`, `title` and `placeholder` on any element, and `alt` on an image map's `area`. Each becomes a `note` block with `Attrs` `type: attribute` and `attribute: <name>`, at the element's place (before the block the element is in, when that block has no text yet), with `Loc` the byte range of the attribute's value. A value that only repeats the element's own text, or an image's alt text, after white space is collapsed, is left out, since that text is extracted already. `aria-labelledby` and `aria-describedby` are not followed: the elements they name are read where they stand, with the reasons their own declarations give.

## Warnings

| Code | Severity | When |
|---|---|---|
| `input.active_content` | info | scripts, `on...` event attributes, `javascript:` addresses |
| `input.external_reference` | warning | resources loaded from elsewhere: pictures, scripts, stylesheets, frames, embedded objects, media, a refresh to another address, a form's target; nothing is fetched |
| `text.unmapped_glyphs` | warning | bytes not valid in the encoding read, shown as U+FFFD |
| `image.unsupported_encoding` | warning | with `ImagesData`, a `data:` image that could not be decoded, or an EPUB image that could not be read (not counting images left without data by the decode budget or encryption, which have their own warnings: `limit.exceeded`, `epub.encrypted`) |
| `part.no_text`, `part.image_only` | warning | the body or an EPUB chapter has no text (`part.no_text` also when an EPUB has no chapters) |
| `limit.exceeded` | error | a limit cut off the body, an EPUB chapter (saying so too when the chapter's name was left out), the table of contents or the stored part; the text budget left out an EPUB chapter's name; the text budget stopped the reading of an EPUB's chapters (on the document, naming the chapter and what was not read) or of the rest of its manifest (on the document, with the number of images and embedded documents not read); an EPUB has more chapters than `Limits.MaxParts` (on the document, naming what was not read); the decode budget left `data:` images undecoded, without ID or data (on their part, in every images mode), EPUB images without data (on their chapter or the stored part) or embedded documents unread (on the document), with the count; an EPUB's table of contents passed a bound of its own (above) or its navigation document or NCX was not read for its size; an EPUB's `encryption.xml` was too large to read in a document that is returned; an `iframe srcdoc` document is at the depth limit; a bound of the tree builder lost spans or a declared display: more than 64 formatting elements open at once, the bound on formatting elements reopened, a `style` attribute longer than 16 KiB (one warning each per part); a bound cut a string of the output (each once, with a count): a metadata value over 64 KiB, more than 256 `author` or `keywords` declarations, an `<html lang>` over 64 bytes (left out), on the document; an `id` (or an `a`'s `name`) over 1,024 bytes not used as `Loc.Anchor`, a code block's language over 64 bytes (left out), on the part; an EPUB chapter's `<title>` over 64 KiB cut as its name, on the chapter; an EPUB's package metadata text over 64 KiB, Dublin Core elements past 4,096, manifest items or spine entries past 2^20, on the document |
| `html.charset_unsupported` | warning | a declared encoding that is not decoded, read as windows-1252; or an `Extractor.Charset` label that is not decoded, ignored. In an EPUB, one warning for the document with the number of chapters and the labels (the first 8, with each one's count) |
| `html.nesting_flattened` | warning | elements nested deeper than `Extractor.MaxDepth` (default 512), or more than 4096 open at once; below that their text is read side by side, with their display reasons kept |
| `input.repaired`, `input.ambiguous` | warning | EPUB: the archive's directory was unreadable, or a member name occurs twice |
| `epub.non_linear` | info | the chapter is `linear="no"` in the spine |
| `epub.encrypted` | error | resources are encrypted (DRM) and were not read; or `encryption.xml` could not be read in full and a chapter or image may be encrypted; on a chapter or the stored part, the count of encrypted images left without data |
| `epub.mimetype` | info | the `mimetype` member is missing or wrong |
| `part.failed` | error | EPUB: a chapter could not be read (missing from the manifest or archive, encrypted) |
| `child.encrypted` | error | EPUB, on a child: an embedded document `META-INF/encryption.xml` lists; it was not opened |
| `child.not_extracted` | info, warning | EPUB, on a child: not extracted, since `Options.Child` is not set (info), or the depth limit was reached or its bytes could not be read (warning); set by core's `Options.ExtractChild` |
| `child.unsupported` | warning | EPUB, on a child: `Options.Child` returned `ErrUnsupported` (the command's does for formats other than HTML, Markdown, text and EPUB) |
| `child.failed` | error | EPUB, on a child: `Options.Child` returned any other error |

## Limits

| Limit | Default | |
|---|---|---|
| `Limits.MaxInputBytes` | 256 MiB | the file is held in memory; larger is `ErrLimitExceeded` |
| `Limits.MaxDecodedBytes` | 1 GiB | the text decoded to UTF-8, the HTML a Markdown document is rendered to, and the bytes of `data:` images |
| `Limits.MaxTextBytes` | 256 MiB | core's text budget: block text, cells, markers, link targets, image IDs, `BlockCost` a block and `CellCost` a cell of a table (so a small table declaring a huge grid is cut by rows) |
| `Extractor.MaxDepth` | 512 | tree depth |

Every fixed bound that cuts a string of the output (text, names, metadata values, anchors, languages) reports it with `limit.exceeded`; bounds that lose nothing are not reported: an image address over 2,048 bytes is replaced by `sha256:` and its hash as `Image.ID` (the address is in the source at `Loc`), a `width` or `height` outside 1 to 2^24 - 1 is an invalid value and is not given, a number attribute (`start`, `value`, `colspan`, `rowspan`) of more than 18 digits is an invalid integer and is ignored (shorter ones are read in full; `colspan` and `rowspan` are clamped as the HTML standard says), a declared encoding label is read up to 64 bytes (only to look it up and to quote it in `html.charset_unsupported`), and link labels and ordered-list numbers in Markdown are bounded as CommonMark says. Block quotes and lists in Markdown nest at most 100 deep (`md.MaxNest`); a marker past that is text, kept. Scans in Markdown that find no end (code spans, comments) are not repeated. Every tag costs bounded work: end tags look at most 512 elements down the stack, at most 64 formatting elements are kept to reopen and the number reopened is bounded, and inline styles are read up to 16 KiB; when a page reaches one of the last three, its part carries `limit.exceeded`. Extraction stops when the context is cancelled.

## Not read

Stylesheets and scripts (not evaluated); `aria-labelledby`/`aria-describedby` references (not resolved); `srcset`; the `title` and `meta` of an `iframe srcdoc` document; CSS generated content; `<base href>` (addresses are as written); layout tables are tables. In Markdown: GFM footnotes, extended email autolinks, the disallowed-raw-HTML filter and front matter, as above.

## Licence

MIT. The named character reference table is from the WHATWG HTML Living Standard; see `NOTICE`.
