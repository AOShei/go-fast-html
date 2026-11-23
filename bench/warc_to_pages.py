"""Write the HTML responses of a WARC file (or its first part) as files.

    python warc_to_pages.py sample.warc.gz OUTDIR [MAX]

OUTDIR/pages/NNNNNN.html holds each 200 response whose Content-Type is
text/html, its body as served (decompressed, dechunked by warcio);
OUTDIR/manifest.tsv gives file, size, the charset of the HTTP header (or
-), and the URL. A record cut by the end of a partial download ends the
reading.
"""
import os, sys
from warcio.archiveiterator import ArchiveIterator

src, out = sys.argv[1], sys.argv[2]
cap = int(sys.argv[3]) if len(sys.argv) > 3 else 10**9
os.makedirs(os.path.join(out, "pages"), exist_ok=True)
n = 0
seen = set()
with open(src, "rb") as f, open(os.path.join(out, "manifest.tsv"), "w") as man:
    man.write("file\tsize\tcharset\turl\n")
    try:
        for rec in ArchiveIterator(f):
            if rec.rec_type != "response" or rec.http_headers is None:
                continue
            if rec.http_headers.get_statuscode() != "200":
                continue
            ct = (rec.http_headers.get_header("Content-Type") or "").lower()
            if "text/html" not in ct:
                continue
            url = rec.rec_headers.get_header("WARC-Target-URI") or ""
            if url in seen:
                continue
            seen.add(url)
            body = rec.content_stream().read()
            if not body:
                continue
            cs = "-"
            if "charset=" in ct:
                cs = ct.split("charset=", 1)[1].split(";")[0].strip().strip('"\'') or "-"
            name = f"{n:06d}.html"
            with open(os.path.join(out, "pages", name), "wb") as p:
                p.write(body)
            man.write(f"{name}\t{len(body)}\t{cs}\t{url}\n")
            n += 1
            if n >= cap:
                break
    except Exception as e:  # the cut at the end of a partial download
        print("stopped:", type(e).__name__, e, file=sys.stderr)
print(n, "pages")
