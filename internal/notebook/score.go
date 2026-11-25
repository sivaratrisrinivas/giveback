package notebook

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// KeyLine is one line of the answer key: what was written by hand.
type KeyLine struct {
	Number       string
	Written      string  // the whole line as written
	AmountDigits string  // "" if no cash
	Gold         float64 // sovereigns, 0 if none
}

// KeyPage is one page of the answer key.
type KeyPage struct {
	Number   int
	Function string
	Date     string
	Lines    []KeyLine
}

var (
	keyHeading = regexp.MustCompile(`^## Page (\d+): (.+?) \(.*\), (\S+)\s*$`)
	keyRow     = regexp.MustCompile(`^\| (\d+) \| (.+?) \| .* \|\s*$`)
	leadingNum = regexp.MustCompile(`^(\d+(?:\.\d+)?)`)
)

// ParseKey reads the answer key tables from docs/synthetic-notebook.md.
func ParseKey(r io.Reader) (map[int]KeyPage, error) {
	pages := map[int]KeyPage{}
	cur := -1
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if m := keyHeading.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			pages[n] = KeyPage{Number: n, Function: m[2], Date: m[3]}
			cur = n
			continue
		}
		if strings.HasPrefix(line, "## ") {
			cur = -1
			continue
		}
		m := keyRow.FindStringSubmatch(line)
		if m == nil || cur < 0 {
			continue
		}
		written := m[2]
		kl := KeyLine{Number: m[1], Written: written}
		gift := written
		if i := strings.LastIndex(written, " — "); i >= 0 {
			gift = written[i+len(" — "):]
		}
		switch {
		case strings.Contains(gift, "/-"):
			kl.AmountDigits = onlyDigits(gift)
		case strings.Contains(gift, "కాసు"):
			if g := leadingNum.FindString(gift); g != "" {
				kl.Gold, _ = strconv.ParseFloat(g, 64)
			}
		}
		p := pages[cur]
		p.Lines = append(p.Lines, kl)
		pages[cur] = p
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no pages found in answer key")
	}
	return pages, nil
}

// LineScore compares one read line with the key.
type LineScore struct {
	Key       KeyLine
	Got       *Line   // nil if the reader missed the line
	CER       float64 // character error rate of the text, 0 = perfect
	AmountOK  bool    // cash and gold both right (or both rightly absent)
	Flagged   bool
	SilentBad bool // amount wrong and NOT flagged: the dangerous case
}

// PageScore is the result for one photo.
type PageScore struct {
	Lines                     []LineScore
	Extra                     []Line // lines the reader found that are not in the key
	FunctionCER               float64
	DateOK                    bool
	Found, AmountsOK, Flagged int
	SilentBad, FalseAlarms    int
	MeanCER                   float64
}

// Score compares a read page with its answer key.
func Score(p Page, key KeyPage) PageScore {
	var s PageScore
	byNum := map[string]*Line{}
	for i := range p.Lines {
		n := onlyDigits(p.Lines[i].Number)
		if _, dup := byNum[n]; dup || n == "" {
			s.Extra = append(s.Extra, p.Lines[i])
			continue
		}
		byNum[n] = &p.Lines[i]
	}
	var cerSum float64
	for _, k := range key.Lines {
		ls := LineScore{Key: k, CER: 1}
		if got, ok := byNum[k.Number]; ok {
			delete(byNum, k.Number)
			ls.Got = got
			s.Found++
			ls.CER = CER(k.Written, got.Text)
			ls.AmountOK = got.AmountDigits == k.AmountDigits && got.GoldSovereigns == k.Gold
			ls.Flagged = len(got.Flags) > 0
			ls.SilentBad = !ls.AmountOK && !ls.Flagged
		} else {
			ls.SilentBad = true // a missed gift is a silent error too
		}
		cerSum += ls.CER
		if ls.AmountOK {
			s.AmountsOK++
		}
		if ls.Flagged {
			s.Flagged++
			if ls.AmountOK {
				s.FalseAlarms++
			}
		}
		if ls.SilentBad {
			s.SilentBad++
		}
		s.Lines = append(s.Lines, ls)
	}
	for _, l := range byNum {
		s.Extra = append(s.Extra, *l)
	}
	if len(key.Lines) > 0 {
		s.MeanCER = cerSum / float64(len(key.Lines))
	}
	s.FunctionCER = CER(key.Function, p.Function)
	s.DateOK = onlyDigits(p.Date) == onlyDigits(key.Date)
	return s
}

// CER is the character error rate of got against want, after dropping spaces
// and punctuation that handwriting renders inconsistently (dashes, commas, "/-").
func CER(want, got string) float64 {
	w, g := []rune(normalize(want)), []rune(normalize(got))
	if len(w) == 0 {
		if len(g) == 0 {
			return 0
		}
		return 1
	}
	return float64(levenshtein(w, g)) / float64(len(w))
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || r == '—' || r == '–' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
