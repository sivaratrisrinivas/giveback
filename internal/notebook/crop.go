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

// px is a line's box in pixels, with its centre row.
type px struct{ top, left, bottom, right, cy int }

// cropLines cuts every line out of an upright page. boxes are in upright
// coordinates (0–1000). A nil entry means that line could not be cut out.
func cropLines(page image.Image, boxes [][4]int) ([]image.Image, []error) {
	b := page.Bounds()
	W, H := b.Dx(), b.Dy()
	m := newInkMask(page)

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
	alignToWriting(m, pb, order)

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

// alignToWriting moves each box's centre onto the row of writing it stands
// for. Model boxes drift: on one page every box from line 8 down sat about a
// line too high, so line 9's picture showed line 10. The rows of writing are
// found from the ink and matched to the boxes top to bottom, one each, but
// only when there are exactly as many rows as boxes and no box moves by more
// than about a line and a half. Otherwise the boxes are left as they were.
func alignToWriting(m *inkMask, pb []px, order []int) {
	if len(order) < 2 {
		return
	}
	x0, x1 := m.w, 0
	for _, i := range order {
		x0, x1 = min(x0, pb[i].left), max(x1, pb[i].right)
	}
	rows := m.writingRows(x0, x1)
	n := len(order)
	// Typical distance between neighbouring boxes: how far one may move.
	var steps []int
	for k := 1; k < n; k++ {
		steps = append(steps, pb[order[k]].cy-pb[order[k-1]].cy)
	}
	sort.Ints(steps)
	step := steps[len(steps)/2]
	if step <= 0 {
		return
	}
	// The rows of writing where the boxes are, allowing for drift: from half
	// a line above the first box to a line and a half below the last. A
	// heading above is left out; a stray mark that made a row of its own
	// gives one row too many, and then nothing is moved.
	lo, hi := pb[order[0]].cy-step/2, pb[order[n-1]].cy+step*3/2
	var near []span
	for _, r := range rows {
		if r.cy() >= lo && r.cy() <= hi {
			near = append(near, r)
		}
	}
	if len(near) != n {
		return
	}
	for k, i := range order {
		if abs(near[k].cy()-pb[i].cy) > step*3/2 {
			return
		}
	}
	for k, i := range order {
		pb[i].cy = near[k].cy()
	}
}

type span struct{ top, bottom int }

func (s span) cy() int { return (s.top + s.bottom) / 2 }

// writingRows finds the rows of handwriting between columns x0 and x1: runs
// of pixel rows with ink, split where a row is nearly blank. Specks and the
// marks Telugu writes above or below a letter, which form thin runs of their
// own, are joined to the nearest row.
func (m *inkMask) writingRows(x0, x1 int) []span {
	ink := make([]int, m.h)
	var sorted []int
	for y := range ink {
		ink[y] = m.rowInk(y, x0, x1)
		if ink[y] > 0 {
			sorted = append(sorted, ink[y])
		}
	}
	if len(sorted) == 0 {
		return nil
	}
	sort.Ints(sorted)
	busy := sorted[len(sorted)*9/10]
	quiet := max(busy*3/100, 1)
	var rows []span
	for y := 0; y < m.h; {
		if ink[y] <= quiet {
			y++
			continue
		}
		top := y
		for y < m.h && ink[y] > quiet {
			y++
		}
		rows = append(rows, span{top, y})
	}
	if len(rows) == 0 {
		return nil
	}
	var hs []int
	for _, r := range rows {
		hs = append(hs, r.bottom-r.top)
	}
	sort.Ints(hs)
	tall := hs[len(hs)*3/4] // most runs are whole lines of writing
	for changed := true; changed && len(rows) > 1; {
		changed = false
		for i, r := range rows {
			if r.bottom-r.top >= tall*2/5 {
				continue
			}
			j := i - 1 // join it to the closer neighbour
			if i == 0 || (i+1 < len(rows) && rows[i+1].top-r.bottom < r.top-rows[i-1].bottom) {
				j = i + 1
			}
			a, b := min(i, j), max(i, j)
			rows[a] = span{min(rows[a].top, rows[b].top), max(rows[a].bottom, rows[b].bottom)}
			rows = append(rows[:b], rows[b+1:]...)
			changed = true
			break
		}
	}
	return rows
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
