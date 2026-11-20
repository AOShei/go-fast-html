package css

import "testing"

func TestParse(t *testing.T) {
	st := Parse("DISPLAY: None; color: Transparent; font-size: 0; left: -9999px")
	if st.Display != "none" || !st.ColorSet || st.ColorAlpha != 0 {
		t.Errorf("got %+v", st)
	}
	if st := Parse("display:none !important; display:block"); st.Display != "none" {
		t.Errorf("important lost: %+v", st)
	}
	if st := Parse("display:none; /* x; */ display:block"); st.Display != "block" {
		t.Errorf("last wins: %+v", st)
	}
	for _, c := range []struct {
		in   string
		zero bool
	}{{"opacity: 0", true}, {"opacity: 0.0", true}, {"opacity: 0%", true}, {"opacity: -1", true},
		{"opacity: 0.01", false}, {"opacity: 1", false}} {
		st := Parse(c.in)
		if !st.OpacitySet || (st.Opacity == 0) != c.zero {
			t.Errorf("%q: %+v", c.in, st)
		}
	}
	if st := Parse("color: inherit"); st.ColorSet {
		t.Errorf("inherit: %+v", st)
	}
	if st := Parse("white-space: PRE-wrap"); st.WhiteSpace != "pre-wrap" {
		t.Errorf("white-space: %+v", st)
	}
}

func TestAlpha(t *testing.T) {
	for _, c := range []struct {
		in string
		a  float64
		ok bool
	}{
		{"transparent", 0, true},
		{"white", 1, true},
		{"CanvasText", 1, true},
		{"#abc", 1, true},
		{"#0000", 0, true},
		{"#11223300", 0, true},
		{"#112233ff", 1, true},
		{"rgba(0, 0, 0, 0)", 0, true},
		{"rgb(10 20 30 / 0%)", 0, true},
		{"rgb(10 20 30 / 1%)", 0.01, true},
		{"hsl(0 0% 0%)", 1, true},
		{"oklch(0.5 0.1 20 / 0)", 0, true},
		{"currentcolor", 0, false},
		{"color-mix(in srgb, red, blue)", 0, false},
		{"#12", 0, false},
		{"bogus", 0, false},
	} {
		a, ok := Alpha(c.in)
		if a != c.a || ok != c.ok {
			t.Errorf("Alpha(%q) = %v, %v", c.in, a, ok)
		}
	}
}

// Run with -fuzzminimizetime=2s: see FuzzExtract in the root package.
func FuzzParse(f *testing.F) {
	f.Add("display:none;color:rgb(1,2,3)")
	f.Add("color: hsl(1 2% 3% / .5); opacity: 0")
	f.Fuzz(func(t *testing.T, s string) {
		st := Parse(s)
		if st.Opacity < 0 || st.Opacity > 1 || st.ColorAlpha < 0 || st.ColorAlpha > 1 {
			t.Fatalf("out of range: %+v", st)
		}
		Alpha(s)
	})
}
