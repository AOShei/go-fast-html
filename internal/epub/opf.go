package epub

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/AOShei/go-fast-core"
)

// MaxXML bounds the size of container.xml, the package document, the
// navigation document, the NCX and encryption.xml.
const MaxXML = 16 << 20

// MaxMeta bounds the length of the text of one metadata element; the rest
// is cut off.
const MaxMeta = 64 << 10

// MaxDC bounds the number of Dublin Core elements read; the rest are left
// out.
const MaxDC = 4096

// MaxEncrypted bounds the number of members encryption.xml may list.
const MaxEncrypted = 1 << 20

// Item is an entry of the manifest.
type Item struct {
	ID, Href, MediaType, Properties string
	// Path is the member name in the archive.
	Path string
}

// SpineItem is an entry of the spine.
type SpineItem struct {
	IDRef  string
	Linear bool
}

// DC is one Dublin Core element of the metadata.
type DC struct {
	Name, Value, ID string
}

// Package is what the package document says.
type Package struct {
	Path     string // of the OPF in the archive
	Version  string
	UniqueID string // the id of the unique identifier
	DC       []DC
	// Meta holds EPUB 3 <meta property> values and EPUB 2 <meta name
	// content> pairs, by property or name.
	Meta     map[string]string
	Manifest []Item
	ByID     map[string]*Item
	ByPath   map[string]*Item
	Spine    []SpineItem
	NCX      string // manifest id of the NCX, from spine@toc
	// MetaCut reports that the text of a metadata element was longer
	// than MaxMeta and was cut there.
	MetaCut bool
	// DCLeftOut counts the Dublin Core elements past MaxDC, which were
	// left out.
	DCLeftOut int
}

var (
	// ErrNoPackage: container.xml does not lead to a package document.
	ErrNoPackage = errors.New("no package document (META-INF/container.xml)")
	// ErrTooManyEntries: encryption.xml lists more members than
	// MaxEncrypted.
	ErrTooManyEntries = errors.New("more entries than the limit")
	// ErrMalformedXML: an XML member is not well-formed enough to be
	// read to its end.
	ErrMalformedXML = errors.New("the XML cannot be read to its end")
)

// readXML reads an XML member of at most MaxXML bytes. A member too large
// for MaxXML or the budget is an error that says so.
func (a *Archive) readXML(name string, budget *gofastcore.DecodeBudget) ([]byte, error) {
	b, err := a.Read(name, budget, MaxXML)
	switch {
	case errors.Is(err, ErrTooLarge):
		return nil, fmt.Errorf("%s: %w of %d MiB", name, err, MaxXML>>20)
	case errors.Is(err, ErrBudget):
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return b, err
}

// Resolve turns an address in the document at base into a member path:
// percent-decoded, relative to base's directory, without its fragment or
// query. It reports false for an address with a scheme or one that leaves
// the archive.
func Resolve(base, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if i := strings.IndexAny(ref, "#?"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" {
		return base, true
	}
	if i := strings.IndexByte(ref, ':'); i >= 0 && !strings.ContainsAny(ref[:i], "/") {
		return "", false
	}
	if u, err := url.PathUnescape(ref); err == nil {
		ref = u
	}
	var p string
	if strings.HasPrefix(ref, "/") {
		p = path.Clean(ref[1:])
	} else {
		p = path.Clean(path.Join(path.Dir(base), ref))
	}
	if p == ".." || strings.HasPrefix(p, "../") || p == "." {
		return "", false
	}
	return p, true
}

func decoder(b []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		return r, nil // read as it is; the bytes are nearly always UTF-8
	}
	return d
}

func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// ReadPackage reads container.xml and the package document it names.
func (a *Archive) ReadPackage(budget *gofastcore.DecodeBudget) (*Package, error) {
	c, err := a.readXML("META-INF/container.xml", budget)
	if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrBudget) {
		return nil, err
	}
	if err != nil {
		return nil, ErrNoPackage
	}
	opf := ""
	d := decoder(c)
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "rootfile" {
			mt := attr(se, "media-type")
			if p := attr(se, "full-path"); p != "" && (mt == "" || mt == "application/oebps-package+xml") {
				opf = strings.TrimPrefix(path.Clean(p), "/")
				break
			}
		}
	}
	if opf == "" {
		return nil, ErrNoPackage
	}
	b, err := a.readXML(opf, budget)
	if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrBudget) {
		return nil, err
	}
	if err != nil {
		return nil, ErrNoPackage
	}
	return parsePackage(opf, b), nil
}

const dcNS = "http://purl.org/dc/elements/1.1/"

