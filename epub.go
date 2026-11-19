package gofasthtml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-html/internal/blocks"
	"github.com/AOShei/go-fast-html/internal/epub"
)

// Defaults for EPUB.
const (
	// DefaultMaxArchiveMembers bounds the number of files in an EPUB's
	// zip archive.
	DefaultMaxArchiveMembers = 100_000
	// DefaultMaxParts bounds the chapters read when Limits.MaxParts is
	// zero.
	DefaultMaxParts = 100_000
)

// Warning codes raised while an EPUB is read.
const (
	// WarnNonLinear: the chapter is marked linear="no" in the spine: a
	// reading system shows it only when it is linked to (notes, answers).
	// It is extracted in its place in the spine.
	WarnNonLinear = "epub.non_linear"
	// WarnEncrypted: resources of the publication are encrypted (DRM)
	// and were not read. Font obfuscation is not counted.
	WarnEncrypted = "epub.encrypted"
	// WarnMimetype: the archive's mimetype member is missing or does not
	// say application/epub+zip; the archive was read as an EPUB all the
	// same.
	WarnMimetype = "epub.mimetype"
)

// StoredManifestItem is the Attrs["stored"] value of an image the
// publication stores and no chapter shows.
const StoredManifestItem = "manifest_item"

// epub reads an EPUB publication.
func (e *Extractor) epub(ctx context.Context, doc *gofastcore.Document, src []byte, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget) (*gofastcore.Document, error) {
	maxMembers := int(gofastcore.ResolveLimit(int64(e.MaxArchiveMembers), DefaultMaxArchiveMembers))
	if e.MaxArchiveMembers < 0 {
		maxMembers = -1
	}
	a, err := epub.Open(src, maxMembers)
	switch {
	case errors.Is(err, epub.ErrTooManyMembers):
		return nil, fmt.Errorf("gofasthtml: %v: %w", err, gofastcore.ErrLimitExceeded)
	case err != nil:
		return nil, fmt.Errorf("gofasthtml: %v: %w", err, gofastcore.ErrMalformed)
	}
	if a.Repaired {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputRepaired,
			"the archive's directory could not be read; its members were found by their own headers")
	}
	if len(a.Duplicates) > 0 {
		doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputAmbiguous,
			fmt.Sprintf("%d member names occur more than once; the first of each was read", len(a.Duplicates)))
	}
	if mt, err := a.Read("mimetype", decode, 256); err != nil || strings.TrimSpace(string(mt)) != "application/epub+zip" {
		doc.Warn(gofastcore.SeverityInfo, WarnMimetype, "the mimetype member is missing or does not say application/epub+zip")
	}
	pkg, err := a.ReadPackage(decode)
	if err != nil {
		if decode.Left() < 0 {
			return nil, fmt.Errorf("gofasthtml: %v: %w", err, gofastcore.ErrLimitExceeded)
		}
		return nil, fmt.Errorf("gofasthtml: %v: %w", err, gofastcore.ErrMalformed)
	}
	doc.Metadata = epubMetadata(pkg)
	encrypted := a.Encryption(decode)
	t := &tally{}

	// The table of contents, counted before the first part.
	spineIndex := map[string]int{}
	for i, s := range pkg.Spine {
		if it, ok := pkg.ByID[s.IDRef]; ok && it.Path != "" {
			if _, seen := spineIndex[it.Path]; !seen {
				spineIndex[it.Path] = i + 1
			}
		}
	}
	navPath := ""
	var entries []epub.Entry
	for _, it := range pkg.Manifest {
		if hasProperty(it.Properties, "nav") && it.Path != "" && encrypted[it.Path] == "" {
			if b, err := a.Read(it.Path, decode, epub.MaxXML); err == nil {
				navPath = it.Path
				entries = epub.Nav(ctx, it.Path, b)
			}
			break
		}
	}
	ncxPath := ""
	if it, ok := pkg.ByID[pkg.NCX]; ok && it.Path != "" {
		ncxPath = it.Path
	} else {
		for _, it := range pkg.Manifest {
			if it.MediaType == "application/x-dtbncx+xml" {
				ncxPath = it.Path
				break
			}
		}
	}
	if len(entries) == 0 && ncxPath != "" && encrypted[ncxPath] == "" {
		if b, err := a.Read(ncxPath, decode, epub.MaxXML); err == nil {
			entries = epub.NCX(ncxPath, b)
		}
	}
	names := map[int]string{}
	for _, en := range entries {
		title := blocks.Clean(en.Title)
		if title == "" {
			continue
		}
		if !text.Take(len(title)) {
			doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				"the limit on the amount of text was reached in the table of contents; the rest of the publication was not read")
			t.finish(doc)
			if len(doc.Parts) == 0 {
				doc.Parts = append(doc.Parts, emptyChapter())
			}
			return doc, nil
		}
		n := spineIndex[en.Path]
		doc.Outline = append(doc.Outline, gofastcore.OutlineItem{Title: title, Level: max(en.Level, 1), Part: n})
		if _, ok := names[n]; !ok && n > 0 {
			names[n] = title
		}
	}

	// The chapters.
	if opts.Parts.First > len(pkg.Spine) && len(pkg.Spine) > 0 || opts.Parts.Last != 0 && opts.Parts.First > opts.Parts.Last {
		return nil, fmt.Errorf("gofasthtml: the publication has %d chapters: %w", len(pkg.Spine), gofastcore.ErrNoPartsSelected)
	}
	maxParts := int(gofastcore.ResolveLimit(int64(opts.Limits.MaxParts), DefaultMaxParts))
	shown := map[string]bool{} // images a chapter shows
	lockedChapters, selected := 0, 0
	cutShort := false
	for i, s := range pkg.Spine {
		n := i + 1
		if !opts.Parts.Contains(n) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(doc.Parts) >= maxParts {
			doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				fmt.Sprintf("the publication has more chapters than the limit of %d; the rest were not read", maxParts))
			cutShort = true
			break
		}
		selected++
		part := gofastcore.Part{Kind: gofastcore.PartChapter, Number: n}
		it, ok := pkg.ByID[s.IDRef]
		switch {
		case !ok || it.Path == "":
			part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the spine names an item that is not in the manifest")
		case encrypted[it.Path] != "":
			lockedChapters++
			part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the chapter is encrypted and was not read")
			part.Warn(gofastcore.SeverityError, WarnEncrypted, "encrypted with "+blocksLabel(encrypted[it.Path]))
		default:
			if err := e.chapter(ctx, a, pkg, it, opts, text, decode, &part, t, shown); err != nil {
				return nil, err
			}
		}
		if !s.Linear {
			part.Warn(gofastcore.SeverityInfo, WarnNonLinear, "the spine marks this chapter linear=\"no\"")
		}
		if part.Name == "" {
			part.Name = names[n]
		}
		if part.Name != "" && !text.Take(len(part.Name)) {
			part.Name = ""
		}
		part.MarkHidden()
		doc.Parts = append(doc.Parts, part)
		if text.Spent() {
			cutShort = true
			break
		}
	}
	if selected > 0 && lockedChapters == selected {
		return nil, fmt.Errorf("gofasthtml: every chapter is encrypted (DRM): %w", gofastcore.ErrUnsupported)
	}
	if len(encrypted) > 0 {
		doc.Warn(gofastcore.SeverityError, WarnEncrypted, fmt.Sprintf("%d resources are encrypted and were not read", len(encrypted)))
	}
	if len(doc.Parts) == 0 {
		doc.Parts = append(doc.Parts, emptyChapter())
	}

	// What the publication stores beside its chapters, with the whole
	// document only.
	if opts.Parts == (gofastcore.PartRange{}) && !cutShort {
		e.manifestRest(ctx, a, pkg, opts, text, decode, doc, shown, encrypted, navPath, ncxPath, t)
	}
	t.finish(doc)
	return doc, nil
}

