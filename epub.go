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
	// and were not read. Font obfuscation is not counted. Also: the
	// chapter may be encrypted, META-INF/encryption.xml not having been
	// read in full, and was not read.
	WarnEncrypted = "epub.encrypted"
	// WarnMimetype: the archive's mimetype member is missing or does not
	// say application/epub+zip; the archive was read as an EPUB all the
	// same.
	WarnMimetype = "epub.mimetype"
)

// StoredManifestItem is the Attrs["stored"] value of an image the
// publication stores and no chapter shows. It is core's
// gofastcore.StoredManifestItem.
const StoredManifestItem = gofastcore.StoredManifestItem

// StoredStylesheet is the Attrs["stored"] value of the text of a style
// element, kept in the stored part. It is core's
// gofastcore.StoredStylesheet.
const StoredStylesheet = gofastcore.StoredStylesheet

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
		if errors.Is(err, epub.ErrTooLarge) || errors.Is(err, epub.ErrBudget) {
			return nil, fmt.Errorf("gofasthtml: %v: %w", err, gofastcore.ErrLimitExceeded)
		}
		return nil, fmt.Errorf("gofasthtml: %v: %w", err, gofastcore.ErrMalformed)
	}
	doc.Metadata = epubMetadata(pkg)
	if pkg.MetaCut {
		doc.Warn(gofastcore.SeverityInfo, WarnMetadataTruncated,
			fmt.Sprintf("the text of a metadata element of the package document was cut at %d KiB", epub.MaxMeta>>10))
	}
	if pkg.DCLeftOut > 0 {
		doc.Warn(gofastcore.SeverityInfo, WarnMetadataTruncated,
			fmt.Sprintf("the package document has more than %d Dublin Core elements; %s left out", epub.MaxDC, count(pkg.DCLeftOut, "was", "were")))
	}
	encrypted, encErr := a.Encryption(decode)
	locked := func(p string) string { return encrypted[p] }
	if encErr != nil {
		// Fail closed: encryption.xml was not read in full, so any
		// member but the ones the standard keeps in the clear may be
		// encrypted, and none is read as text.
		locked = func(p string) string {
			if p == "mimetype" || strings.HasPrefix(p, "META-INF/") || p == pkg.Path {
				return ""
			}
			return unknownEncryption
		}
	}
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
	var tocCut epub.Cut
	for _, it := range pkg.Manifest {
		if hasProperty(it.Properties, "nav") && it.Path != "" && locked(it.Path) == "" {
			b, err := a.Read(it.Path, decode, epub.MaxXML)
			if err == nil {
				navPath = it.Path
				entries, tocCut = epub.Nav(ctx, it.Path, b)
			} else {
				tocLimit(doc, "navigation document", err)
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
	if len(entries) == 0 && ncxPath != "" && locked(ncxPath) == "" {
		if b, err := a.Read(ncxPath, decode, epub.MaxXML); err == nil {
			entries, tocCut = epub.NCX(ncxPath, b)
		} else {
			tocLimit(doc, "NCX", err)
		}
	}
	if tocCut.Entries {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("the table of contents has more than %d entries; the rest were not read", epub.MaxEntries))
	}
	if tocCut.Deep {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("the table of contents has lists nested deeper than %d levels; those were not read", epub.MaxLevel))
	}
	if tocCut.Titles > 0 {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("%s in the table of contents longer than %d KiB, cut there", count(tocCut.Titles, "title is", "titles are"), epub.MaxTitle>>10))
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
				fmt.Sprintf("the publication has more chapters than the limit of %d; %s",
					maxParts, notRead(unread(a, pkg, opts, shown, navPath, ncxPath, t, n-1, func(left int) string {
						if left == 1 {
							return fmt.Sprintf("chapter %d", n)
						}
						return fmt.Sprintf("the %d chapters from chapter %d on", left, n)
					}))))
			cutShort = true
			break
		}
		selected++
		part := gofastcore.Part{Kind: gofastcore.PartChapter, Number: n}
		textWarned := false // the part says the text budget cut it
		it, ok := pkg.ByID[s.IDRef]
		switch {
		case !ok || it.Path == "":
			part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the spine names an item that is not in the manifest")
		case locked(it.Path) == unknownEncryption:
			lockedChapters++
			part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed,
				"META-INF/encryption.xml was not read in full, so whether the chapter is encrypted is not known; it was not read")
			part.Warn(gofastcore.SeverityError, WarnEncrypted, "not known whether encrypted: META-INF/encryption.xml was not read in full")
		case locked(it.Path) != "":
			lockedChapters++
			part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the chapter is encrypted and was not read")
			part.Warn(gofastcore.SeverityError, WarnEncrypted, "encrypted with "+blocksLabel(encrypted[it.Path]))
		default:
			cut, err := e.chapter(ctx, a, pkg, it, opts, text, decode, &part, t, shown, locked)
			if err != nil {
				return nil, err
			}
			textWarned = cut
		}
		if !s.Linear {
			part.Warn(gofastcore.SeverityInfo, WarnNonLinear, "the spine marks this chapter linear=\"no\"")
		}
		if part.Name == "" {
			part.Name = names[n]
		}
		nameCut := false
		if part.Name != "" && !text.Take(len(part.Name)) {
			part.Name, nameCut = "", true
		}
		switch {
		case nameCut && textWarned:
			// One warning for the cap: the one that says the chapter is
			// cut off says so of its name too.
			for i := len(part.Warnings) - 1; i >= 0; i-- {
				if w := &part.Warnings[i]; w.Code == gofastcore.WarnLimitExceeded && w.Message == textCutMessage(part.Kind) {
					w.Message += "; the chapter's name was left out too"
					break
				}
			}
		case nameCut:
			part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				"the limit on the amount of text was reached at the chapter's name; it was left out")
		case text.Spent() && !textWarned:
			part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
				"the limit on the amount of text was reached in the chapter")
		}
		part.Finish()
		doc.Parts = append(doc.Parts, part)
		if text.Spent() {
			cutShort = true
			if rest := unread(a, pkg, opts, shown, navPath, ncxPath, t, n, func(left int) string {
				if left == 1 {
					return "the chapter after it"
				}
				return fmt.Sprintf("the %d chapters after it", left)
			}); len(rest) > 0 {
				doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
					fmt.Sprintf("the limit on the amount of text was reached in chapter %d; %s", n, notRead(rest)))
			}
			break
		}
	}
	if selected > 0 && lockedChapters == selected {
		if encErr != nil {
			return nil, encryptionError(encErr)
		}
		return nil, fmt.Errorf("gofasthtml: every chapter is encrypted (DRM): %w", gofastcore.ErrUnsupported)
	}
	if encErr != nil {
		code, sev := WarnEncrypted, gofastcore.SeverityError
		if errors.Is(encryptionError(encErr), gofastcore.ErrLimitExceeded) {
			code = gofastcore.WarnLimitExceeded
		}
		doc.Warn(sev, code, fmt.Sprintf("%v; which resources are encrypted is not known, so no content document of the publication was read", encErr))
	} else if len(encrypted) > 0 {
		doc.Warn(gofastcore.SeverityError, WarnEncrypted, fmt.Sprintf("%d resources are encrypted and were not read", len(encrypted)))
	}
	if len(doc.Parts) == 0 {
		doc.Parts = append(doc.Parts, emptyChapter())
	}

	// What the publication stores beside its chapters, with the whole
	// document only.
	if opts.Parts == (gofastcore.PartRange{}) && !cutShort {
		e.manifestRest(ctx, a, pkg, opts, text, decode, doc, shown, locked, navPath, ncxPath, t)
	}
	t.finish(doc)
	return doc, nil
}

