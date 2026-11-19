# go-fast-html

Extracts HTML into the shared go-fast document model, `gofastcore.Document` (`github.com/AOShei/go-fast-core`). Markdown and plain text will follow. Standard library and go-fast-core only.

```go
import "github.com/AOShei/go-fast-html" // package gofasthtml

doc, err := gofasthtml.ExtractFile(ctx, "page.html", nil)
if err != nil { ... }
fmt.Print(doc.Markdown())
```

**Status:** HTML is read; Markdown, plain text and the command `cmd/gofasthtml` are not written yet. See `IMPLEMENTATION_PLAN.md`.

## What is read

The library has its own HTML5 parser under `internal/`, since the standard library has none:

- **Encoding** (`internal/charset`), as the WHATWG encoding sniffing decides it: a byte order mark, then a `<meta charset>` or `<meta http-equiv="Content-Type">` found by the prescan of the first 1024 bytes, then UTF-8 if the whole input is valid UTF-8, otherwise windows-1252. UTF-8, UTF-16LE, UTF-16BE and windows-1252 are decoded; following the Encoding standard, the ISO-8859-1 and US-ASCII labels are read as windows-1252. A document that declares any other encoding is read as windows-1252, with the warning `html.charset_unsupported`.
- **Tokenizer** (`internal/lexer`), following the WHATWG tokenization rules for data, RCDATA (`title`, `textarea`), RAWTEXT (`style`, `xmp`, `iframe`, `noembed`, `noframes`), script data with its escaped and double-escaped states, PLAINTEXT, tags and attributes, comments, bogus comments, DOCTYPE, CDATA in SVG and MathML, and character references: the full table of named references (`internal/entity`) with the rule for legacy names without a semicolon, and numeric references with the standard's replacements.
- **Tree** (`internal/dom`), built the way browsers recover from tag soup: implied end tags (`p`, `li`, `dd`, `dt`, `option`, table rows and cells), headings that close headings, misnested formatting elements (a simplified adoption agency algorithm), formatting reopened after a block closes it, content misplaced in a table moved in front of it (foster parenting), SVG and MathML with the HTML elements that break out of them. `noscript` is read with scripting off, as its content.

## How it maps

One part of kind `body`, numbered 1. Every block has `Loc.Offset` and `Loc.Length`, its byte range in the source file (also when the file is UTF-16 or windows-1252), and `Loc.Anchor` from the `id` of its element, or of an element in it (`<a name>` counts).

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
| `hr` | ends the block; core has no break block |
| `select` | `note` of type `choices`, the options a line each, `Attrs["field"]` its name |
| `textarea`, `input` with a value | `paragraph` with `Attrs["field"]` the control's name; a hidden input is flagged `invisible` |
| `<!-- comment -->` | `note` of type `comment`, flagged `invisible`, after the block it stands in |
| `script`, `style` | `code` (language `javascript`, `json` or `css`), flagged `invisible`: read as data, never run |
| `<title>` | `Metadata.Title` |
| `<meta name=author>` | `Metadata.Authors`, one entry each, not split |
| `<meta name=keywords>` | `Metadata.Keywords`, one entry, not split |
| `<meta name=description>` | `Metadata.Extra["description"]` |
| `<meta name=generator>` | `Metadata.Application` |
| `<html lang>` | `Metadata.Language` |

Outside `pre` (and `white-space: pre` in inline style), white space is collapsed as CSS `white-space: normal` does. Ligatures U+FB00–U+FB06 are written as their letters everywhere but in code.

## Hidden text

Hidden text is extracted and flagged, never dropped. Only the `style` attribute and the document's own attributes are read: **stylesheets (`<style>` and linked ones) and scripts are not evaluated**, so text a stylesheet or a script hides is not flagged.

| Reason | When |
|---|---|
| `invisible` | the `hidden` attribute (unless inline style gives another `display`); `display: none`; `visibility: hidden` or `collapse` (undone by `visible` inside); `template`, `noscript`, `noembed`, `noframes`, `iframe` fallback text; `script` and `style` text; `aria-hidden="true"`; a `details` without `open`, but for its `summary`; a `dialog` without `open`; `<input type=hidden>`; `rp`; comments |
| `tiny` | an inline `font-size` (or `font`) under 2 points, relative sizes multiplied through |
| `transparent` | `opacity` of 0.01 or less; `color: transparent` or a color with alpha 0 |
| `low_contrast` | an inline or `<font color>` text color against the nearest inline `background`/`bgcolor`, white if none, by core's `LowContrast` |
| `off_page` | `left`, `top`, `margin-left`, `margin-top` or `text-indent` of -999 pixels or less |
| `clipped` | `clip: rect(...)` that shows nothing, `clip-path: inset(50%)`, or a width or height of 1 pixel or less with overflow hidden |
| `invisible_chars` | core's scan |

`Document.HiddenChecked` lists `invisible_chars`, and `invisible` too when the document has no stylesheet and no script: then inline style and attributes are all that decides what is displayed. The other reasons are never listed: their checks are partial (sizes from stylesheets, nested `small`, background images, positioning by other means).

## Warnings

| Code | Severity | When |
|---|---|---|
| `input.active_content` | info | scripts, `on...` event attributes, `javascript:` addresses |
| `input.external_reference` | warning | resources loaded from elsewhere: pictures, scripts, stylesheets, frames, embedded objects, media, a refresh to another address, a form's target; nothing is fetched |
| `text.hidden` | warning | hidden text; the blocks say why. On the document: invisible characters in metadata |
| `text.unmapped_glyphs` | warning | bytes not valid in the encoding read, shown as U+FFFD |
| `image.unsupported_encoding` | warning | with `ImagesData`, a `data:` image that could not be decoded |
| `part.no_text`, `part.image_only` | warning | the body has no text |
| `limit.exceeded` | error | a limit cut the body off |
| `html.charset_unsupported` | warning | a declared encoding that is not decoded; read as windows-1252 |
| `html.nesting_flattened` | warning | elements nested deeper than `Extractor.MaxDepth` (default 512), or more than 4096 open at once; below that their text is read side by side, with the reasons it is hidden kept |
| `html.metadata_truncated` | info | a metadata value over 64 KiB, or more than 256 authors or keyword declarations |

## Limits

| Limit | Default | |
|---|---|---|
| `Limits.MaxInputBytes` | 256 MiB | the file is held in memory; larger is `ErrLimitExceeded` |
| `Limits.MaxDecodedBytes` | 1 GiB | the text decoded to UTF-8 and the bytes of `data:` images |
| `Limits.MaxTextBytes` | 256 MiB | core's text budget: block text, cells, markers, link targets, image IDs, `BlockCost` a block and `CellCost` a cell of a table (so a small table declaring a huge grid is cut by rows) |
| `Extractor.MaxDepth` | 512 | tree depth |

Every tag costs bounded work: end tags look at most 512 elements down the stack, formatting elements reopened are bounded, and inline styles are read up to 16 KiB. Extraction stops when the context is cancelled.

## Not read

Stylesheets and scripts (not evaluated); `title` attributes (tooltips); `placeholder`; `alt` of `area`; `srcset`; `iframe srcdoc` (not extracted as an embedded document); CSS generated content; `<base href>` (addresses are as written); layout tables are tables. Markdown and plain text are not read yet.

## Licence

MIT. The named character reference table is from the WHATWG HTML Living Standard; see `NOTICE`.
