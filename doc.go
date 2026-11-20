// Package gofasthtml extracts HTML, Markdown, plain text and EPUB into the
// shared go-fast document model, gofastcore.Document. An EPUB is a part
// for each chapter of its spine, read as XHTML. Markdown is parsed as
// CommonMark (with GFM tables), rendered to HTML and read as a page is, so
// its raw HTML gets the same checks; blocks are located in the Markdown.
//
// It uses the standard library and go-fast-core only, with an HTML5
// tokenizer and tree builder of its own that recover from tag soup the way
// browsers do. A document is one part of kind body. Blocks carry their
// byte range in the source (Loc.Offset, Loc.Length) and the id of their
// element (Loc.Anchor).
//
// Everything is extracted. Where the page declares, in an attribute or in
// inline style, that text is not displayed or has no ink, the blocks carry
// that reason (hidden, no_ink); nothing else about display is judged, and
// stylesheets and scripts are not evaluated. Script and style text is read
// as data into the stored part and never run. Nothing the page refers to
// is fetched. See the README for the mapping, the warnings and the limits.
package gofasthtml
