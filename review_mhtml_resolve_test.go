package gofasthtml

import (
	"encoding/base64"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// bodyImages lists the image blocks of the body part.
func bodyImages(doc *gofastcore.Document) []*gofastcore.Image {
	var out []*gofastcore.Image
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind == gofastcore.KindImage {
			out = append(out, b.Image)
		}
	}
	return out
}

// Review finding: mhtml.go:374-387, an <img> that names a part of the
// file marks that part shown whatever its media type, and mhtmlRest
// (line 539) then neither lists it nor hands it to ExtractChild. A frame's
// HTML (or a script, or any other document) that an <img> also names is
// dropped: its text is in no document. Only a part read as an image
// should be taken as shown; the others should still be embedded
// documents.
func TestReviewMHTMLImgNamingFrameDropsIt(t *testing.T) {
	src := mhtOf(`<p>page</p><iframe src="f.html"></iframe><img src="f.html">`,
		"Content-Type: text/html\r\nContent-Location: https://example.org/f.html\r\n\r\n<p>frame text</p>")
	c := &childReader{}
	doc := extractMHT(t, src, &gofastcore.Options{Child: c.read})
	if len(doc.Children) != 1 {
		t.Fatalf("the frame is not an embedded document: %d children, handed %q", len(doc.Children), c.names)
	}
}

// Review finding: mhtml.go:374 resolves only an <img>'s src. Chrome saves
// the image the page shows, which for <img srcset> and <picture><source>
// is the srcset candidate it chose, not the src. In the dev corpus
// (chrome-bundesregierung.mhtml) 27 images the page shows go to the stored
// part as images "no page shows", while the body's image blocks keep the
// unsaved src address and count as external references. The srcset
// candidates should be looked up in the file when the src is not in it.
func TestReviewMHTMLSrcsetImageNotResolved(t *testing.T) {
	png := base64.StdEncoding.EncodeToString(pngPixel)
	src := mhtOf(`<p>page</p><img src="small.png" srcset="big.png 2x" alt="a">`,
		"Content-Type: image/png\r\nContent-Transfer-Encoding: base64\r\nContent-Location: https://example.org/big.png\r\n\r\n"+png)
	doc := extractMHT(t, src, nil)
	imgs := bodyImages(doc)
	if len(imgs) != 1 || imgs[0].ID != "https://example.org/big.png" || len(doc.Parts) != 1 {
		var ids []string
		for _, im := range imgs {
			ids = append(ids, im.ID)
		}
		t.Fatalf("body image IDs %q; %d parts (the saved image is a stored item)", ids, len(doc.Parts))
	}
}

// Review finding: mhtml.go:137-148 resolves addresses with net/url, which
// takes a Windows path as written by Internet Explorer for a page saved
// from disk ("C:\dir\page.htm") for an opaque URL of scheme "c". A
// relative image address of that page then resolves to "c:/img/a.png"
// and is not found, though the part "C:\dir\img\a.png" is in the file.
// SUSPECTED as to how common such locations are; the fix is to read a
// drive-letter path (and backslashes) as a file: URL before resolving.
func TestReviewMHTMLWindowsPathNotResolved(t *testing.T) {
	png := base64.StdEncoding.EncodeToString(pngPixel)
	src := "From: \"Saved by Windows Internet Explorer 9\"\r\n" +
		"Content-Type: multipart/related; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\nContent-Location: C:\\dir\\page.htm\r\n\r\n" +
		"<p>page</p><img src=\"img/a.png\">\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-Transfer-Encoding: base64\r\nContent-Location: C:\\dir\\img\\a.png\r\n\r\n" + png + "\r\n" +
		"--B--\r\n"
	doc := extractMHT(t, src, &gofastcore.Options{Name: "a.mht"})
	imgs := bodyImages(doc)
	if len(imgs) != 1 || imgs[0].MIME != "image/png" {
		var ids []string
		for _, im := range imgs {
			ids = append(ids, im.ID+" "+im.MIME)
		}
		t.Fatalf("image not found in the file: %q; %d parts", ids, len(doc.Parts))
	}
}
