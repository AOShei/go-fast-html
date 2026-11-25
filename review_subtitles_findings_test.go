package gofasthtml

// Review findings on the subtitle readers: each test fails today and
// passes once its defect is fixed.

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

// reviewWithin extracts src as format and fails when that takes longer
// than limit (the work is left running in the background; the inputs are
// sized so that it ends within seconds).
func reviewWithin(t *testing.T, format, src string, limit time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := (&Extractor{Format: format}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), &gofastcore.Options{})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%d bytes in %v", len(src), time.Since(start))
	case <-time.After(limit):
		t.Fatalf("%d bytes of %s input not read within %v", len(src), format, limit)
	}
}

// A WebVTT cue of "<" with no ">" after them: each "<" searches the rest
// of the cue for ">" (subtitle.go vttText), so a 1 MiB cue takes seconds
// and an 8 MiB one minutes.
func TestReviewSubtitlesVTTUnclosedTagQuadratic(t *testing.T) {
	reviewWithin(t, "vtt", "WEBVTT\n\n00:01.000 --> 00:02.000\n"+strings.Repeat("<", 1<<20)+"\n", time.Second)
}

// A SubRip cue of "{\" with no "}", or of "<font " with no ">": each
// searches the rest of the cue (subtitle.go srtTag).
func TestReviewSubtitlesSRTUnclosedOverrideQuadratic(t *testing.T) {
	t.Run("override", func(t *testing.T) {
		reviewWithin(t, "srt", "1\n00:00:01,000 --> 00:00:02,000\n"+strings.Repeat("{\\", 1<<19)+"\n", time.Second)
	})
	t.Run("font", func(t *testing.T) {
		reviewWithin(t, "srt", "1\n00:00:01,000 --> 00:00:02,000\n"+strings.Repeat("<font ", 3<<17)+"\n", time.Second)
	})
}

// WebVTT header lines that are not "Key: value": each is appended to
// Header.Text with +=, which copies all of it (subtitle.go VTT).
func TestReviewSubtitlesVTTHeaderTextQuadratic(t *testing.T) {
	reviewWithin(t, "vtt", "WEBVTT\n"+strings.Repeat("xxxxxxxx\n", 80000)+"\n00:01.000 --> 00:02.000\nx\n", time.Second)
}

// WebVTT header lines "Key: value" with one key: each value is joined to
// the 64 KiB already held by a new string of old+"\n"+v before the cut
// (subtitles.go vttMetadata set), 64 KiB copied per five bytes of input.
// It is measured by the bytes allocated, not by time, which a busy test
// machine makes unreliable: copying 64 KiB a line allocates 26 GB here.
func TestReviewSubtitlesVTTHeaderFieldsCopy(t *testing.T) {
	src := "WEBVTT\n" + strings.Repeat("a: b\n", 400000) + "\n00:01.000 --> 00:02.000\nx\n"
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := (&Extractor{Format: "vtt"}).Extract(context.Background(), strings.NewReader(src), int64(len(src)), &gofastcore.Options{}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 256<<20 {
		t.Fatalf("%d bytes of input allocated %d bytes", len(src), n)
	}
}

// Many distinct voices in one cue: each <v> is looked up in the list of
// those seen with a linear scan (subtitle.go vttText, contains).
func TestReviewSubtitlesVTTVoicesQuadratic(t *testing.T) {
	var sb strings.Builder
	for i := range 100000 {
		sb.WriteString("<v ")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(">")
	}
	reviewWithin(t, "vtt", "WEBVTT\n\n00:01.000 --> 00:02.000\n"+sb.String()+"x\n", time.Second)
}

// WebVTT cue tags nest without bound: 100,000 nested <b> give 100,000
// spans over one character, where HTML stops at Extractor.MaxDepth.
func TestReviewSubtitlesVTTNestingUnbounded(t *testing.T) {
	src := "WEBVTT\n\n00:01.000 --> 00:02.000\n" + strings.Repeat("<b>", 100000) + "x\n"
	doc := subExtract(t, "vtt", src, &gofastcore.Options{Spans: true})
	if n := len(doc.Parts[0].Blocks[0].Spans); n > DefaultMaxDepth {
		t.Errorf("%d spans for 3 bytes of text; nesting is not bounded (MaxDepth %d)", n, DefaultMaxDepth)
	}
}

// In WebVTT only an empty line ends a block (the spec's "collect a
// WebVTT block": "if line is the empty string, break"); a line of spaces
// is a line of the cue. Here it splits the cue, and "B" becomes a block
// that is not a cue, with input.repaired.
func TestReviewSubtitlesVTTSpaceLineInCue(t *testing.T) {
	doc := subExtract(t, "vtt", "WEBVTT\n\n00:01.000 --> 00:02.000\nA\n   \nB\n", nil)
	if bl := doc.Parts[0].Blocks; len(bl) != 1 || !strings.Contains(bl[0].Text, "B") {
		t.Errorf("one cue A..B expected, got %q\n%s", briefs(doc.Parts[0]), warnCodes(doc))
	}
}

// The same rule in the header: a line of spaces does not end it, so the
// header line after it is a header line, not a block of the body.
func TestReviewSubtitlesVTTSpaceLineInHeader(t *testing.T) {
	doc := subExtract(t, "vtt", "WEBVTT\nKind: captions\n \nLanguage: en\n\n00:01.000 --> 00:02.000\nx\n", nil)
	if bl := doc.Parts[0].Blocks; len(bl) != 1 || doc.Metadata.Language != "en" {
		t.Errorf("one cue and Language en expected, got %q, Language %q\n%s", briefs(doc.Parts[0]), doc.Metadata.Language, warnCodes(doc))
	}
}

// The Blender film subtitles (dev/srt/sintel_fr.srt and others) begin
// "\r\n" then a byte order mark, which is U+FEFF in the text, before the
// first cue number. The number line, U+FEFF then "1", is not taken for a number
// (isNumber trims only white space): it becomes a paragraph of text
// before the first cue, with input.repaired, and cue 1 has no anchor.
func TestReviewSubtitlesSRTNumberAfterMidBOM(t *testing.T) {
	src := "\r\n" + string(rune(0xFEFF)) + "1\r\n00:01:47,250 --> 00:01:50,500\r\nCette lame\r\n\r\n2\r\n00:01:51,800 --> 00:01:55,800\r\nElle a\r\n"
	doc := subExtract(t, "srt", src, nil)
	bl := doc.Parts[0].Blocks
	if len(bl) != 2 || bl[0].Attrs["start"] == "" || !strings.Contains(bl[0].Loc.Anchor, "1") {
		t.Errorf("two cues, the first #1, expected; got %q\n%s", briefs(doc.Parts[0]), warnCodes(doc))
	}
}
