// Package epub reads the container of an EPUB 2 or 3 publication: the zip
// archive, META-INF/container.xml, the package document (OPF) with its
// metadata, manifest and spine, the navigation (EPUB 3 nav document or
// EPUB 2 NCX) and META-INF/encryption.xml. The content documents are read
// by the caller, with the HTML path of this library.
package epub

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/ziprecover"
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
	// the members were found by their local headers; Recovery says what
	// was found.
	Repaired bool
	Recovery *ziprecover.Recovery
	// Duplicates lists names that occur more than once; the first is
	// used.
	Duplicates []string
}

// Open reads the archive src with go-fast-core's ziprecover: its
// directory, or for a file cut short, the members found by their own
// headers, in order from the first byte. maxMembers bounds the number of
// members; negative means no bound. maxDecoded bounds what recovery may
// decompress to find where members end (ErrBudget past it); it is not
// taken from the budget the members are read with.
func Open(src []byte, maxMembers int, maxDecoded int64) (*Archive, error) {
	a := &Archive{byName: map[string]*Member{}}
	lim := ziprecover.Limits{MaxMembers: max(maxMembers, 0), MaxDecodedBytes: max(maxDecoded, 1)}
	zr, rec, err := ziprecover.Open(bytes.NewReader(src), int64(len(src)), lim)
	switch {
	case errors.Is(err, ziprecover.ErrTooManyMembers):
		return nil, ErrTooManyMembers
	case errors.Is(err, ziprecover.ErrBudget):
		return nil, ErrBudget
	case err != nil:
		return nil, ErrNotZip
	}
	if maxMembers >= 0 && len(zr.File) > maxMembers {
		return nil, ErrTooManyMembers
	}
	for _, f := range zr.File {
		if f.Mode().IsDir() {
			continue
		}
		a.add(&Member{Name: f.Name, Size: int64(min(f.UncompressedSize64, 1<<62)), open: f.Open})
	}
	a.Recovery = rec
	a.Repaired = rec != nil
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
