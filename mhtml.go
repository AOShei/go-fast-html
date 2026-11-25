package gofasthtml

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"net/url"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/blocks"
	"github.com/AOShei/go-fast-html/internal/charset"
	"github.com/AOShei/go-fast-html/internal/mimepart"
)

// maxMIMEHeader bounds the header section of an MHTML file and of each of
// its parts.
const maxMIMEHeader = 1 << 20

// maxPartRef bounds the Content-Location or Content-ID used as an image's
// ID and a child's name; a longer one is replaced by a hash.
const maxPartRef = 2048

// mpart is one part of an MHTML file.
type mpart struct {
	mediatype string
	params    map[string]string
	cte       string
	location  string // Content-Location as written
	resolved  string // Content-Location resolved against the base, without fragment
	id        string // Content-ID without its angle brackets
	body      []byte // still in its transfer encoding
}

// ref is how the part is named: its location, else its Content-ID.
func (p *mpart) ref() string {
	r := p.location
	if r == "" && p.id != "" {
		r = "cid:" + p.id
	}
	if len(r) > maxPartRef {
		sum := sha256.Sum256([]byte(r))
		r = "sha256:" + hex.EncodeToString(sum[:16])
	}
	return r
}

// mhtmlArchive is what an MHTML file holds, and the lookups of a page's
// references in it.
type mhtmlArchive struct {
	parts   []mpart
	base    *url.URL
	byLoc   map[string]int
	byID    map[string]int
	decode  *gofastcore.DecodeBudget
	repairs map[string]int // input.repaired messages, counted

	tooMany, headerCut, tooDeep bool // bounds reached while splitting
}

// maxMIMEDepth bounds how deeply multipart parts nest in an MHTML file.
const maxMIMEDepth = 8

// split adds the parts of a multipart body to a.parts, the parts of a
// multipart part in its place (Internet Explorer puts the page in a
// multipart/alternative). It reports whether the boundary was found.
func (a *mhtmlArchive) split(body []byte, boundary string, depth, maxParts int) bool {
	mp := mimepart.Split(body, boundary, maxParts)
	delim := []byte("--" + boundary)
	for _, p := range mp.Parts {
		if bytes.Contains(p, delim) {
			// A boundary that does not begin a line, after a part in
			// binary encoding with no line break at its end.
			mp = mimepart.SplitAnywhere(body, boundary, maxParts)
			a.repaired("boundaries that do not begin a line were taken as boundaries")
			break
		}
	}
	if len(mp.Parts) == 0 {
		return false
	}
	if !mp.Closed {
		a.repaired("a multipart body has no closing boundary; its last part runs to the end of the body")
	}
	a.tooMany = a.tooMany || mp.More
	for _, raw := range mp.Parts {
		if len(a.parts) >= maxParts {
			a.tooMany = true
			return true
		}
		ph, pat, pprob := mimepart.ParseHeader(raw, maxMIMEHeader)
		a.headerCut = a.headerCut || pprob.Truncated
		p := newPart(ph, raw[pat:])
		if strings.HasPrefix(p.mediatype, "multipart/") && p.params["boundary"] != "" {
			if depth+1 >= maxMIMEDepth {
				a.tooDeep = true
				continue
			}
			if a.split(p.body, p.params["boundary"], depth+1, maxParts) {
				continue
			}
		}
		a.parts = append(a.parts, p)
	}
	return true
}

// find returns the part a reference of the page names: "cid:" and a
// Content-ID, or an address resolved against the page's location and
// compared with the parts' resolved locations, or failing that the
// address as written.
func (a *mhtmlArchive) find(ref string) (int, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, false
	}
	if len(ref) > 4 && strings.EqualFold(ref[:4], "cid:") {
		id := ref[4:]
		if u, err := url.PathUnescape(id); err == nil {
			id = u
		}
		i, ok := a.byID[strings.Trim(id, "<>")]
		return i, ok
	}
	if i, ok := a.byLoc[a.resolve(ref)]; ok {
		return i, true
	}
	i, ok := a.byLoc[ref]
	return i, ok
}