func emptyChapter() gofastcore.Part {
	p := gofastcore.Part{Kind: gofastcore.PartChapter, Number: 1}
	p.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "the publication has no chapters")
	p.MarkHidden()
	return p
}

// chapter reads one content document of the spine into part.
func (e *Extractor) chapter(ctx context.Context, a *epub.Archive, pkg *epub.Package, it *epub.Item, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget, part *gofastcore.Part, t *tally, shown map[string]bool) error {
	b, err := a.Read(it.Path, decode, -1)
	if err != nil {
		if errors.Is(err, epub.ErrBudget) {
			part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the limit on decoded bytes was reached; the chapter was not read")
		} else {
			part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the chapter could not be read from the archive")
		}
		return nil
	}
	pc := pageConfig{opts: opts, text: text, decode: decode,
		xhtml: it.MediaType != "text/html",
		image: func(src string) (blocks.ImageRef, bool) {
			p, ok := epub.Resolve(it.Path, src)
			if !ok || !a.Has(p) {
				return blocks.ImageRef{}, false
			}
			shown[p] = true
			mime := ""
			if item, ok := pkg.ByPath[p]; ok {
				mime = item.MediaType
			}
			return blocks.ImageRef{ID: p, MIME: mime, Data: func() ([]byte, bool) {
				d, err := a.Read(p, decode, -1)
				return d, err == nil
			}}, true
		},
		internal: func(ref string) bool {
			p, ok := epub.Resolve(it.Path, ref)
			return ok && a.Has(p)
		},
	}
	res, err := e.page(ctx, b, "html", pc, part, t)
	if err != nil {
		return err
	}
	if res != nil {
		part.Name = res.Meta.Title
	}
	return nil
}

