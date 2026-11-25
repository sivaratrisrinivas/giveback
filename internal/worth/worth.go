// Package worth says roughly what an old rupee amount is worth today, using
// the official Consumer Price Index for Industrial Workers (CPI-IW) published
// by the Labour Bureau, Government of India.
//
// It never touches gold: gold is shown by weight only, never turned into
// rupees, because gold is given back by weight.
//
// The figures below are copied as published; nothing is estimated.
//
//   - Yearly averages (April to March): RBI Handbook of Statistics on Indian
//     Economy 2025-26, Table 34 "Consumer Price Index - Annual Average"
//     (dated Jul 31, 2026), column CPI-IW. Source named there: Labour Bureau.
//     https://www.rbi.org.in/Scripts/PublicationsView.aspx?id=23859
//   - Today's index: Labour Bureau CPI-IW page, latest month shown (Aug 2026,
//     base 2016=100). https://labourbureau.gov.in/CPI
//   - Linking factors, from the notes to Table 34: "CPI - IW series for
//     2005-06 has been derived by linking the 2001 series with the 1982
//     series by using the linking factor 4.63 for the 'General Index'" and
//     "CPI - IW data with base 2016=100 were reported from September 2020;
//     the linking factor with base 2001 series is 2.88."
//
// A linking factor says how many points of the old series one point of the
// new series is worth: old = new × factor. So a 1982-base figure is divided by
// 4.63 × 2.88 to put it on base 2016, and a 2001-base figure by 2.88.
package worth

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

const (
	link1982to2001 = 4.63
	link2001to2016 = 2.88

	TodayIndex = 154.4      // CPI-IW, base 2016=100
	TodayMonth = "Aug 2026" // the month TodayIndex is for

	Source = "CPI-IW, Labour Bureau (yearly averages from RBI Handbook 2025-26, Table 34)"
)

// yearly is the CPI-IW general index, average of April to March, keyed by the
// year the April falls in (1993 = 1993-94), exactly as Table 34 prints it.
var yearly = map[int]struct {
	index float64
	base  int
}{
	1993: {258, 1982}, 1994: {284, 1982}, 1995: {313, 1982}, 1996: {342, 1982},
	1997: {366, 1982}, 1998: {414, 1982}, 1999: {428, 1982}, 2000: {444, 1982},
	2001: {463, 1982}, 2002: {482, 1982}, 2003: {500, 1982}, 2004: {520, 1982},
	2005: {117, 2001}, 2006: {125, 2001}, 2007: {133, 2001}, 2008: {145, 2001},
	2009: {163, 2001}, 2010: {180, 2001}, 2011: {195, 2001}, 2012: {215, 2001},
	2013: {236, 2001}, 2014: {251, 2001}, 2015: {265, 2001}, 2016: {276, 2001},
	2017: {284, 2001}, 2018: {300, 2001}, 2019: {323, 2001},
	2020: {118, 2016}, 2021: {124, 2016}, 2022: {131, 2016}, 2023: {138, 2016},
	2024: {143, 2016}, 2025: {147.0, 2016},
}

// First and last financial years in the table, for messages.
const (
	FirstYear = 1993
	LastYear  = 2025
)

// Value is an old amount in today's money.
type Value struct {
	Then    int     // rupees as written
	Today   int     // rounded, about
	Factor  float64 // how many times prices have gone up
	Year    string  // the financial year used, e.g. "1993-94"
	Index   float64 // that year's average, as published
	Base    int     // the base year of that published figure
	Guessed bool    // only a year was written, no month: the April–December year was used
}

// Today converts rupees given on date (as written in the notebook, e.g.
// "12-2-1994") into today's money. ok is false when the date has no clear
// four-digit year or falls outside the official table: then no value is
// given rather than a guess.
func Today(rupees int, date string) (v Value, ok bool) {
	fy, guessed, ok := financialYear(date)
	if !ok {
		return Value{}, false
	}
	y, ok := yearly[fy]
	if !ok {
		return Value{}, false
	}
	on2016 := y.index
	switch y.base {
	case 1982:
		on2016 = y.index / (link1982to2001 * link2001to2016)
	case 2001:
		on2016 = y.index / link2001to2016
	}
	factor := TodayIndex / on2016
	return Value{
		Then:    rupees,
		Today:   round2(float64(rupees) * factor),
		Factor:  factor,
		Year:    fmt.Sprintf("%d-%02d", fy, (fy+1)%100),
		Index:   y.index,
		Base:    y.base,
		Guessed: guessed,
	}, true
}

// Cite is the one-line source shown under a value.
func (v Value) Cite() string {
	link := ""
	switch v.Base {
	case 1982:
		link = ", linked ×4.63 ×2.88"
	case 2001:
		link = ", linked ×2.88"
	}
	return fmt.Sprintf("Source: %s. %s average %g (base %d=100)%s; %s: %g (base 2016=100).",
		Source, v.Year, v.Index, v.Base, link, TodayMonth, TodayIndex)
}

var (
	reYear = regexp.MustCompile(`(?:^|\D)((?:19|20)\d\d)(?:\D|$)`)
	// day-month-year as Indian notebooks write it: 12-2-1994, 12/02/1994, 12.2.1994
	reDMY = regexp.MustCompile(`(?:^|\D)(\d{1,2})[-/.](\d{1,2})[-/.]((?:19|20)\d\d)(?:\D|$)`)
)

// financialYear returns the April-to-March year a written date falls in.
// Without a month, it uses the year starting that April (it covers nine of
// the twelve months) and says so.
func financialYear(date string) (fy int, guessed, ok bool) {
	if m := reDMY.FindStringSubmatch(date); m != nil {
		month, _ := strconv.Atoi(m[2])
		year, _ := strconv.Atoi(m[3])
		if month >= 1 && month <= 12 {
			if month <= 3 {
				return year - 1, false, true
			}
			return year, false, true
		}
	}
	m := reYear.FindAllStringSubmatch(date, -1)
	if len(m) != 1 { // none, or two different years: don't guess
		return 0, false, false
	}
	year, _ := strconv.Atoi(m[0][1])
	return year, true, true
}

// round2 keeps two significant figures (₹926 → ₹930, ₹8,906 → ₹8,900), since
// the value is only "about": index averages and linking factors are rough.
func round2(x float64) int {
	if x < 10 {
		return int(math.Round(x))
	}
	p := math.Pow(10, math.Floor(math.Log10(x))-1)
	return int(math.Round(x/p) * p)
}