// resolve resolves an address against the base, without its fragment.
func (a *mhtmlArchive) resolve(ref string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if a.base != nil {
		u = a.base.ResolveReference(u)
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

// data decodes a part's body from its transfer encoding, within the
// decode budget. ok is false past the budget.
func (a *mhtmlArchive) data(p *mpart) ([]byte, bool) {
	var out []byte
	switch p.cte {
	case "base64":
		out = mimepart.Base64(p.body)
		if mimepart.Base64Bad(p.body) {
			a.repaired("base64 with characters outside its alphabet; they were skipped")
		}
	case "quoted-printable":
		out = mimepart.QuotedPrintable(p.body)
	case "", "7bit", "8bit", "binary":
		out = p.body
	default:
		a.repaired("parts declare a transfer encoding that is not base64, quoted-printable, 7bit, 8bit or binary; their bytes were taken as they are")
		out = p.body
	}
	if !a.decode.Take(int64(len(out))) {
		return nil, false
	}
	return out, true
}

func (a *mhtmlArchive) repaired(msg string) {
	if a.repairs == nil {
		a.repairs = map[string]int{}
	}
	a.repairs[msg]++
}

// fieldValue returns a header field's value, unfolded, as text.
func fieldValue(h mimepart.Header, name string) string {
	v, ok := h.Get(name)
	if !ok {
		return ""
	}
	return strings.TrimSpace(string(v))
}

// isMHTML reports whether src begins with the header of an MHTML file
// that a browser saved: a Snapshot-Content-Location field (Chrome, Edge)
// or a From field "<Saved by ...>" (Internet Explorer, Chrome, UnMHT),
// with a multipart/related Content-Type. A message is not taken for one.
func isMHTML(src []byte) bool {
	h, _, _ := mimepart.ParseHeader(src[:min(len(src), 16<<10)], 16<<10)
	ct, _ := mimepart.Params([]byte(fieldValue(h, "Content-Type")))
	if ct != "multipart/related" {
		return false
	}
	if _, ok := h.Get("Snapshot-Content-Location"); ok {
		return true
	}
	return strings.HasPrefix(strings.ToLower(fieldValue(h, "From")), "<saved by ")
}

var wordDecoder = &mime.WordDecoder{CharsetReader: func(label string, r io.Reader) (io.Reader, error) {
	enc, ok := charset.Lookup(label)
	if !ok {
		return nil, fmt.Errorf("charset %q not decoded", label)
	}
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return nil, err
	}
	out, _, _, _ := charset.Decode(b, charset.Result{Enc: enc}, 4<<20)
	return bytes.NewReader(out), nil
}}

// headerText decodes the encoded words of a header value (RFC 2047);
// a value that cannot be decoded is kept as written.
func headerText(v string) string {
	if !strings.Contains(v, "=?") {
		return v
	}
	if d, err := wordDecoder.DecodeHeader(v); err == nil {
		return d
	}
	return v
}

// mhtml reads a web page saved as one file (MHTML, RFC 2557): a MIME
// message whose parts are the page and the resources it uses. The page,
// the part the Content-Type's start parameter names or else the first
// HTML part, is read as HTML into the body part, with its images taken
// from the file; images no page shows go to the stored part, as an EPUB's
// do; other HTML parts (frames) and other files are embedded documents;
// style sheets and fonts are styling and are not listed.
func (e *Extractor) mhtml(ctx context.Context, doc *gofastcore.Document, src []byte, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget) (*gofastcore.Document, error) {
	h, at, prob := mimepart.ParseHeader(src, maxMIMEHeader)
	if prob.Truncated {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("the file's header section is longer than %d bytes; the rest of it was not read", maxMIMEHeader))
	}
	a := &mhtmlArchive{byLoc: map[string]int{}, byID: map[string]int{}, decode: decode}
	ct, ctParams := mimepart.Params([]byte(fieldValue(h, "Content-Type")))
	if len(h) == 0 {
		// No header at all: the file is taken as the page itself.
		a.repaired("the file has no MIME header; it was read as one HTML page")
		a.parts = append(a.parts, mpart{mediatype: "text/html", params: map[string]string{}, body: src})
	} else if strings.HasPrefix(ct, "multipart/") && ctParams["boundary"] != "" {
		maxParts := int(min(gofastcore.ResolveLimit(int64(e.MaxArchiveMembers), DefaultMaxArchiveMembers), 1<<30))
		if !a.split(src[at:], ctParams["boundary"], 0, maxParts) {
			a.repaired("the boundary of the multipart body is never found; the body was read as one HTML page")
			a.parts = append(a.parts, mpart{mediatype: "text/html", params: map[string]string{}, body: src[at:]})
		}
		if a.tooMany {
			doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				fmt.Sprintf("the file has more than %d parts; the rest were not read", maxParts))
		}
		if a.headerCut {
			doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				fmt.Sprintf("a part's header section is longer than %d bytes; the rest of it was not read", maxMIMEHeader))
		}
		if a.tooDeep {
			doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				fmt.Sprintf("multipart parts nest more than %d deep; the deeper ones were not read", maxMIMEDepth))
		}
	} else {
		// A single-part file: the message is the page.
		a.parts = append(a.parts, newPart(h, src[at:]))
	}

	// The page: the start parameter's part, else the first HTML part, else
	// the first part.
	root := -1
	if start := strings.Trim(ctParams["start"], "<> \t"); start != "" {
		for i := range a.parts {
			if a.parts[i].id == start {
				root = i
				break
			}
		}
	}
	if root < 0 {
		for i := range a.parts {
			if a.parts[i].mediatype == "text/html" || a.parts[i].mediatype == "application/xhtml+xml" {
				root = i
				break
			}
		}
	}
	if root < 0 && len(a.parts) > 0 {
		root = 0
	}

	// Addresses are resolved against the page's location.
	baseRef := fieldValue(h, "Snapshot-Content-Location")
	if root >= 0 && a.parts[root].location != "" {
		baseRef = a.parts[root].location
	}
	if u, err := url.Parse(baseRef); err == nil && baseRef != "" {
		a.base = u
	}
	for i := range a.parts {
		p := &a.parts[i]
		if p.location != "" {
			p.resolved = a.resolve(p.location)
			for _, k := range []string{p.resolved, p.location} {
				if _, dup := a.byLoc[k]; !dup {
					a.byLoc[k] = i
				}
			}
		}
		if p.id != "" {
			if _, dup := a.byID[p.id]; !dup {
				a.byID[p.id] = i
			}
		}
	}

	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1}
	t := &tally{}
	shown := map[int]bool{}
	var res *blocks.Result
	if root >= 0 && !isPage(a.parts[root].mediatype) {
		// The file holds no page, only a resource saved on its own (an
		// image a browser showed by itself).
		rp := &a.parts[root]
		shown[root] = true
		if strings.HasPrefix(rp.mediatype, "image/") && opts.Images != gofastcore.ImagesNone {
			ref := rp.ref()
			img := &gofastcore.Image{ID: ref, MIME: rp.mediatype}
			if opts.Images == gofastcore.ImagesData {
				if d, ok := a.data(rp); ok {
					img.Data = d
				} else {
					part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
						"the limit on decoded bytes was reached; 1 image has no data")
				}
			}
			if text.Take(gofastcore.BlockCost + len(ref) + len(img.Data)) {
				part.Blocks = append(part.Blocks, gofastcore.Block{Kind: gofastcore.KindImage, Image: img})
				part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartImageOnly, "1 image, no text")
			} else {
				part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
					"the limit on the amount of text was reached; the body is cut off there")
			}
		} else {
			shown[root] = false // listed with the rest
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText,
				fmt.Sprintf("the file holds no HTML page; its first part is %s", blocksLabel(rp.mediatype[:min(len(rp.mediatype), 100)])))
		}
		part.Finish()
	} else if root >= 0 {
		rp := &a.parts[root]
		shown[root] = true
		page, ok := a.data(rp)
		if !ok {
			part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				"the limit on decoded bytes was reached; the page was not read")
			part.Finish()
		} else {
			overBudget := 0
			// The part's charset, else Extractor.Charset; a label not
			// decoded here gives way to Extractor.Charset when one is
			// given, and is otherwise reported as one not decoded.
			cs := rp.params["charset"]
			if _, known := charset.Lookup(strings.Trim(cs, "\"' ")); e.Charset != "" && (cs == "" || !known) {
				cs = e.Charset
			}
			pc := pageConfig{opts: opts, text: text, decode: decode, charset: cs,
				xhtml: rp.mediatype == "application/xhtml+xml",
				image: func(ref string) (blocks.ImageRef, bool) {
					i, ok := a.find(ref)
					if !ok || i == root {
						return blocks.ImageRef{}, false
					}
					shown[i] = true
					p := &a.parts[i]
					return blocks.ImageRef{ID: p.ref(), MIME: p.mediatype, Data: func() ([]byte, bool) {
						d, ok := a.data(p)
						if !ok {
							overBudget++
						}
						return d, ok
					}}, true
				},
				internal: func(ref string) bool {
					_, ok := a.find(ref)
					return ok
				},
				noDataElsewhere: func() int { return overBudget },
			}
			var err error
			res, err = e.page(ctx, page, "html", pc, &part, t)
			if err != nil {
				return nil, err
			}
			if overBudget > 0 {
				part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
					"the limit on decoded bytes was reached; "+count(overBudget, "image has", "images have")+" no data")
			}
		}
	} else {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "the file has no parts")
		part.Finish()
	}
	if res != nil {
		doc.Metadata = res.Meta
	}
	mhtmlMetadata(&doc.Metadata, h, baseRef)
	doc.Parts = append(doc.Parts, part)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mhtmlRest(ctx, a, shown, opts, text, doc, t)
	for msg, n := range a.repairs {
		if n > 1 {
			msg = fmt.Sprintf("%s (%d times)", msg, n)
		}
		doc.Warn(gofastcore.SeverityInfo, gofastcore.WarnInputRepaired, msg)
	}
	t.finish(doc)
	return doc, nil
}

