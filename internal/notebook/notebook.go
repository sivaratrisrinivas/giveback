// Package notebook reads a photographed page of a family gift notebook into
// lines, cuts out a picture of each line, and reads every amount twice so a
// misread (₹116 vs ₹1,116) is flagged instead of trusted.
package notebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"strconv"
	"strings"

	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

// Line is one gift written on the page.
type Line struct {
	Number         string  `json:"number"`          // the line number as written, "" if none
	Text           string  `json:"text"`            // the whole line as written, without its number
	Name           string  `json:"name"`            // who gave: a name or a relation word, as written
	Place          string  `json:"place"`           // town written with the name, "" if none
	AmountDigits   string  `json:"amount_digits"`   // cash amount, digits only, "" if no cash
	GoldSovereigns float64 `json:"gold_sovereigns"` // కాసు / sovereigns of gold, 0 if none
	Item           string  `json:"item"`            // non-cash gift as written (ring, saree...), "" if none
	Unclear        bool    `json:"unclear"`         // the model itself was unsure of this line
	Box            [4]int  `json:"box_2d"`          // [ymin, xmin, ymax, xmax], 0–1000, in the photo as sent

	// Filled in after the model answers.
	Crop         image.Image `json:"-"` // the line, cut out and turned upright
	SecondDigits string      `json:"second_amount_digits"`
	SecondGold   float64     `json:"second_gold_sovereigns"`
	Flags        []string    `json:"flags"` // why a person should look at this line
}

// Rupees returns the cash amount and whether there was one.
func (l Line) Rupees() (int, bool) {
	if l.AmountDigits == "" {
		return 0, false
	}
	n, err := strconv.Atoi(l.AmountDigits)
	return n, err == nil
}

// Page is one photographed notebook page.
type Page struct {
	Function string `json:"function"` // the heading: which function these gifts were given at
	Date     string `json:"date"`     // as written, e.g. "12-2-1994"
	Rotation int    `json:"rotation"` // degrees clockwise to turn the photo upright
	Lines    []Line `json:"lines"`

	Warnings []string `json:"-"` // page-level problems a person should know about

	FirstRead  vision.Result `json:"-"`
	SecondRead vision.Result `json:"-"`
}

func readPage(ctx context.Context, r *vision.Reader, photo vision.Image) (Page, error) {
	res, err := r.ReadJSON(ctx, pagePrompt, pageSchema, photo)
	if err != nil {
		return Page{}, err
	}
	var p Page
	if err := json.Unmarshal(res.JSON, &p); err != nil {
		return Page{}, fmt.Errorf("page JSON from %s: %w", res.Model, err)
	}
	p.FirstRead = res
	return p, nil
}

// Flagged returns the lines a person must check.
func (p Page) Flagged() []Line {
	var out []Line
	for _, l := range p.Lines {
		if len(l.Flags) > 0 {
			out = append(out, l)
		}
	}
	return out
}

const pagePrompt = `This is a photo of one page from an Indian family's gift notebook (usually Telugu).
At a wedding or function the family writes down each gift: who gave it and how much.
Lines mix Telugu script and English. Copy the handwriting exactly; do not correct or translate it.

The photo may be sideways or upside down. Set "rotation" to how many degrees CLOCKWISE the photo
must be turned so the writing reads normally (0, 90, 180 or 270).

The paper may be thin: faint, mirror-image writing showing through from the other side of the
sheet is NOT on this page. Ignore it completely. Read only the dark ink written on this side.

Give "function" (the heading, e.g. a wedding or housewarming, copied as written) and "date" as written.

For every gift line, top to bottom:
- number: the line number written at the start, "" if none.
- text: the whole line exactly as written, without the line number.
- name: who gave the gift, as written. It may be a relation word like పెద్దనాన్న, బాబాయి, పిన్ని, మామ, అత్త.
- place: a town written with the name, "" if none.
- amount_digits: the cash amount as digits only, dropping "Rs", commas and the "/-" ending
  ("Rs 1,116/-" -> "1116"). "" if there is no cash. Look carefully at how many digits there are:
  116 and 1116 are very different gifts.
- gold_sovereigns: number of sovereigns (కాసు / కాసులు) if gold is given, else 0.
- item: a non-cash gift as written (ring, chain, plate, saree...), "" if none.
- unclear: true if you are not sure of any part of this line.
- box_2d: [ymin, xmin, ymax, xmax] of the whole line including the amount, in the photo exactly as
  given (before any rotation), scaled 0-1000.

Do not guess. If a word cannot be read, copy what you can and set unclear to true.
Return only JSON with keys: function, date, rotation, lines.`

var pageSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"function": map[string]any{"type": "string"},
		"date":     map[string]any{"type": "string"},
		"rotation": map[string]any{"type": "integer", "enum": []int{0, 90, 180, 270}},
		"lines": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"number":          map[string]any{"type": "string"},
				"text":            map[string]any{"type": "string"},
				"name":            map[string]any{"type": "string"},
				"place":           map[string]any{"type": "string"},
				"amount_digits":   map[string]any{"type": "string"},
				"gold_sovereigns": map[string]any{"type": "number"},
				"item":            map[string]any{"type": "string"},
				"unclear":         map[string]any{"type": "boolean"},
				"box_2d": map[string]any{"type": "array", "items": map[string]any{"type": "integer"},
					"minItems": 4, "maxItems": 4},
			},
			"required": []string{"number", "text", "name", "place", "amount_digits",
				"gold_sovereigns", "item", "unclear", "box_2d"},
		}},
	},
	"required": []string{"function", "date", "rotation", "lines"},
}

