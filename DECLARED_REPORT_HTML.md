# Declared, not judged: go-fast-html

Meant for the workspace root (`/home/andrew/Documents/go-fast-libraries/DECLARED_REPORT_HTML.md`); written into the repository because this run could not write outside it.

## 1. Branch and core

Branch `declared-not-judged` (from `f5ec78f`, on `review-1`), local only, not pushed. Pins go-fast-core **`05bbddb`** (`v0.0.0-20261120081927-05bbddb16b9f`), schema 0.2. Commits: `2c55b5d` (the change, tests, command), `36f7c5f` (comments), `025e812` (README, plan, CHANGELOG), and this report.

## 2. Declarations reported (as in the README)

`hidden`:
- the `hidden` attribute, including `hidden="until-found"` (unless inline style gives another `display`);
- inline `display: none`;
- inline `visibility: hidden` or `collapse`, inherited, and undone by `visibility: visible` inside;
- the SVG presentation attribute `display="none"`;
- the SVG presentation attribute `visibility="hidden"` (or `collapse`), undone by `visible` inside;
- the content of `template` (inert by the element's definition). **To confirm, Andrew.**

`no_ink`:
- inline `opacity: 0` exactly;
- inline `color: transparent`, or a colour whose alpha is exactly 0 (undone by another colour inside).

Same rules for raw HTML in Markdown and every EPUB chapter, including the `hidden` attribute on the navigation document's `nav` and `ol` (exemption from `4ad11ce` removed). No reason: `noscript`, closed `details`/`dialog`, `rp`, `aria-hidden`; comments and attribute text (notes); script, style and `<input type=hidden>` values (stored part).

## 3. Removed

- Checks: `tiny` (font sizes), `low_contrast` (colour comparison against inline backgrounds and white, `<font color>`/`bgcolor`), `off_page` (negative offsets, text-indent), `clipped` (clip, clip-path, 1-pixel boxes), with their inline-style parsing. `internal/css` keeps only display, visibility, opacity, the alpha of `color`, white-space.
- The outlier rule for comments, attribute text and form values, with its single-token form-value exemption.
- `Document.HiddenChecked` and its "no stylesheet" rule; the metadata invisible-character scan.
- Warnings `text.hidden`, `text.unshown_outlier`. The command no longer lists `text.hidden` on stderr.
- Command: `-exclude-hidden` replaced by `-exclude-display[=hidden,no_ink]`.
- Size: non-test Go about 700 lines deleted, 290 added; tests about 230 deleted, 270 added.

## 4. Corpus, before (`f5ec78f`) and after

| Set | Before: blocks / files | After: blocks / files |
|---|---|---|
| dev html (35) | invisible=78 / 9 | hidden=96 / 6 |
| dev md (8) | 0 | 0 |
| dev epub (15) | 0 | 0 |
| heldout html (13) | invisible=664 / 5 | hidden=625 / 4 |
| heldout md (6) | 0 | 0 |
| heldout epub (10) | 0 | hidden=263 / 3 |

No errors, Validate failures, panics or timeouts, before or after. Warnings `text.hidden` (9 dev, 5 held-out) and `text.unshown_outlier` (4 dev, 2 held-out) are gone; others unchanged. Dev html: 4 files lose their only reason (long comments the outlier rule marked); `mdn-html-element.html` gains 22 blocks from its 23 `<template>` elements; the menus and banners of the other pages keep `hidden`. Held-out epub: presumably navigation documents in the spine (exemption removed); not inspected. The old code had no inferred reason (`tiny` etc.) on any corpus file.

## 5. Benchmark (`BenchmarkExtract`, ns/op, 3 runs)

| File | Before | After |
|---|---|---|
| html | 1.55 to 1.67 M | 1.50 to 1.69 M |
| md | 175 to 179 k | 174 to 180 k |
| epub | 11.8 to 11.9 M | 10.8 to 10.9 M |

Allocations unchanged (html 16,309, md 2,876, epub 63,662 per op). The library has no instruction counter, so these are wall-clock figures.

## 6. R6

`TestCharactersKept` (HTML, Markdown, plain text) and `TestEPUBCharactersKept` (an EPUB chapter): tag characters, U+202E/U+202C, a run of zero-width characters and a variation selector come through unchanged, with no reason and no warning.

## 7. For Andrew

- **`template` gets `hidden`**, as the handoff recommends. Confirm.
- **SVG `visibility` in the `collapse` form** is treated like `hidden`, as CSS does.
- **`opacity: 0` is not undone inside** (CSS opacity composes), and `color` alpha 0 is undone by a declared colour inside (colour inherits). Both follow CSS.
- **The command flag is renamed** `-exclude-display` with no alias for `-exclude-hidden`. An alias could be kept if scripts use the old one.
- **The `select` choices note** keeps its element's reason (the options are displayed content, not a comment or attribute).
- The "before" numbers were taken by checking out `f5ec78f` in the repository itself; a worktree in `.worktrees/html-before` could not reach the corpus because environment variables and symlinks were refused. The worktree was created and removed.
