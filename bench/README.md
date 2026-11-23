# Benchmarks against the tools in use today

go-fast-html is measured against the HTML text extractors data and AI pipelines use: from Python, BeautifulSoup with lxml, html2text, inscriptis, resiliparse, selectolax, markitdown and trafilatura; from Rust, the `html2text` and `htmd` crates. The tables are in [`comparison.md`](comparison.md); this file says what they show and how to make them again.

## What the comparison shows

On 9,835 real pages from Common Crawl (1.45 GiB):

- **Speed.** With one worker, go-fast-html reads them in 5.4 s.
  - selectolax, a parser written in C on the lexbor engine, is faster (3.8 s).
  - Every other tool is slower: resiliparse (lexbor, C++) 1.4 times, the Rust `html2text` crate 2.8 times, inscriptis 6 times, BeautifulSoup 13 times, trafilatura 27 times, markitdown 43 times.
  - With 24 workers go-fast-html takes 0.80 s: selectolax is faster (0.48 s), resiliparse about level (0.84 s), and every other tool slower, the Rust `html2text` crate 1.7 times.
  - go-fast-html does more per page than selectolax: block structure, tables, links, declared display, locations, metadata, and scripts and comments kept as stored data.
- **The slowest page.** go-fast-html's slowest page took 19 ms with one worker. Every other tool had a page between 89 ms and 2 s. In a batch, this is what decides whether a worker stalls.
- **Memory.** With one worker go-fast-html peaks at 62 MB, with selectolax (64 MB) and htmd (50 MB) the least. With 24 workers it uses 320 MB, against 0.4 to 4.5 GB for the others. Go's garbage collector can trade memory for speed (`GOGC`, see the library's README); the tables use Go's default.
- **Accuracy.** Measured against the text Chromium shows:
  - Rendered as a browser does, go-fast-html has the highest F1 of all tools: 0.966, against inscriptis 0.956 and selectolax 0.929.
  - Its `Text()` scores lower (0.875) only because it keeps what a browser does not draw: HTML comments, `title` and `aria-label` text, text the page declares hidden, and images' alt text. That is the series' rule "preserve content", and each of these carries its reason in the output. Its recall, 0.995, is the highest.
  - "What a browser shows" is `bench/gohtml` with `GOHTML_VISIBLE=1`: the body's blocks without stored data, declared-hidden blocks, attribute notes or images.
  - The Markdown converters (markitdown, html2text, htmd) score low on precision because they write every link's address into the text.
  - trafilatura extracts only the main content, by design, so its recall is low. It is listed for its speed, not as a like-for-like comparison.
- **Failures.** go-fast-html refused 7 pages with `ErrEncodingUnknown`: no declaration it reads, not UTF-8, no encoding certain. The other tools read every page, in an encoding they guessed. Given each page's HTTP `charset` as `Extractor.Charset`, as a crawler passes it on, 3 are refused: they declare nothing, and no encoding is certain from their bytes.

## History

- v0.3.0: 10.3 s with one worker, 2.08 s with 24; 10 pages refused (7 with the HTTP charset).
- v0.4.0: the `<meta charset>` up to the first MiB when the page is not UTF-8, and EUC-JP, GB18030, KOI8-R and -U, IBM866, x-mac-cyrillic and ISO-8859-2 to 16 (#22); a lexer that does not copy the page, and blocks of nodes (#23); memory reused from one page to the next (#24). Output of #23 and #24 byte-identical to that before them.

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

`run.log` here is the log of the run in `comparison.md`. `measure.py` waits before each run until the machine is quiet, and repeats a run during which other processes used more than a little CPU (`others_cpu_s` and `attempts` in each `measure.json`), so that a shared machine does not bias the times. `bench/gohtml` and `bench/rshtml` are modules of their own, so they are not part of the library's build.
