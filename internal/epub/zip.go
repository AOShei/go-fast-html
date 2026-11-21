// Package epub reads the container of an EPUB 2 or 3 publication: the zip
// archive, META-INF/container.xml, the package document (OPF) with its
// metadata, manifest and spine, the navigation (EPUB 3 nav document or
// EPUB 2 NCX) and META-INF/encryption.xml. The content documents are read
// by the caller, with the HTML path of this library.
package epub

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/AOShei/go-fast-core"
)

var (
	// ErrNotZip: the input is not a zip archive, and no member could be
	// found in it either.
	ErrNotZip = errors.New("not a zip archive")
	// ErrTooManyMembers: the archive has more members than allowed.
	ErrTooManyMembers = errors.New("the archive has more members than the limit")
	// ErrBudget: reading a member would pass the limit on decoded bytes.
	ErrBudget = errors.New("the limit on decoded bytes was reached")
	// ErrTooLarge: a member is larger than the bound Read was given.
	ErrTooLarge = errors.New("the member is larger than the limit")
	// ErrMissing: no member has the name.
	ErrMissing = errors.New("no such member")
)

// Member is one file of the archive.
type Member struct {
	Name string
	// Size is the uncompressed size the archive declares; it is not
	// trusted for reading.
	Size int64
	open func() (io.ReadCloser, error)
}

// Archive is an opened zip archive.
type Archive struct {
	Members []*Member
	byName  map[string]*Member
	// Repaired reports that the central directory could not be read and
	// the members were found by their local headers.
	Repaired bool
	// Duplicates lists names that occur more than once; the first is
	// used.
	Duplicates []string
}

// Open reads the directory of the archive src. maxMembers bounds the
// number of members; negative means no bound.
func Open(src []byte, maxMembers int) (*Archive, error) {
	a := &Archive{byName: map[string]*Member{}}
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err == nil {
		if maxMembers >= 0 && len(zr.File) > maxMembers {
			return nil, ErrTooManyMembers
		}
		for _, f := range zr.File {
			if f.Mode().IsDir() {
				continue
			}
			a.add(&Member{Name: f.Name, Size: int64(min(f.UncompressedSize64, 1<<62)), open: f.Open})
		}
		return a, nil
	}
	if err := a.scan(src, maxMembers); err != nil {
		return nil, err
	}
	if len(a.Members) == 0 {
		return nil, ErrNotZip
	}
	a.Repaired = true
	return a, nil
}

func (a *Archive) add(m *Member) {
	if _, dup := a.byName[m.Name]; dup {
		a.Duplicates = append(a.Duplicates, m.Name)
		return
	}
	a.byName[m.Name] = m
	a.Members = append(a.Members, m)
}

// scan finds members by their local file headers, for an archive whose
// central directory is missing or damaged (a file cut short). A member
// compressed with deflate is read to the end of its stream; a stored one
// needs its size in the header.
func (a *Archive) scan(src []byte, maxMembers int) error {
	sig := []byte("PK\x03\x04")
	n := 0
	for i := bytes.Index(src, sig); i >= 0; {
		if maxMembers >= 0 && n >= maxMembers {
			return ErrTooManyMembers
		}
		next := i + 4
		if i+30 <= len(src) {
			h := src[i:]
			flags := binary.LittleEndian.Uint16(h[6:])
			method := binary.LittleEndian.Uint16(h[8:])
			csize := int64(binary.LittleEndian.Uint32(h[18:]))
			usize := int64(binary.LittleEndian.Uint32(h[22:]))
			nameLen := int(binary.LittleEndian.Uint16(h[26:]))
			extraLen := int(binary.LittleEndian.Uint16(h[28:]))
			data := i + 30 + nameLen + extraLen
			if data <= len(src) && flags&1 == 0 {
				name := string(src[i+30 : i+30+nameLen])
				end := int64(len(src))
				known := flags&8 == 0 || csize > 0
				if known && int64(data)+csize <= end {
					end = int64(data) + csize
					next = int(end)
				}
				body := src[data:end]
				switch {
				case method == 8:
					a.add(&Member{Name: name, Size: usize, open: func() (io.ReadCloser, error) {
						return flate.NewReader(bytes.NewReader(body)), nil
					}})
					n++
				case method == 0 && known:
					a.add(&Member{Name: name, Size: usize, open: func() (io.ReadCloser, error) {
						return io.NopCloser(bytes.NewReader(body)), nil
					}})
					n++
				}
			}
		}
		k := bytes.Index(src[next:], sig)
		if k < 0 {
			break
		}
		i = next + k
	}
	return nil
}

// Has reports whether the archive has a member of that name.
func (a *Archive) Has(name string) bool { _, ok := a.byName[name]; return ok }

// Get returns the member of that name.
func (a *Archive) Get(name string) (*Member, bool) {
	m, ok := a.byName[name]
	return m, ok
}

// Read decompresses the named member, taking what it reads from budget,
// and reads no more than limit bytes (negative: no limit beyond the
// budget). A member larger than what is left of budget is ErrBudget; one
// larger than limit, with more of budget left, is ErrTooLarge.
func (a *Archive) Read(name string, budget *gofastcore.DecodeBudget, limit int64) ([]byte, error) {
	m, ok := a.byName[name]
	if !ok {
		return nil, ErrMissing
	}
	rc, err := m.open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	left := max(budget.Left(), 0)
	most := left
	if limit >= 0 {
		most = min(most, limit)
	}
	b, err := io.ReadAll(io.LimitReader(rc, most+1))
	if int64(len(b)) > most {
		budget.Take(most)
		if most < left {
			return nil, ErrTooLarge
		}
		return nil, ErrBudget
	}
	budget.Take(int64(len(b)))
	if err != nil {
		return b, fmt.Errorf("%s: %w", name, err)
	}
	return b, nil
}