// mediaType reports whether s is a media type: a type and a subtype,
// each a MIME token (RFC 2045), at most 127 bytes.
func mediaType(s string) bool {
	t, sub, ok := strings.Cut(s, "/")
	return ok && len(s) <= 127 && mimeToken(t) && mimeToken(sub)
}

func mimeToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7F || strings.IndexByte("()<>@,;:\\\"/[]?=", c) >= 0 {
			return false
		}
	}
	return true
}

// isPage reports whether a media type is a page read as HTML.
func isPage(mt string) bool { return mt == "text/html" || mt == "application/xhtml+xml" }

// newPart reads a part's header.
func newPart(h mimepart.Header, body []byte) mpart {
	mt, params := mimepart.Params([]byte(fieldValue(h, "Content-Type")))
	if _, ok := h.Get("Content-Type"); !ok {
		mt = "text/plain" // RFC 2045's default
	} else if !mediaType(mt) {
		mt = "application/octet-stream"
	}
	cte, _ := mimepart.Params([]byte(fieldValue(h, "Content-Transfer-Encoding")))
	return mpart{mediatype: mt, params: params, cte: cte,
		location: cleanLine(fieldValue(h, "Content-Location")),
		id:       cleanLine(strings.Trim(fieldValue(h, "Content-ID"), "<> \t")),
		body:     body}
}

