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
- `template` content as `hidden` is decided (Andrew, 2026-11-20).
