"""Write the text a browser shows for each page: the reference for
score_text.py.

    python -I reference_html.py LIST OUTDIR [N] [SEED]

A random sample of N pages (default 500) of LIST is loaded in headless
Chromium from disk, with JavaScript off and every network request
blocked, so only the page's own HTML and inline styles count; the
reference is document.body.innerText, the text as rendered (display:none
and the like left out, line breaks where blocks end). OUTDIR/NAME.txt for
each; a page that fails to load within 20 s is left out.
"""
import os, random, sys
from playwright.sync_api import sync_playwright

lst, out = sys.argv[1], sys.argv[2]
n = int(sys.argv[3]) if len(sys.argv) > 3 else 500
seed = int(sys.argv[4]) if len(sys.argv) > 4 else 1
files = [l.strip() for l in open(lst) if l.strip()]
random.Random(seed).shuffle(files)
os.makedirs(out, exist_ok=True)
done = 0
with sync_playwright() as p:
    b = p.chromium.launch()
    ctx = b.new_context(java_script_enabled=False)
    ctx.route("**/*", lambda route: route.continue_() if route.request.url.startswith("file:") else route.abort())
    page = ctx.new_page()
    for f in files:
        if done >= n:
            break
        try:
            page.goto("file://" + f, timeout=20000, wait_until="domcontentloaded")
            text = page.evaluate("() => document.body ? document.body.innerText : ''")
        except Exception as e:
            print("skip", os.path.basename(f), type(e).__name__, file=sys.stderr)
            continue
        with open(os.path.join(out, os.path.basename(f) + ".txt"), "w", encoding="utf-8") as o:
            o.write(text)
        done += 1
    b.close()
print(done, "references")