// mhtmlMetadata adds what the file's header says to what the page's own
// metadata says: the page's title, else the Subject (the title the
// browser wrote); the Date as Created; the program in a From field
// "<Saved by X>" as Application; the page's address and every header
// field in Extra under its lowercase name.
func mhtmlMetadata(md *gofastcore.Metadata, h mimepart.Header, location string) {
	if md.Title == "" {
		md.Title = gofastcore.ExpandLigatures(cleanLine(headerText(fieldValue(h, "Subject"))))
	}
	if md.Created.IsZero() {
		if t, err := mail.ParseDate(fieldValue(h, "Date")); err == nil {
			md.Created = t
		}
	}
	if from := fieldValue(h, "From"); md.Application == "" && len(from) > 11 &&
		strings.HasPrefix(strings.ToLower(from), "<saved by ") && strings.HasSuffix(from, ">") {
		md.Application = cleanLine(from[10 : len(from)-1])
	}
	for _, f := range h {
		key := strings.ToLower(f.Name)
		v := cleanLine(headerText(strings.TrimSpace(string(f.Value))))
		if key == "" || v == "" {
			continue
		}
		if md.Extra == nil {
			md.Extra = map[string]string{}
		}
		if old, ok := md.Extra[key]; ok {
			v = old + "\n" + v
		}
		md.Extra[key] = v
	}
	if location != "" {
		if md.Extra == nil {
			md.Extra = map[string]string{}
		}
		if _, ok := md.Extra["url"]; !ok {
			md.Extra["url"] = cleanLine(location)
		}
	}
}

