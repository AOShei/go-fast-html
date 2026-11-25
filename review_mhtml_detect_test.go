package gofasthtml

import (
	"testing"

	"github.com/AOShei/go-fast-core"
)

// Review finding: mhtml.go:203 (isMHTML) and :480-482 (mhtmlMetadata)
// accept only From: <Saved by ...>. Internet Explorer 9 writes the
// program as a quoted string, From: "Saved by Windows Internet Explorer
// 9", as every IE file of the dev corpus shows (chromium-multi_frames_ie,
// -page_with_image_ie, -simple_page_ie, -relative_url,
// -page_with_css_and_js_ie). Without the name such a file is detected as
// HTML and read as markup, header and base64 included; with it, the
// program is not taken as Application. Both forms should be accepted.
func TestReviewMHTMLInternetExplorerQuotedFrom(t *testing.T) {
	src := "From: \"Saved by Windows Internet Explorer 9\"\r\n" +
		"Subject: A page\r\n" +
		"Date: Wed, 18 May 2011 16:50:17 -0700\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related;\r\n\ttype=\"text/html\";\r\n\tboundary=\"----=_NextPart_000\"\r\n" +
		"X-MimeOLE: Produced By Microsoft MimeOLE V6.1.7600.16776\r\n\r\n" +
		"This is a multi-part message in MIME format.\r\n\r\n" +
		"------=_NextPart_000\r\nContent-Type: text/html; charset=\"utf-8\"\r\nContent-Location: http://localhost/p.html\r\n\r\n" +
		"<p>x</p>\r\n------=_NextPart_000--\r\n"
	got := Detect("", []byte(src))
	doc := extractMHT(t, src, &gofastcore.Options{Name: "a.mht"})
	if got != "mht" || doc.Metadata.Application != "Windows Internet Explorer 9" {
		t.Fatalf("Detect %q, Application %q", got, doc.Metadata.Application)
	}
}

// Review finding: mhtml.go:298-304 takes the page's own Content-Location
// as the base whenever it has one, without resolving it against the
// file's Snapshot-Content-Location. A page whose location is relative
// ("p.html") then gives a relative base, and an <img src="i.png"> resolves
// to "i.png", which matches no absolute part location. SUSPECTED as to
// which program writes such a file; the fix is to resolve the page's
// location against Snapshot-Content-Location first.
func TestReviewMHTMLRelativeRootLocation(t *testing.T) {
	src := "From: <Saved by Blink>\r\nSnapshot-Content-Location: https://example.org/d/p.html\r\n" +
		"Content-Type: multipart/related; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\nContent-Location: p.html\r\n\r\n<p>x</p><img src=\"i.png\">\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-Location: https://example.org/d/i.png\r\n\r\nPNG\r\n--B--\r\n"
	doc := extractMHT(t, src, &gofastcore.Options{Name: "a.mht"})
	if len(doc.Parts) != 1 {
		t.Fatalf("%d parts; image not resolved", len(doc.Parts))
	}
}
