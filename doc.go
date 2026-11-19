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
// Text a reader of the page cannot see is extracted like any other and
// flagged with the reason, as far as the page's attributes and inline
// style say: stylesheets and scripts are not evaluated. Script and style
// text is read as data, flagged, and never run. Nothing the page refers to
// is fetched. See the README for the mapping, the warnings and the limits.
package gofasthtml