// cleanLine drops control characters and makes the text valid UTF-8.
func cleanLine(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || r >= 0x80 && r < 0xA0 {
			return -1
		}
		return r
	}, s)
}

// styling reports whether a media type is styling: a style sheet or a
// font. Such parts are not listed, as an EPUB's style sheets are not.
func styling(mt string) bool {
	return mt == "text/css" || strings.HasPrefix(mt, "font/") || strings.Contains(mt, "font") ||
		mt == "application/vnd.ms-fontobject"
}

// mhtmlRest lists the parts besides the page: images it does not show in
// a stored part, as manifest items; other documents as embedded
// documents. Styling is not listed.
func (e *Extractor) mhtmlRest(ctx context.Context, a *mhtmlArchive, shown map[int]bool, opts *gofastcore.Options,
	text *gofastcore.TextBudget, doc *gofastcore.Document, t *tally) {
	if opts.Parts != (gofastcore.PartRange{}) {
		return // as an EPUB's manifest, read with the whole document only
	}
	stored := gofastcore.Part{Kind: gofastcore.PartStored, Number: 2, Blocks: t.stored}
	whole := true
	overBudget, childOverBudget, cut := 0, 0, false
	for i := range a.parts {
		p := &a.parts[i]
		if shown[i] || styling(p.mediatype) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return
		}
		if strings.HasPrefix(p.mediatype, "image/") {
			if !whole || opts.Images == gofastcore.ImagesNone || cut {
				continue
			}
			ref := p.ref()
			img := &gofastcore.Image{ID: ref, MIME: p.mediatype}
			if opts.Images == gofastcore.ImagesData {
				if d, ok := a.data(p); ok {
					img.Data = d
				} else {
					overBudget++
				}
			}
			if !text.Take(gofastcore.BlockCost + len(ref)*2 + len(img.Data)) {
				cut = true
				stored.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
					"the limit on the amount of text was reached; the stored part is cut off there")
				continue
			}
			b := gofastcore.Block{Kind: gofastcore.KindImage, Image: img,
				Attrs: map[string]string{gofastcore.AttrStored: StoredManifestItem}}
			if ref != "" {
				b.Attrs[gofastcore.AttrName] = ref
			}
			stored.Blocks = append(stored.Blocks, b)
			continue
		}
		if strings.Contains(p.mediatype, "javascript") || strings.Contains(p.mediatype, "ecmascript") {
			t.active++
		}
		mt := p.mediatype
		if cs := p.params["charset"]; cs != "" && strings.HasPrefix(mt, "text/") && len(cs) <= 64 && mimeToken(cs) {
			mt += "; charset=" + cs
		}
		name := p.ref()
		if opts.Child == nil {
			doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, nil, int64(len(p.body))))
			continue
		}
		data, ok := a.data(p)
		if !ok {
			childOverBudget++
			doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, nil, int64(len(p.body))))
			continue
		}
		doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, bytes.NewReader(data), int64(len(data))))
	}
	if overBudget > 0 {
		stored.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; "+count(overBudget, "image has", "images have")+" no data")
	}
	if childOverBudget > 0 {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; "+count(childOverBudget, "embedded document was", "embedded documents were")+" not read")
	}
	if whole && len(stored.Blocks) > 0 {
		stored.Finish()
		doc.Parts = append(doc.Parts, stored)
	} else {
		for _, w := range stored.Warnings {
			doc.Warn(w.Severity, w.Code, w.Message)
		}
	}
}