// unreadItem is a thing not read, for notRead.
type unreadItem struct {
	text string
	many bool
}

// unread lists what reading the chapters no further than n leaves
// unread: the selected chapters after it, as chapters says them, and
// with the whole document the stored part and the embedded documents,
// when the publication has them.
func unread(a *epub.Archive, pkg *epub.Package, opts *gofastcore.Options, shown map[string]bool,
	navPath, ncxPath string, t *tally, n int, chapters func(left int) string) []unreadItem {
	var rest []unreadItem
	if left := selectedAfter(opts.Parts, n, len(pkg.Spine)); left > 0 {
		rest = append(rest, unreadItem{chapters(left), left > 1})
	}
	if opts.Parts == (gofastcore.PartRange{}) {
		images, docs := manifestLeft(a, pkg, opts, shown, navPath, ncxPath, 0)
		if len(t.stored) > 0 || images > 0 {
			rest = append(rest, unreadItem{"the stored part", false})
		}
		if docs > 0 {
			rest = append(rest, unreadItem{"the " + count(docs, "embedded document", "embedded documents"), docs > 1})
		}
	}
	return rest
}

// notRead says that the things listed were not read.
func notRead(rest []unreadItem) string {
	switch len(rest) {
	case 0:
		return "nothing else was to be read"
	case 1:
		if rest[0].many {
			return rest[0].text + " were not read"
		}
		return rest[0].text + " was not read"
	}
	var s []string
	for _, r := range rest {
		s = append(s, r.text)
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1] + " were not read"
}

