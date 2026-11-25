package mimepart

import (
	"bytes"
	"testing"
	"time"
)

// Review finding: SplitAnywhere looks for the end of the line after every
// occurrence of the delimiter, wherever it is, and resumes just after the
// delimiter when the rest of the line is not white space. A long line made
// of nothing but the delimiter therefore costs the length of the line for
// each of its occurrences: time quadratic in the size of a small file.
// The end of the line found once should serve every delimiter on it.
func TestReviewSplitAnywhereQuadratic(t *testing.T) {
	// 1 MiB of "-" with boundary "-": a delimiter "---" every 5 bytes
	// (with its closing "--"), each followed by the rest of a line with no
	// line break. Measured at 0.05 s for 256 KiB, 12.6 s for 4 MiB.
	body := append([]byte("--x\r\nContent-Type: text/html\r\n\r\n"), bytes.Repeat([]byte("-"), 1<<20)...)
	start := time.Now()
	SplitAnywhere(body, "-", 1000)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("SplitAnywhere on %d bytes took %v", len(body), d)
	}
}
