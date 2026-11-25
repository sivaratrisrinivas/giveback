package worth

import (
	"math"
	"strings"
	"testing"
)

func TestToday(t *testing.T) {
	cases := []struct {
		rupees  int
		date    string
		year    string
		today   int
		guessed bool
	}{
		// Feb 1994 is in 1993-94: 116 × 154.4 / (258 / 4.63 / 2.88) = 925.7
		{116, "12-2-1994", "1993-94", 930, false},
		{1116, "12-2-1994", "1993-94", 8900, false},
		// Jun 2003 is in 2003-04 (still base 1982): 1116 × 154.4 / (500 / 13.3344) = 4595
		{1116, "8-6-2003", "2003-04", 4600, false},
		// base 2001: 2010-11 = 180; 500 × 154.4 / (180 / 2.88) = 1235
		{500, "15/8/2010", "2010-11", 1200, false},
		// base 2016 directly: 2023-24 = 138
		{1000, "1.1.2024", "2023-24", 1100, false},
		// year only: April–March year starting then
		{116, "1994", "1994-95", 840, true},
	}
	for _, c := range cases {
		v, ok := Today(c.rupees, c.date)
		if !ok {
			t.Errorf("Today(%d, %q): no value", c.rupees, c.date)
			continue
		}
		if v.Year != c.year || v.Today != c.today || v.Guessed != c.guessed {
			t.Errorf("Today(%d, %q) = year %s, ₹%d, guessed %v; want %s, ₹%d, %v",
				c.rupees, c.date, v.Year, v.Today, v.Guessed, c.year, c.today, c.guessed)
		}
	}
}

func TestFactorIsExact(t *testing.T) {
	v, _ := Today(116, "12-2-1994")
	want := 154.4 / (258 / (4.63 * 2.88))
	if math.Abs(v.Factor-want) > 1e-9 {
		t.Errorf("factor %v, want %v", v.Factor, want)
	}
}

func TestNoGuessing(t *testing.T) {
	for _, d := range []string{"", "12-2-94", "1985", "1994 or 1995", "Padma's wedding"} {
		if v, ok := Today(116, d); ok {
			t.Errorf("Today(116, %q) = %+v; want no value", d, v)
		}
	}
}

func TestCite(t *testing.T) {
	v, _ := Today(116, "12-2-1994")
	c := v.Cite()
	for _, want := range []string{"Labour Bureau", "Table 34", "1993-94 average 258 (base 1982=100)", "×4.63 ×2.88", "Aug 2026: 154.4 (base 2016=100)"} {
		if !strings.Contains(c, want) {
			t.Errorf("Cite() = %q, missing %q", c, want)
		}
	}
}