// selectedAfter counts the chapters past n, of the spine's last, that r
// selects.
func selectedAfter(r gofastcore.PartRange, n, last int) int {
	c := 0
	for i := n + 1; i <= last; i++ {
		if r.Contains(i) {
			c++
		}
	}
	return c
}

// unknownEncryption stands for the algorithm of a member that may be
// encrypted: META-INF/encryption.xml was not read in full.
const unknownEncryption = "\x00unknown"

// encryptionError is the error for a publication none of whose selected
// chapters was read because encryption.xml could not be read in full.
func encryptionError(err error) error {
	if errors.Is(err, epub.ErrTooLarge) || errors.Is(err, epub.ErrBudget) || errors.Is(err, epub.ErrTooManyEntries) {
		return fmt.Errorf("gofasthtml: %v; which chapters are encrypted is not known, so none was read: %w", err, gofastcore.ErrLimitExceeded)
	}
	return fmt.Errorf("gofasthtml: %v; which chapters are encrypted is not known, so none was read: %w", err, gofastcore.ErrMalformed)
}

// tocLimit warns that a table of contents document was not read for a
// limit.
func tocLimit(doc *gofastcore.Document, what string, err error) {
	switch {
	case errors.Is(err, epub.ErrTooLarge):
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("the %s is larger than %d MiB and was not read; the outline has no entries from it", what, epub.MaxXML>>20))
	case errors.Is(err, epub.ErrBudget):
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			fmt.Sprintf("the limit on decoded bytes was reached at the %s, which was not read; the outline has no entries from it", what))
	}
}

func emptyChapter() gofastcore.Part {
	p := gofastcore.Part{Kind: gofastcore.PartChapter, Number: 1}
	p.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "the publication has no chapters")
	p.Finish()
	return p
}

// chapter reads one content document of the spine into part.
func (e *Extractor) chapter(ctx context.Context, a *epub.Archive, pkg *epub.Package, it *epub.Item, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget, part *gofastcore.Part, t *tally, shown map[string]bool,
	locked func(string) string) (textCut bool, err error) {
	b, err := a.Read(it.Path, decode, -1)
	if err != nil {
		if errors.Is(err, epub.ErrBudget) {
			part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the limit on decoded bytes was reached; the chapter was not read")
		} else {
			part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the chapter could not be read from the archive")
		}
		return false, nil
	}
	overBudget := 0 // images whose data passed the limit on decoded bytes
	encImages := 0  // images encryption.xml lists, whose data is not read
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
				if locked(p) != "" {
					encImages++
					return nil, false
				}
				d, err := a.Read(p, decode, -1)
				if errors.Is(err, epub.ErrBudget) {
					overBudget++
				}
				return d, err == nil
			}}, true
		},
		internal: func(ref string) bool {
			p, ok := epub.Resolve(it.Path, ref)
			return ok && a.Has(p)
		},
		noDataElsewhere: func() int { return overBudget + encImages },
	}
	res, err := e.page(ctx, b, "html", pc, part, t)
	if err != nil {
		return false, err
	}
	if overBudget > 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; "+count(overBudget, "image has", "images have")+" no data")
	}
	if encImages > 0 {
		part.Warn(gofastcore.SeverityError, WarnEncrypted,
			count(encImages, "image the chapter shows is", "images the chapter shows are")+" encrypted; their data was not read")
	}
	if res != nil {
		part.Name = res.Meta.Title
	}
	return res != nil && res.Cut, nil
}

