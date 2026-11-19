# go-fast-html implementation plan

Module `github.com/AOShei/go-fast-html`, package `gofasthtml`. Extracts
HTML, Markdown and plain text into the go-fast-core model (one `body` part;
`Loc.Offset`/`Length` are byte offsets into the source; `Loc.Anchor` from
`id`). go-fast-core pinned at commit 031432a
(`v0.0.0-20261119191115-031432aa57b0`).

## Layout

| Path | What |
|---|---|
| `extract.go`, `doc.go` | Public API: `Extractor`, `Extract`, `ExtractFile`, warning codes, defaults |
| `internal/charset` | Encoding sniffing (BOM, `<meta>` prescan, UTF-8 validity, windows-1252) and decoding to UTF-8 with a map from decoded offsets back to source offsets |
| `internal/entity` | WHATWG named character references (`entities.txt`, embedded) and the numeric-reference rules; `gen.go` regenerates the table from `entities.json` |
| `internal/lexer` | HTML5 tokenizer (WHATWG section 13.2.5): data, RCDATA, RAWTEXT, script data (with escaped states), PLAINTEXT, tags, attributes, comments, bogus comments, DOCTYPE, CDATA in foreign content, character references |
| `internal/dom` | Tree builder for tag soup: implied end tags, misnested formatting (simplified adoption agency), active formatting reconstruction, foster parenting, foreign content breakout, a nesting depth limit; per-node effective hidden state from inline style |
| `internal/css` | Inline `style` declarations that matter: display, visibility, font-size, opacity, color/background, white-space, position offsets |
| `internal/blocks` | Walk the tree and emit core blocks, with whitespace collapsing, spans, hidden ranges, tables, lists, budget |
| `cmd/gofasthtml` | CLI printing JSON (run 2) |

## Phases

1. **HTML (run 1)**: charset, entities, tokenizer, tree builder, mapping,
   public API, tests, fuzz targets, README. Status: in progress.
2. **Markdown, plain text, CLI (run 2)**: CommonMark parser to the same
   blocks, plain text (paragraphs by blank lines), `cmd/gofasthtml`, and
   anything left from phase 1.

## Status

- [ ] Repository setup, plan
- [ ] internal/charset
- [ ] internal/entity
- [ ] internal/lexer
- [ ] internal/dom
- [ ] internal/css
- [ ] internal/blocks + public API
- [ ] Tests (fixtures, Validate, limits, hidden), fuzz targets
- [ ] README, NOTICE
- [ ] Markdown (run 2)
- [ ] Plain text (run 2)
- [ ] CLI (run 2)
