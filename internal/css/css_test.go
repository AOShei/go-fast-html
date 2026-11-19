package css

import "testing"

func TestParse(t *testing.T) {
	st := Parse("DISPLAY: None; color:#FFF; background: url(a;b.png) rgb(255, 255, 255) no-repeat; font-size: 0")
	if st.Display != "none" || !st.ColorSet || st.Color != (RGBA{255, 255, 255, 1}) || !st.BgSet || st.Background != (RGBA{255, 255, 255, 1}) {
		t.Errorf("got %+v", st)
	}
	if !st.FontSize.Set || st.FontSize.Pt != 0 {
		t.Errorf("font size %+v", st.FontSize)
	}
	if st := Parse("display:none !important; display:block"); st.Display != "none" {
		t.Errorf("important lost: %+v", st)
	}
	if st := Parse("display:none; /* x; */ display:block"); st.Display != "block" {
		t.Errorf("last wins: %+v", st)
	}
	if st := Parse("position:absolute; left:-9999px"); !st.OffPage {
		t.Error("off page not found")
	}
	if st := Parse("text-indent:-100em"); !st.OffPage {
		t.Error("text indent not found")
	}
	if st := Parse("width:1px;height:1px;overflow:hidden"); !st.Clipped {
		t.Error("clip not found")
	}
	if st := Parse("clip: rect(0 0 0 0)"); !st.Clipped {
		t.Error("rect clip not found")
	}
	if st := Parse("clip: rect(0, 100px, 20px, 0)"); st.Clipped {
		t.Error("visible clip flagged")
	}
	if st := Parse("opacity: 0.0"); !st.OpacitySet || st.Opacity != 0 {
		t.Errorf("opacity %+v", st)
	}
	if st := Parse("font: italic 1px/2 serif"); st.FontSize.Pt != 0.75 {
		t.Errorf("font shorthand %+v", st.FontSize)
	}
	if st := Parse("font-size: 50%"); !st.FontSize.Relative || st.FontSize.Factor != 0.5 {
		t.Errorf("relative %+v", st.FontSize)
	}
}

func TestColor(t *testing.T) {
	for _, c := range []struct {
		in   string
		want RGBA
		ok   bool
	}{
		{"white", RGBA{255, 255, 255, 1}, true},
		{"Transparent", RGBA{}, true},
		{"#abc", RGBA{0xAA, 0xBB, 0xCC, 1}, true},
		{"#11223300", RGBA{0x11, 0x22, 0x33, 0}, true},
		{"rgb(10 20 30 / 50%)", RGBA{10, 20, 30, 0.5}, true},
		{"rgba(100%,0%,0%,0)", RGBA{255, 0, 0, 0}, true},
		{"hsl(0, 0%, 100%)", RGBA{255, 255, 255, 1}, true},
		{"hsl(120deg 100% 25%)", RGBA{0, 128, 0, 1}, true},
		{"currentcolor", RGBA{}, false},
		{"#12", RGBA{}, false},
	} {
		got, ok := ParseColor(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseColor(%q) = %+v, %v", c.in, got, ok)
		}
	}
}

// Run with -fuzzminimizetime=2s: see FuzzExtract in the root package.
func FuzzParse(f *testing.F) {
	f.Add("display:none;color:rgb(1,2,3)")
	f.Add("background: hsl(1 2% 3% / .5) url('a;b')")
	f.Fuzz(func(t *testing.T, s string) {
		st := Parse(s)
		if st.Opacity != st.Opacity && st.OpacitySet {
			t.Fatal("NaN opacity")
		}
		ParseColor(s)
	})
}
