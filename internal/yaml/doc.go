// Package yaml is a streaming, event-based parser of YAML 1.2.2 that gives
// every node its place in the input.
//
// A Parser reads a []byte of UTF-8 (a byte order mark at the start or
// before a document is skipped) and returns events one at a time, as
// libyaml does: stream, document, mapping and sequence starts and ends,
// scalars and aliases, with anchors, tags (resolved to their full form with
// the document's %TAG directives and the defaults "!" and "!!") and scalar
// styles. Comments produce no events. It passes every case of the YAML test
// suite (data release 2022-01-17).
//
// # Offsets
//
// Start and End are byte offsets into the input, half-open:
//
//   - Scalar: its own text. A plain scalar from its first to its last
//     character (a multi-line one spans its lines); a quoted one with both
//     quotes; a block scalar from its indicator ("|" or ">") to the end of
//     its last content line, before that line's line break. An empty node
//     (an implicit null) has Start == End where it would stand.
//   - PropStart is the offset of a node's first property (anchor or tag),
//     or Start when it has none.
//   - MappingStart, SequenceStart: Start == End. In block style the first
//     character of the collection (its first key, with that key's
//     properties, or the "?", ":" or "-" that opens it); in flow style the
//     "{" or "[". An implicit single-pair mapping in a flow sequence starts
//     at its key.
//   - MappingEnd, SequenceEnd: Start == End == the end of the collection: in
//     block style the end of its last value, in flow style just after the
//     "}" or "]". A collection's span is its start event's Start to its end
//     event's End; children's spans lie within it.
//   - DocumentStart: the first directive, or the "---", up to the end of the
//     "---"; without either, Start == End at the first character of the
//     content. DocumentEnd: the "..." when there is one, else Start == End
//     == the end of the content.
//   - Alias: the "*name".
//
// # Bounds
//
// Parsing is linear in the input: no line is read twice except the white
// space after a scalar, implicit keys are bounded as the spec bounds them
// (one line, 1,024 characters; inside a flow mapping, where the spec lets a
// key span lines, 64 KiB), and the scanner reserves token slots where a key
// may begin so that resolving one never shifts the token queue. Limits
// bounds the nesting of collections (flow and block together); passing it
// is a *SyntaxError that matches ErrDepth. The parser holds no recursion:
// its stack is a slice bounded by the nesting limit.
//
// # What it does not do
//
// It does not resolve plain scalars to types (no schema: "true", "1" and
// "null" are strings with their style; deciding is the caller's), does not
// expand aliases (an alias is reported, never followed, so a caller that
// expands must count what it builds), does not build documents and does not
// emit YAML. Input is read as data only; nothing in it is run or followed.
package yaml
