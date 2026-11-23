# Benchmarks against the tools in use today

go-fast-html is measured against the HTML text extractors data and AI pipelines use: from Python, BeautifulSoup with lxml, html2text, inscriptis, resiliparse, selectolax, markitdown and trafilatura; from Rust, the `html2text` and `htmd` crates. The tables are in [`comparison.md`](comparison.md); this file says what they show and how to make them again.

## What the comparison shows

On 9,835 real pages from Common Crawl (1.45 GiB):

- **Speed.** With one worker, go-fast-html reads them in 10.3 s.
  - The two parsers written in C and C++ on the lexbor engine are faster: selectolax (3.7 s) and resiliparse (7.2 s).
  - Every other tool is slower: the Rust `html2text` crate 1.4 times, inscriptis 3 times, BeautifulSoup 7 times, trafilatura 14 times, markitdown 23 times.
  - With 24 workers the order is the same, except that the Rust `html2text` crate (1.3 s) passes go-fast-html (2.1 s).
  - go-fast-html does more per page than the lexbor tools: block structure, tables, links, declared display, locations, metadata, and scripts and comments kept as stored data. Even so, it is behind them, and closing that gap is open work (below).
- **The slowest page.** go-fast-html's slowest page took 33 ms with one worker. Every other tool had a page between 99 ms and 2 s. In a batch, this is what decides whether a worker stalls.
- **Memory.** With one worker go-fast-html peaks at 53 MB, the least but for htmd (49 MB). With 24 workers it uses 264 MB, against 0.4 to 4.5 GB for the others.
- **Accuracy.** Measured against the text Chromium shows:
  - Rendered as a browser does, go-fast-html has the highest F1 of all tools: 0.964, against inscriptis 0.956 and selectolax 0.929.
  - Its `Text()` scores lower (0.873) only because it keeps what a browser does not draw: HTML comments, `title` and `aria-label` text, text the page declares hidden, and images' alt text. That is the series' rule "preserve content", and each of these carries its reason in the output. Its recall, 0.992, is the highest.
  - "What a browser shows" is `bench/gohtml` with `GOHTML_VISIBLE=1`: the body's blocks without stored data, declared-hidden blocks, attribute notes or images.
  - The Markdown converters (markitdown, html2text, htmd) score low on precision because they write every link's address into the text.
  - trafilatura extracts only the main content, by design, so its recall is low. It is listed for its speed, not as a like-for-like comparison.
- **Failures.** go-fast-html refused 10 pages with `ErrEncodingUnknown`: no declaration it reads, not UTF-8, no encoding certain. The other tools read every page, in an encoding they guessed. Given each page's HTTP `charset` as `Extractor.Charset`, as a crawler passes it on, 7 are refused:
  - 2 declare their encoding in a `<meta>` past the first 1,024 bytes the prescan reads (windows-1250, and EUC-JP, which go-fast-html does not decode). The HTML standard has a parser that meets such a `<meta>` switch to it ("change the encoding"); go-fast-html does not yet.
  - 2 are declared by HTTP in encodings go-fast-html does not decode: KOI8-R and ISO-8859-2.
  - 3 declare nothing, and no encoding is certain from their bytes.

## Open work this found

1. **Speed against lexbor.** go-fast-html is 2.8 times slower than selectolax. On one core the time is spread over the tokenizer (31%), the walk that makes blocks (32%) and the text buffer (15%), with allocation throughout: no single hot spot.
2. **A `<meta charset>` past the first 1,024 bytes**, as the HTML standard's "change the encoding" has it.
3. **More encodings:** EUC-JP, ISO-2022-JP, GB18030, KOI8-R, KOI8-U and ISO-8859-2 to 16. go-fast-email already has tables for the first three.

## Reproducing

The Python tools are pinned in `requirements-text.txt` and live in their own environment, apart from the PDF and Office tools:

```sh
cd ../../bench-env
uv venv --python 3.13 .venv-text
uv pip install --python .venv-text/bin/python -r ../go-fast-html/bench/requirements-text.txt
.venv-text/bin/python -m playwright install chromium
```

Then, with `DATA` a directory outside the repository:

```sh
# The pages: the first 400 MB of one WARC file of the crawl.
curl -o warc.paths.gz https://data.commoncrawl.org/crawl-data/CC-MAIN-2026-43/warc.paths.gz
curl -r 0-419430399 -o $DATA/sample.warc.gz "https://data.commoncrawl.org/$(zcat warc.paths.gz | sed -n 1000p)"
python -I bench/warc_to_pages.py $DATA/sample.warc.gz $DATA          # pages/ and manifest.tsv
ls $DATA/pages/*.html > $DATA/all.list

# The tools: build the Go and Rust harnesses, then run everything.
(cd bench/gohtml && go build -o gohtml .)
(cd bench/rshtml && CARGO_TARGET_DIR=../../../bench-env/rust-target cargo build --release)
bench/run_all_html.sh $DATA/all.list $DATA/results > $DATA/run.log   # about 15 minutes
GOHTML_VISIBLE=1 bench/gohtml/gohtml $DATA/all.list $DATA/results/go-fast-html-visible 24
GOHTML_EXCLUDE_HIDDEN=1 bench/gohtml/gohtml $DATA/all.list $DATA/results/go-fast-html-nohidden 24

# Accuracy, and the report.
python -I bench/reference_html.py $DATA/all.list $DATA/reference 500 1
python -I bench/score_text.py $DATA/reference $DATA/results/*-1 $DATA/results/go-fast-html-{visible,nohidden} > $DATA/scores.json
python -I bench/report.py $DATA > bench/comparison.md
```

`run.log` here is the log of the run in `comparison.md`. `bench/gohtml` and `bench/rshtml` are modules of their own, so they are not part of the library's build.
