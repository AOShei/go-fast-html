package gofasthtml

import (
	goflag "flag"
	"os"
	"testing"
)

// TestMain overwrites every input buffer given back to the pools, so that
// a document sharing its page's memory fails TestInputReused, the corpus
// identity check and any test holding a document across extractions, every
// time. Benchmarks run without it: it would add a pass over each input.
func TestMain(m *testing.M) {
	goflag.Parse()
	if f := goflag.Lookup("test.bench"); f == nil || f.Value.String() == "" {
		poisonReleased.Store(true)
	}
	os.Exit(m.Run())
}
