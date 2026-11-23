"""Extract the text of every file in a list with one Python HTML tool.

    python -I run_html.py TOOL LIST OUTDIR WORKERS

Each worker process imports the tool once and reads its share of the
files (in order, interleaved). For each file: the seconds the call took
(time.perf_counter, after the bytes are read), whether it raised (and the
exception's type), and the length of the text. The text is written to
OUTDIR/text/NAME.txt for the quality comparison. A call longer than 60 s
is stopped and counted as a timeout. OUTDIR/results.jsonl gets one line
a file.
"""
import json, os, signal, sys, time
from multiprocessing import Pool

TOOL, LIST, OUT, WORKERS = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])


def make(tool):
    if tool == "bs4-lxml":
        from bs4 import BeautifulSoup
        def f(b):
            s = BeautifulSoup(b, "lxml")
            for t in s(["script", "style", "noscript", "template"]):
                t.decompose()
            return s.get_text("\n")
        return f
    if tool == "html2text":
        import html2text
        from bs4 import UnicodeDammit
        def f(b):
            h = html2text.HTML2Text()
            h.body_width = 0
            return h.handle(UnicodeDammit(b, is_html=True).unicode_markup or "")
        return f
    if tool == "inscriptis":
        from inscriptis import get_text
        from bs4 import UnicodeDammit
        return lambda b: get_text(UnicodeDammit(b, is_html=True).unicode_markup or "")
    if tool == "resiliparse":
        from resiliparse.extract.html2text import extract_plain_text
        from resiliparse.parse.html import HTMLTree
        from resiliparse.parse.encoding import detect_encoding, bytes_to_str
        return lambda b: extract_plain_text(HTMLTree.parse(bytes_to_str(b, detect_encoding(b))), main_content=False)
    if tool == "selectolax":
        from selectolax.lexbor import LexborHTMLParser
        def f(b):
            t = LexborHTMLParser(b)
            for n in t.css("script, style, noscript, template"):
                n.decompose()
            return t.body.text(separator="\n") if t.body else ""
        return f
    if tool == "markitdown":
        import io
        from markitdown import MarkItDown, StreamInfo
        md = MarkItDown()
        return lambda b: md.convert_stream(io.BytesIO(b), stream_info=StreamInfo(extension=".html", mimetype="text/html")).text_content
    if tool == "trafilatura":
        import trafilatura
        return lambda b: trafilatura.extract(b) or ""
    raise SystemExit("unknown tool " + tool)


class Timeout(Exception):
    pass


def alarm(*_):
    raise Timeout()


def work(args):
    k, files = args
    signal.signal(signal.SIGALRM, alarm)
    f = make(TOOL)
    out = []
    for path in files:
        name = os.path.basename(path)
        b = open(path, "rb").read()
        t0 = time.perf_counter()
        err, n = None, 0
        signal.alarm(60)
        try:
            text = f(b) or ""
            n = len(text)
        except Timeout:
            err = "Timeout"
        except BaseException as e:  # a tool's failure, whatever it is
            err = type(e).__name__
        finally:
            signal.alarm(0)
        dt = time.perf_counter() - t0
        if err is None:
            with open(os.path.join(OUT, "text", name + ".txt"), "w", encoding="utf-8", errors="replace") as o:
                o.write(text)
        out.append({"file": name, "s": dt, "err": err, "chars": n})
    return out


def main():
    files = [l.strip() for l in open(LIST) if l.strip()]
    os.makedirs(os.path.join(OUT, "text"), exist_ok=True)
    shares = [(k, files[k::WORKERS]) for k in range(WORKERS)]
    if WORKERS == 1:
        res = [work(shares[0])]
    else:
        with Pool(WORKERS) as p:
            res = p.map(work, shares)
    with open(os.path.join(OUT, "results.jsonl"), "w") as o:
        for part in res:
            for r in part:
                o.write(json.dumps(r) + "\n")


if __name__ == "__main__":
    main()
