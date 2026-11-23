#!/bin/bash
# Run every HTML tool over the page list with 1 and 24 workers.
#   run_all_html.sh LIST RESULTS
set -u
LIST=$1; R=$2
B=${BENCH_ENV:-$(cd "$(dirname "$0")/../../bench-env" && pwd)}
HERE=$(cd "$(dirname "$0")" && pwd)
PY=$B/.venv-text/bin/python
RS=$B/rust-target/release/rshtml
GO=$HERE/gohtml/gohtml
mkdir -p "$R"
for w in 1 24; do
  for t in go-fast-html selectolax resiliparse bs4-lxml inscriptis html2text markitdown trafilatura rs-html2text rs-htmd; do
    out="$R/$t-$w"
    rm -rf "$out"; mkdir -p "$out"
    case $t in
      go-fast-html) cmd=("$GO" "$LIST" "$out" "$w");;
      rs-*) cmd=("$RS" "${t#rs-}" "$LIST" "$out" "$w");;
      *) cmd=("$PY" -I "$HERE/run_html.py" "$t" "$LIST" "$out" "$w");;
    esac
    echo "== $t w=$w load $(cut -d' ' -f1 /proc/loadavg)"
    "$PY" -I "$HERE/measure.py" "$out/measure.json" -- "${cmd[@]}" 2>&1 | grep -v -i warning | tail -1
    echo "load $(cut -d' ' -f1 /proc/loadavg)" > "$out/load.txt"
  done
done
echo ALL DONE
