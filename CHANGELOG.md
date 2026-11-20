# Changelog

## Unreleased: declared, not judged (breaking)

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
- `template` content as `hidden` is decided (Andrew, 2026-11-20).
- Encodings: windows-874, windows-1250 to windows-1258, macintosh, Shift_JIS (code page 932), GBK (936), EUC-KR (949) and Big5 (950) are decoded, with their WHATWG labels, from tables copied from go-fast-rtf (b756076). They were read as windows-1252 with `html.charset_unsupported` before. Big5 has no HKSCS extensions; `gb18030` is still not decoded.
- Big5 now includes HKSCS: pairs code page 950 leaves undefined are read from `big5-hkscs.txt` (generated from Python's `big5hkscs` codec), including characters above U+FFFF, and 0x8862, 0x8864, 0x88A3, 0x88A5 decode to a letter and a combining mark. They were U+FFFD before.
- `Extractor.Charset` (command: `-charset`): the encoding the input comes with, as a MIME charset label; after a byte order mark, before `<meta>` and detection. A label not decoded is ignored with `html.charset_unsupported`.