func parsePackage(opfPath string, b []byte) *Package {
	p := &Package{Path: opfPath, Meta: map[string]string{}, ByID: map[string]*Item{}, ByPath: map[string]*Item{}}
	d := decoder(b)
	var stack []string
	var text []byte
	textCut := false
	var cur xml.StartElement
	inMeta := false
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			cur = t
			text, textCut = text[:0], false
			switch t.Name.Local {
			case "package":
				p.Version = attr(t, "version")
				p.UniqueID = attr(t, "unique-identifier")
			case "metadata":
				inMeta = true
			case "meta":
				if name := attr(t, "name"); name != "" {
					p.Meta[name] = attr(t, "content")
				}
			case "item":
				it := Item{ID: attr(t, "id"), Href: attr(t, "href"), MediaType: strings.ToLower(strings.TrimSpace(attr(t, "media-type"))), Properties: attr(t, "properties")}
				if path, ok := Resolve(opfPath, it.Href); ok && it.Href != "" {
					it.Path = path
				}
				if len(p.Manifest) < 1<<20 {
					p.Manifest = append(p.Manifest, it)
				}
			case "itemref":
				if len(p.Spine) < 1<<20 {
					p.Spine = append(p.Spine, SpineItem{IDRef: attr(t, "idref"), Linear: !strings.EqualFold(strings.TrimSpace(attr(t, "linear")), "no")})
				}
			case "spine":
				p.NCX = attr(t, "toc")
			}
		case xml.CharData:
			if inMeta {
				var cut bool
				text, cut = appendCapped(text, t, MaxMeta)
				textCut = textCut || cut
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			switch {
			case t.Name.Local == "metadata":
				inMeta = false
			case inMeta && t.Name.Local == "meta" && attr(cur, "property") != "":
				p.Meta[attr(cur, "property")] = string(text)
				p.MetaCut = p.MetaCut || textCut
			case inMeta && (t.Name.Space == dcNS || t.Name.Space == "dc" || t.Name.Space == "") && cur.Name.Local == t.Name.Local && t.Name.Local != "meta":
				if len(p.DC) < MaxDC {
					p.DC = append(p.DC, DC{Name: t.Name.Local, Value: string(text), ID: attr(cur, "id")})
					p.MetaCut = p.MetaCut || textCut
				} else {
					p.DCLeftOut++
				}
			}
			text, textCut = text[:0], false
		}
	}
	for i := range p.Manifest {
		it := &p.Manifest[i]
		if _, dup := p.ByID[it.ID]; !dup && it.ID != "" {
			p.ByID[it.ID] = it
		}
		if _, dup := p.ByPath[it.Path]; !dup && it.Path != "" {
			p.ByPath[it.Path] = it
		}
	}
	return p
}

// Obfuscation algorithms: font mangling, which is not encryption of
// content.
var obfuscation = map[string]bool{
	"http://www.idpf.org/2008/embedding": true,
	"http://ns.adobe.com/pdf/enc#RC":     true,
}

// Encryption reads META-INF/encryption.xml and returns the members that
// are encrypted, with the algorithm, leaving out font obfuscation. When
// there is no encryption.xml it returns nil and no error. When there is
// one that cannot be read in full (larger than MaxXML or the budget,
// listing more than MaxEncrypted members, damaged, or not XML that can be
// read to its end), it returns an error, and which members are encrypted
// is not known.
func (a *Archive) Encryption(budget *gofastcore.DecodeBudget) (map[string]string, error) {
	const name = "META-INF/encryption.xml"
	b, err := a.readXML(name, budget)
	switch {
	case errors.Is(err, ErrMissing):
		return nil, nil
	case errors.Is(err, ErrTooLarge) || errors.Is(err, ErrBudget):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("%s cannot be read: %w", name, err)
	}
	out := map[string]string{}
	d := decoder(b)
	alg := ""
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s cannot be read: %w: %v", name, ErrMalformedXML, err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "EncryptedData":
			alg = ""
		case "EncryptionMethod":
			alg = attr(se, "Algorithm")
		case "CipherReference":
			if obfuscation[alg] {
				continue
			}
			if p, ok := Resolve("", attr(se, "URI")); ok {
				if _, seen := out[p]; !seen && len(out) >= MaxEncrypted {
					return nil, fmt.Errorf("%s lists more than %d members: %w", name, MaxEncrypted, ErrTooManyEntries)
				}
				out[p] = alg
			}
		}
	}
	return out, nil
}

// appendCapped appends s to b, keeping b within limit bytes; it cuts s at
// the start of a UTF-8 sequence, and reports whether it cut.
func appendCapped(b, s []byte, limit int) ([]byte, bool) {
	room := limit - len(b)
	if len(s) <= room {
		return append(b, s...), false
	}
	n := max(room, 0)
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return append(b, s[:n]...), true
}