// manifestRest lists what the manifest holds besides the spine: images
// no chapter shows, in a stored part, and other documents as embedded
// documents.
func (e *Extractor) manifestRest(ctx context.Context, a *epub.Archive, pkg *epub.Package, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget, doc *gofastcore.Document, shown map[string]bool,
	encrypted map[string]string, navPath, ncxPath string, t *tally) {
	inSpine := map[string]bool{}
	for _, s := range pkg.Spine {
		if it, ok := pkg.ByID[s.IDRef]; ok {
			inSpine[it.Path] = true
		}
	}
	last := len(pkg.Spine)
	if n := len(doc.Parts); n > 0 {
		last = max(last, doc.Parts[n-1].Number)
	}
	stored := gofastcore.Part{Kind: gofastcore.PartStored, Number: last + 1, Hidden: gofastcore.HiddenInvisible}
	for _, it := range pkg.Manifest {
		if it.Path == "" || inSpine[it.Path] || it.Path == navPath || it.Path == ncxPath || !a.Has(it.Path) {
			continue
		}
		mt := it.MediaType
		switch {
		case strings.HasPrefix(mt, "image/"):
			if shown[it.Path] || opts.Images == gofastcore.ImagesNone {
				continue
			}
			img := &gofastcore.Image{ID: it.Path, MIME: mt}
			if opts.Images == gofastcore.ImagesData && encrypted[it.Path] == "" {
				if d, err := a.Read(it.Path, decode, -1); err == nil {
					img.Data = d
				}
			}
			b := gofastcore.Block{Kind: gofastcore.KindImage, Image: img,
				Attrs: map[string]string{gofastcore.AttrStored: StoredManifestItem, gofastcore.AttrName: it.Path}}
			if hasProperty(it.Properties, "cover-image") || pkg.Meta["cover"] == it.ID {
				b.Text = "cover"
			}
			if !text.Take(gofastcore.BlockCost + len(it.Path)*2 + len(img.Data)) {
				stored.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the limit on the amount of text was reached; the stored part is cut off there")
				goto done
			}
			stored.Blocks = append(stored.Blocks, b)
		case mt == "text/css":
			// Styling, not evaluated.
		default:
			if strings.Contains(mt, "javascript") || strings.Contains(mt, "ecmascript") {
				t.active++
			}
			if err := ctx.Err(); err != nil {
				return
			}
			m, _ := a.Get(it.Path)
			name := path.Base(it.Path)
			if alg := encrypted[it.Path]; alg != "" {
				c := gofastcore.New("")
				c.Source = gofastcore.Source{Name: name, MIME: mt, Size: m.Size}
				c.Warn(gofastcore.SeverityError, gofastcore.WarnChildEncrypted, "encrypted with "+blocksLabel(alg)+"; not opened")
				doc.Children = append(doc.Children, c)
				continue
			}
			if opts.Child == nil {
				doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, nil, m.Size))
				continue
			}
			data, err := a.Read(it.Path, decode, -1)
			if err != nil {
				doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, nil, m.Size))
				continue
			}
			doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, bytes.NewReader(data), int64(len(data))))
		}
	}
done:
	if len(stored.Blocks) > 0 {
		stored.MarkHidden()
		doc.Parts = append(doc.Parts, stored)
	}
}

func hasProperty(props, p string) bool {
	for _, f := range strings.Fields(props) {
		if f == p {
			return true
		}
	}
	return false
}

// epubMetadata maps the package's Dublin Core metadata.
func epubMetadata(p *epub.Package) gofastcore.Metadata {
	var m gofastcore.Metadata
	extra := func(k, v string) {
		if m.Extra == nil {
			m.Extra = map[string]string{}
		}
		if old, ok := m.Extra[k]; ok {
			v = old + "\n" + v
		}
		m.Extra[k] = v
	}
	var ids []string
	for _, dc := range p.DC {
		v := blocks.Clean(dc.Value)
		if v == "" {
			continue
		}
		switch dc.Name {
		case "title":
			if m.Title == "" {
				m.Title = v
			}
		case "creator":
			m.Authors = append(m.Authors, v)
		case "language":
			if m.Language == "" {
				m.Language = v
			}
		case "subject":
			m.Keywords = append(m.Keywords, v)
		case "description":
			extra("description", v)
		case "publisher":
			extra("publisher", v)
		case "date":
			extra("date", v)
			if m.Created.IsZero() {
				m.Created = parseDate(v)
			}
		case "identifier":
			if dc.ID != "" && dc.ID == p.UniqueID {
				ids = append([]string{v}, ids...)
			} else {
				ids = append(ids, v)
			}
		case "contributor", "rights", "source", "type", "format", "relation", "coverage":
			extra(dc.Name, v)
		}
	}
	if len(ids) > 0 {
		extra("identifier", strings.Join(ids, "\n"))
	}
	if v := blocks.Clean(p.Meta["dcterms:modified"]); v != "" {
		m.Modified = parseDate(v)
	}
	if v := blocks.Clean(p.Meta["generator"]); v != "" {
		m.Application = v
	}
	if p.Version != "" {
		extra("epub_version", blocks.Clean(p.Version))
	}
	return m
}

func parseDate(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02T15:04:05", "2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
