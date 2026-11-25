package notebook

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"sort"
)

// Models draw line boxes loosely: they clip a trailing "/-" or take in half of
// the next line. So a box is used only to say roughly where a line is; the crop
// is then snapped to the ink: cut between lines at the emptiest pixel row, and
// widen sideways until the writing stops.

// uprightBox maps a [ymin, xmin, ymax, xmax] box (0–1000) in the photo as sent
// to the same box once the photo is turned `rotation` degrees clockwise.
func uprightBox(b [4]int, rotation int) [4]int {
	ymin, xmin, ymax, xmax := b[0], b[1], b[2], b[3]
	switch rotation % 360 {
	case 90:
		return [4]int{xmin, 1000 - ymax, xmax, 1000 - ymin}
	case 180:
		return [4]int{1000 - ymax, 1000 - xmax, 1000 - ymin, 1000 - xmin}
	case 270:
		return [4]int{1000 - xmax, ymin, 1000 - xmin, ymax}
	}
	return b
}

// inkMask marks pixels clearly darker than the paper. Faint show-through from
// the other side of the sheet is lighter than the threshold and drops out.
type inkMask struct {
	w, h int
	ink  []bool
}

func newInkMask(img image.Image) *inkMask {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	lum := make([]uint8, w*h)
	var hist [256]int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			l := uint8((299*(r>>8) + 587*(g>>8) + 114*(bl>>8)) / 1000)
			lum[y*w+x] = l
			hist[l]++
		}
	}
	// The median brightness is the paper: a page is mostly blank.
	median, seen := 0, 0
	for i, n := range hist {
		seen += n
		if seen >= w*h/2 {
			median = i
			break
		}
	}
	thr := uint8(median * 62 / 100)
	m := &inkMask{w: w, h: h, ink: make([]bool, w*h)}
	for i, l := range lum {
		m.ink[i] = l < thr
	}
	return m
}

func (m *inkMask) rowInk(y, x0, x1 int) int {
	n := 0
	for x := max(x0, 0); x < min(x1, m.w); x++ {
		if m.ink[y*m.w+x] {
			n++
		}
	}
	return n
}

func (m *inkMask) colInk(x, y0, y1 int) int {
	n := 0
	for y := max(y0, 0); y < min(y1, m.h); y++ {
		if m.ink[y*m.w+x] {
			n++
		}
	}
	return n
}

// quietestRow returns the row in [y0, y1) with the least ink across [x0, x1),
// preferring the one nearest the middle when several tie.
func (m *inkMask) quietestRow(y0, y1, x0, x1 int) int {
	y0, y1 = max(y0, 0), min(y1, m.h)
	if y1 <= y0 {
		return max(min(y0, m.h-1), 0)
	}
	mid := (y0 + y1) / 2
	best, bestInk := mid, 1<<30
	for y := y0; y < y1; y++ {
		// Smooth over three rows so one stray dot does not decide the cut.
		n := m.rowInk(y, x0, x1)
		if y > 0 {
			n += m.rowInk(y-1, x0, x1)
		}
		if y+1 < m.h {
			n += m.rowInk(y+1, x0, x1)
		}
		if n < bestInk || (n == bestInk && abs(y-mid) < abs(best-mid)) {
			best, bestInk = y, n
		}
	}
	return best
}

// cropLines cuts every line out of an upright page. boxes are in upright
// coordinates (0–1000). A nil entry means that line could not be cut out.
func cropLines(page image.Image, boxes [][4]int) ([]image.Image, []error) {
	b := page.Bounds()
	W, H := b.Dx(), b.Dy()
	m := newInkMask(page)

	type px struct{ top, left, bottom, right, cy int }
	out := make([]image.Image, len(boxes))
	errs := make([]error, len(boxes))
	var order []int
	pb := make([]px, len(boxes))
	for i, bx := range boxes {
		if bx[0] >= bx[2] || bx[1] >= bx[3] || bx[0] < 0 || bx[1] < 0 || bx[2] > 1000 || bx[3] > 1000 {
			errs[i] = errors.New("bad box")
			continue
		}
		p := px{top: bx[0] * H / 1000, left: bx[1] * W / 1000, bottom: bx[2] * H / 1000, right: bx[3] * W / 1000}
		p.cy = (p.top + p.bottom) / 2
		pb[i] = p
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, c int) bool { return pb[order[a]].cy < pb[order[c]].cy })

	for k, i := range order {
		p := pb[i]
		h := max(p.bottom-p.top, H/100)
		// Top and bottom: the quietest row between this line and its neighbours.
		var top, bottom int
		if k > 0 {
			top = m.quietestRow(pb[order[k-1]].cy, p.cy, p.left, p.right)
		} else {
			top = m.quietestRow(p.cy-h, p.cy-h/3, p.left, p.right)
		}
		if k+1 < len(order) {
			bottom = m.quietestRow(p.cy, pb[order[k+1]].cy, p.left, p.right)
		} else {
			bottom = m.quietestRow(p.cy+h/3, p.cy+h, p.left, p.right)
		}
		if bottom-top < 4 {
			errs[i] = errors.New("line too thin")
			continue
		}
		// Sideways: from the box, keep going while ink continues with gaps no
		// wider than a word space, so a trailing "/-" is never cut off.
		gap := max(W*4/100, 8)
		left, right := p.left, p.right
		for x, last := p.right, p.right; x < W && x-last <= gap; x++ {
			if m.colInk(x, top, bottom) > 0 {
				last, right = x, x
			}
		}
		for x, last := p.left, p.left; x >= 0 && last-x <= gap; x-- {
			if m.colInk(x, top, bottom) > 0 {
				last, left = x, x
			}
		}
		// Then trim blank margins the box may have had.
		for left < right && m.colInk(left, top, bottom) == 0 {
			left++
		}
		for right > left && m.colInk(right, top, bottom) == 0 {
			right--
		}
		pad := max(H/150, 6)
		r := image.Rect(left-2*pad, top, right+2*pad+1, bottom+1).Intersect(image.Rect(0, 0, W, H))
		if r.Dx() < 8 || r.Dy() < 4 {
			errs[i] = errors.New("no ink where the line should be")
			continue
		}
		c := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(c, c.Bounds(), page, b.Min.Add(r.Min), draw.Src)
		out[i] = c
	}
	return out, errs
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Rotate turns an image clockwise by 0, 90, 180 or 270 degrees.
func Rotate(src image.Image, degrees int) image.Image {
	if degrees%360 == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	switch degrees % 360 {
	case 90:
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
	case 180:
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
	case 270:
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
	default:
		return src
	}
	return dst
}

// EncodeJPEG encodes an image at a quality good enough to read handwriting.
func EncodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	return buf.Bytes(), err
}
