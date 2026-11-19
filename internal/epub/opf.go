package epub

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/AOShei/go-fast-core"
)

// MaxXML bounds the size of container.xml, the package document, the NCX
// and encryption.xml.
const MaxXML = 16 << 20

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
}

// ErrNoPackage: container.xml does not lead to a package document.
var ErrNoPackage = errors.New("no package document (META-INF/container.xml)")

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
	c, err := a.Read("META-INF/container.xml", budget, MaxXML)
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
	b, err := a.Read(opf, budget, MaxXML)
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
	var text strings.Builder
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
			text.Reset()
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
			if inMeta && text.Len() < 1<<16 {
				text.Write(t)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			switch {
			case t.Name.Local == "metadata":
				inMeta = false
			case inMeta && t.Name.Local == "meta" && attr(cur, "property") != "":
				p.Meta[attr(cur, "property")] = text.String()
			case inMeta && (t.Name.Space == dcNS || t.Name.Space == "dc" || t.Name.Space == "") && cur.Name.Local == t.Name.Local && t.Name.Local != "meta":
				if len(p.DC) < 4096 {
					p.DC = append(p.DC, DC{Name: t.Name.Local, Value: text.String(), ID: attr(cur, "id")})
				}
			}
			text.Reset()
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
// are encrypted, with the algorithm, leaving out font obfuscation.
func (a *Archive) Encryption(budget *gofastcore.DecodeBudget) map[string]string {
	b, err := a.Read("META-INF/encryption.xml", budget, MaxXML)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	d := decoder(b)
	alg := ""
	for {
		tok, err := d.Token()
		if err != nil {
			break
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
			if p, ok := Resolve("", attr(se, "URI")); ok && len(out) < 1<<20 {
				out[p] = alg
			}
		}
	}
	return out
}