// inSpine reports the members the spine reads as chapters.
func inSpine(pkg *epub.Package) map[string]bool {
	m := map[string]bool{}
	for _, s := range pkg.Spine {
		if it, ok := pkg.ByID[s.IDRef]; ok {
			m[it.Path] = true
		}
	}
	return m
}

// Kinds of manifest items manifestRest lists.
const (
	restNone  = iota
	restImage // an image for the stored part
	restChild // an embedded document
)

// restKind says how manifestRest lists a manifest item.
func restKind(a *epub.Archive, it *epub.Item, opts *gofastcore.Options, spine, shown map[string]bool, navPath, ncxPath string) int {
	if it.Path == "" || spine[it.Path] || it.Path == navPath || it.Path == ncxPath || !a.Has(it.Path) {
		return restNone
	}
	switch mt := it.MediaType; {
	case strings.HasPrefix(mt, "image/"):
		if shown[it.Path] || opts.Images == gofastcore.ImagesNone {
			return restNone
		}
		return restImage
	case mt == "text/css":
		return restNone // styling, not evaluated
	}
	return restChild
}

// manifestLeft counts the images and embedded documents manifestRest
// would list from manifest item from on.
func manifestLeft(a *epub.Archive, pkg *epub.Package, opts *gofastcore.Options, shown map[string]bool, navPath, ncxPath string, from int) (images, docs int) {
	spine := inSpine(pkg)
	for i := from; i < len(pkg.Manifest); i++ {
		switch restKind(a, &pkg.Manifest[i], opts, spine, shown, navPath, ncxPath) {
		case restImage:
			images++
		case restChild:
			docs++
		}
	}
	return images, docs
}

