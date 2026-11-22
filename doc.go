// Package gofasthtml extracts HTML, Markdown, plain text, EPUB, comma-
// and tab-separated values and Jupyter notebooks into the shared go-fast
// document model, gofastcore.Document. An EPUB is a part for each chapter
// of its spine, read as XHTML. A CSV or TSV file is one part of kind sheet
// whose rows are table blocks. A notebook is one part of kind body: its
// Markdown cells read as Markdown is, its code cells as code blocks
// followed by the blocks of their outputs. Markdown is parsed as
// CommonMark with the GFM tables, strikethrough, task lists and extended
// autolinks, rendered to HTML and read as a page is, so its raw HTML gets
// the same checks; blocks are located in the Markdown.
//
// It uses the standard library and go-fast-core only, with an HTML5
// tokenizer and tree builder of its own that recover from tag soup the way
// browsers do. An HTML page, a Markdown document or a plain text file is
// one part of kind body; an EPUB is one part of kind chapter for each
// chapter. Script and style text, hidden form values and, in an EPUB,
// images no chapter shows go to a part of kind stored after them when the
// whole document is read. Blocks carry their byte range in the source
// (Loc.Offset, Loc.Length) and the id of their element (Loc.Anchor).
//
// Everything is extracted. Where the page declares, in an attribute or in
// inline style, that text is not displayed or has no ink, the blocks carry
// that reason (hidden, no_ink); nothing else about display is judged, and
// stylesheets and scripts are not evaluated. Script and style text is read
// as data into the stored part and never run. Nothing the page refers to
// is fetched. See the README for the mapping, the warnings and the limits.
package gofasthtml