// The second read sees only the cropped lines, and is asked a narrower
// question in a different way, so it is not simply repeating the first answer.
const amountPrompt = `Each image is one handwritten line cut from an Indian family's gift notebook, in order: image 1 is
the first line. Ignore the name. Look only at the gift at the END of each line.

For each image, in the same order, give:
- digits_seen: every digit of the money amount, one by one, separated by spaces, exactly as written
  ("Rs 1,116/-" -> "1 1 1 6"; "516/-" -> "5 1 6"). The "/-" is an ending mark, not a digit. Do not count
  the line number at the start of the line. "" if there is no money amount.
- digit_count: how many digits that is.
- gold_sovereigns: if the gift is gold in sovereigns (కాసు / కాసులు), how many; else 0.
  A number written just before కాసు / కాసులు counts sovereigns of gold, NOT money: "1 కాసు ఉంగరం"
  is digits_seen "", gold_sovereigns 1.
Return only JSON: {"lines": [...]} with exactly one entry per image.`

var amountSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"lines": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"digits_seen":     map[string]any{"type": "string"},
				"digit_count":     map[string]any{"type": "integer"},
				"gold_sovereigns": map[string]any{"type": "number"},
			},
			"required": []string{"digits_seen", "digit_count", "gold_sovereigns"},
		}},
	},
	"required": []string{"lines"},
}

// Read reads one page photo: lines with boxes, a crop of each line, and a
// second, independent read of every amount. Lines needing a person's eye are flagged.
func Read(ctx context.Context, r *vision.Reader, photo vision.Image) (Page, error) {
	img, _, err := image.Decode(bytes.NewReader(photo.Data))
	if err != nil {
		return Page{}, fmt.Errorf("decode photo: %w", err)
	}

	p, err := readPage(ctx, r, photo)
	if err != nil {
		return Page{}, err
	}
	turned := 0
	// A sideways photo is turned upright and read again: models read and place
	// boxes far better on an upright page, and the turn they claimed gets checked.
	if p.Rotation != 0 {
		turned = p.Rotation
		img = Rotate(img, turned)
		jpg, err := EncodeJPEG(img)
		if err != nil {
			return Page{}, err
		}
		first := p.FirstRead
		if p, err = readPage(ctx, r, vision.Image{MIME: "image/jpeg", Data: jpg}); err != nil {
			return Page{}, err
		}
		p.FirstRead.Elapsed += first.Elapsed
		if p.Rotation != 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("still not upright after turning %d°", turned))
		}
	}

	upright := Rotate(img, p.Rotation)
	boxes := make([][4]int, len(p.Lines))
	for i, l := range p.Lines {
		boxes[i] = uprightBox(l.Box, p.Rotation)
	}
	p.Rotation = (p.Rotation + turned) % 360
	cut, cutErrs := cropLines(upright, boxes)

	var crops []vision.Image
	for i := range p.Lines {
		l := &p.Lines[i]
		l.Flags = nil
		if l.Unclear {
			l.Flags = append(l.Flags, "model unsure of this line")
		}
		if _, ok := l.Rupees(); l.AmountDigits != "" && !ok {
			l.Flags = append(l.Flags, fmt.Sprintf("amount %q is not a number", l.AmountDigits))
		}
		if cutErrs[i] != nil {
			l.Flags = append(l.Flags, "no picture of this line: "+cutErrs[i].Error())
			continue
		}
		c := cut[i]
		l.Crop = c
		jpg, err := EncodeJPEG(c)
		if err != nil {
			return Page{}, err
		}
		crops = append(crops, vision.Image{MIME: "image/jpeg", Data: jpg})
	}
	if len(crops) == 0 {
		return p, nil
	}

	res2, err := r.ReadJSON(ctx, amountPrompt, amountSchema, crops...)
	if err != nil {
		for i := range p.Lines {
			p.Lines[i].Flags = append(p.Lines[i].Flags, "second read failed: amount checked only once")
		}
		return p, nil
	}
	p.SecondRead = res2
	var second struct {
		Lines []struct {
			DigitsSeen     string  `json:"digits_seen"`
			DigitCount     int     `json:"digit_count"`
			GoldSovereigns float64 `json:"gold_sovereigns"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(res2.JSON, &second); err != nil || len(second.Lines) != len(crops) {
		for i := range p.Lines {
			p.Lines[i].Flags = append(p.Lines[i].Flags, "second read unusable: amount checked only once")
		}
		return p, nil
	}
	k := 0
	for i := range p.Lines {
		l := &p.Lines[i]
		if l.Crop == nil {
			continue
		}
		s := second.Lines[k]
		k++
		l.SecondDigits = onlyDigits(s.DigitsSeen)
		l.SecondGold = s.GoldSovereigns
		if l.SecondDigits != l.AmountDigits {
			l.Flags = append(l.Flags, fmt.Sprintf("amount read as %s, then as %s", show(l.AmountDigits), show(l.SecondDigits)))
		} else if s.DigitCount != len(l.SecondDigits) {
			l.Flags = append(l.Flags, fmt.Sprintf("second read saw %d digits but listed %q", s.DigitCount, s.DigitsSeen))
		}
		if l.SecondGold != l.GoldSovereigns {
			l.Flags = append(l.Flags, fmt.Sprintf("gold read as %g, then as %g sovereigns", l.GoldSovereigns, l.SecondGold))
		}
	}
	return p, nil
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func show(digits string) string {
	if digits == "" {
		return "no cash"
	}
	return "₹" + digits
}
