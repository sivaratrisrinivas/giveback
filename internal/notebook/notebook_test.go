package notebook

import (
	"image"
	"image/color"
	"os"
	"testing"
)

func TestParseKey(t *testing.T) {
	f, err := os.Open("../../docs/synthetic-notebook.md")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pages, err := ParseKey(f)
	if err != nil {
		t.Fatal(err)
	}
	p1, p2 := pages[1], pages[2]
	if len(p1.Lines) != 10 || len(p2.Lines) != 8 {
		t.Fatalf("lines: page1 %d, page2 %d; want 10, 8", len(p1.Lines), len(p2.Lines))
	}
	if p1.Function != "పద్మ పెళ్ళి" || p1.Date != "12-2-1994" || p2.Date != "8-6-2003" {
		t.Errorf("headings: %q %q / %q", p1.Function, p1.Date, p2.Date)
	}
	checks := []struct {
		got  KeyLine
		amt  string
		gold float64
	}{
		{p1.Lines[0], "516", 0},
		{p1.Lines[2], "", 1}, // 1 కాసు ఉంగరం
		{p1.Lines[4], "1116", 0},
		{p1.Lines[8], "", 0},     // silver plate
		{p2.Lines[0], "1116", 0}, // Rs 1,116/- (the name part has a hyphen too)
		{p2.Lines[4], "", 2},     // 2 కాసులు గొలుసు
	}
	for _, c := range checks {
		if c.got.AmountDigits != c.amt || c.got.Gold != c.gold {
			t.Errorf("line %s %q: amount %q gold %g; want %q %g", c.got.Number, c.got.Written,
				c.got.AmountDigits, c.got.Gold, c.amt, c.gold)
		}
	}
}

func TestCER(t *testing.T) {
	if c := CER("K. Ramana Reddy, Miryalaguda — 116/-", "K.Ramana Reddy, Miryalaguda - 116/-"); c != 0 {
		t.Errorf("punctuation-only difference gave CER %g", c)
	}
	if c := CER("abcd", "abed"); c != 0.25 {
		t.Errorf("CER %g, want 0.25", c)
	}
}

// A sideways photo: a mark at the photo's top-right must end up at the top-left
// once turned 270° clockwise (90° anticlockwise).
func TestRotate(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	src.Set(3, 0, color.White)
	got := Rotate(src, 270)
	if b := got.Bounds(); b.Dx() != 2 || b.Dy() != 4 {
		t.Fatalf("size %v", b)
	}
	if r, _, _, _ := got.At(0, 0).RGBA(); r == 0 {
		t.Error("270: mark not at top-left")
	}
	got = Rotate(src, 90)
	if r, _, _, _ := got.At(1, 3).RGBA(); r == 0 {
		t.Error("90: mark not at bottom-right")
	}
}

// Two "lines" of ink on white paper; the model's box for the first stops well
// short of its end and overlaps the second. The crop must reach the end of the
// ink and must not take in the second line.
func TestCropSnapsToInk(t *testing.T) {
	page := image.NewRGBA(image.Rect(0, 0, 1000, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 1000; x++ {
			page.Set(x, y, color.White)
		}
	}
	ink := color.RGBA{30, 30, 120, 255}
	for x := 100; x < 800; x++ { // line 1: rows 100-120, x 100-800
		for y := 100; y < 120; y++ {
			page.Set(x, y, ink)
		}
	}
	for x := 100; x < 600; x++ { // line 2: rows 200-220
		for y := 200; y < 220; y++ {
			page.Set(x, y, ink)
		}
	}
	boxes := [][4]int{
		{230, 100, 550, 600}, // y 92-220, x 100-600: too short, too tall
		{490, 100, 560, 600},
	}
	crops, errs := cropLines(page, boxes)
	if errs[0] != nil || errs[1] != nil {
		t.Fatal(errs)
	}
	c := crops[0].Bounds()
	if c.Dx() < 700 {
		t.Errorf("line 1 crop is %dpx wide; the ink is 700px", c.Dx())
	}
	inkRows := 0
	for y := c.Min.Y; y < c.Max.Y; y++ {
		if r, _, _, _ := crops[0].At(c.Min.X+300, y).RGBA(); r < 0x8000 {
			inkRows++
		}
	}
	if inkRows != 20 {
		t.Errorf("line 1 crop holds %d rows of ink; want 20 (only line 1)", inkRows)
	}
}

func TestUprightBox(t *testing.T) {
	// A box near the top-right of a photo turned 90° clockwise to read it
	// ends up near the top-left once turned back... checked via 270 here:
	// in page1-rotated the first line is at the photo's right, running down.
	got := uprightBox([4]int{100, 800, 900, 850}, 270)
	want := [4]int{150, 100, 200, 900}
	if got != want {
		t.Errorf("got %v want %v", got, want)
	}
}

// Boxes that drift up the page, until the last sits nearer the line above
// it, still give each line its own picture (seen live on the demo's page 1).
func TestCropFollowsDriftingBoxes(t *testing.T) {
	const W, H = 1000, 600
	page := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			page.Set(x, y, color.White)
		}
	}
	ink := color.RGBA{30, 30, 120, 255}
	widths := []int{300, 700, 450, 600} // tells the lines apart in a crop
	var boxes [][4]int
	for k, w := range widths {
		top := 100 + 100*k
		for y := top; y < top+20; y++ {
			for x := 100; x < 100+w; x++ {
				page.Set(x, y, ink)
			}
		}
		drift := 30 * k // line 4's box is 90 px up: closer to line 3 than to line 4
		boxes = append(boxes, [4]int{(top - drift) * 1000 / H, 100, (top + 20 - drift) * 1000 / H, (100 + w) * 1000 / W})
	}
	crops, errs := cropLines(page, boxes)
	for k, w := range widths {
		if errs[k] != nil {
			t.Fatalf("line %d: %v", k+1, errs[k])
		}
		b := crops[k].Bounds()
		inkW := 0
		for x := b.Min.X; x < b.Max.X; x++ {
			for y := b.Min.Y; y < b.Max.Y; y++ {
				if r, _, _, _ := crops[k].At(x, y).RGBA(); r < 0x8000 {
					inkW++
					break
				}
			}
		}
		if inkW != w {
			t.Errorf("line %d: crop holds ink %d px wide; want %d (its own line only)", k+1, inkW, w)
		}
	}
}