// manifestRest lists what the manifest holds besides the spine: images
// no chapter shows, in a stored part, and other documents as embedded
// documents.
func (e *Extractor) manifestRest(ctx context.Context, a *epub.Archive, pkg *epub.Package, opts *gofastcore.Options,
	text *gofastcore.TextBudget, decode *gofastcore.DecodeBudget, doc *gofastcore.Document, shown map[string]bool,
	locked func(string) string, navPath, ncxPath string, t *tally) {
	spine := inSpine(pkg)
	last := len(pkg.Spine)
	if n := len(doc.Parts); n > 0 {
		last = max(last, doc.Parts[n-1].Number)
	}
	// The scripts and styles of the chapters come first.
	stored := gofastcore.Part{Kind: gofastcore.PartStored, Number: last + 1, Blocks: t.stored}
	// Images without data, by cause: not readable, the decode budget,
	// encrypted, possibly encrypted.
	noData, overBudget, encrypted, maybeEncrypted := 0, 0, 0, 0
	childOverBudget := 0 // embedded documents past the decode budget
	cutAt := -1          // the manifest item the text budget stopped at
items:
	for i := range pkg.Manifest {
		it := &pkg.Manifest[i]
		kind := restKind(a, it, opts, spine, shown, navPath, ncxPath)
		mt := it.MediaType
		switch kind {
		case restNone:
			continue
		case restImage:
			img := &gofastcore.Image{ID: it.Path, MIME: mt}
			var cause *int // why the image has no data, counted once it is listed
			if opts.Images == gofastcore.ImagesData {
				switch alg := locked(it.Path); alg {
				case "":
					d, err := a.Read(it.Path, decode, -1)
					switch {
					case err == nil:
						img.Data = d
					case errors.Is(err, epub.ErrBudget):
						cause = &overBudget
					default:
						cause = &noData
					}
				case unknownEncryption:
					cause = &maybeEncrypted
				default:
					cause = &encrypted
				}
			}
			b := gofastcore.Block{Kind: gofastcore.KindImage, Image: img,
				Attrs: map[string]string{gofastcore.AttrStored: StoredManifestItem, gofastcore.AttrName: it.Path}}
			if hasProperty(it.Properties, "cover-image") || pkg.Meta["cover"] == it.ID {
				b.Text = "cover"
			}
			if !text.Take(gofastcore.BlockCost + len(it.Path)*2 + len(img.Data)) {
				cutAt = i
				break items
			}
			stored.Blocks = append(stored.Blocks, b)
			if cause != nil {
				*cause++
			}
		default:
			if strings.Contains(mt, "javascript") || strings.Contains(mt, "ecmascript") {
				t.active++
			}
			if err := ctx.Err(); err != nil {
				return
			}
			m, _ := a.Get(it.Path)
			name := path.Base(it.Path)
			if alg := locked(it.Path); alg != "" {
				c := gofastcore.New("")
				c.Source = gofastcore.Source{Name: name, MIME: mt, Size: m.Size}
				if alg == unknownEncryption {
					c.Warn(gofastcore.SeverityError, gofastcore.WarnChildEncrypted,
						"META-INF/encryption.xml was not read in full, so whether it is encrypted is not known; not opened")
				} else {
					c.Warn(gofastcore.SeverityError, gofastcore.WarnChildEncrypted, "encrypted with "+blocksLabel(alg)+"; not opened")
				}
				doc.Children = append(doc.Children, c)
				continue
			}
			if opts.Child == nil {
				doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, nil, m.Size))
				continue
			}
			data, err := a.Read(it.Path, decode, -1)
			if err != nil {
				if errors.Is(err, epub.ErrBudget) {
					childOverBudget++
				}
				doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, nil, m.Size))
				continue
			}
			doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mt, bytes.NewReader(data), int64(len(data))))
		}
	}
	if noData > 0 {
		stored.Warn(gofastcore.SeverityWarning, gofastcore.WarnImageUnsupportedEncoding,
			fmt.Sprintf("%d images could not be decoded or read; their blocks have no data", noData))
	}
	if overBudget > 0 {
		stored.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; "+count(overBudget, "image has", "images have")+" no data")
	}
	if encrypted > 0 {
		stored.Warn(gofastcore.SeverityError, WarnEncrypted,
			count(encrypted, "image is", "images are")+" encrypted; their data was not read")
	}
	if maybeEncrypted > 0 {
		stored.Warn(gofastcore.SeverityError, WarnEncrypted,
			"META-INF/encryption.xml was not read in full, so whether "+count(maybeEncrypted, "image is", "images are")+" encrypted is not known; their data was not read")
	}
	if childOverBudget > 0 {
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on decoded bytes was reached; "+count(childOverBudget, "embedded document was", "embedded documents were")+" not read")
	}
	if cutAt >= 0 {
		images, docs := manifestLeft(a, pkg, opts, shown, navPath, ncxPath, cutAt)
		if len(stored.Blocks) > 0 {
			stored.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the limit on the amount of text was reached; the stored part is cut off there")
		}
		var left []unreadItem
		if images > 0 {
			left = append(left, unreadItem{count(images, "image", "images"), images > 1})
		}
		if docs > 0 {
			left = append(left, unreadItem{count(docs, "embedded document", "embedded documents"), docs > 1})
		}
		doc.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded,
			"the limit on the amount of text was reached in the manifest's items besides the chapters; "+notRead(left))
	}
	if len(stored.Blocks) > 0 {
		stored.Finish()
		doc.Parts = append(doc.Parts, stored)
	} else {
		// No stored part to carry what was found: the document does.
		doc.Warnings = append(doc.Warnings, stored.Warnings...)
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
