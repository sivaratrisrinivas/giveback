package match

import (
	"strings"
	"unicode"
)

// Roman writes Telugu script in plain Latin letters, without long-vowel or
// aspiration marks (భూతం -> "butam", రెడ్డి -> "reddi"). Other characters
// pass through lower-cased. It is only used to compare names, never shown.
func Roman(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if c, ok := consonants[r]; ok {
			b.WriteString(c)
			// The consonant carries an "a" unless a vowel sign or virama follows.
			if i+1 < len(rs) {
				if v, ok := vowelSigns[rs[i+1]]; ok {
					b.WriteString(v)
					i++
					continue
				}
				if rs[i+1] == '్' { // virama: no vowel
					i++
					continue
				}
			}
			b.WriteString("a")
			continue
		}
		if v, ok := vowels[r]; ok {
			b.WriteString(v)
			continue
		}
		switch r {
		case 'ం': // anusvara ం
			b.WriteString("m")
		case 'ః': // visarga ః
			b.WriteString("h")
		case 'ఁ', '్': // chandrabindu, stray virama
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

var vowels = map[rune]string{
	'అ': "a", 'ఆ': "a", 'ఇ': "i", 'ఈ': "i", 'ఉ': "u", 'ఊ': "u", 'ఋ': "ru",
	'ఎ': "e", 'ఏ': "e", 'ఐ': "ai", 'ఒ': "o", 'ఓ': "o", 'ఔ': "au",
}

var vowelSigns = map[rune]string{
	'ా': "a", 'ి': "i", 'ీ': "i", 'ు': "u", 'ూ': "u", 'ృ': "ru",
	'ె': "e", 'ే': "e", 'ై': "ai", 'ొ': "o", 'ో': "o", 'ౌ': "au",
}

var consonants = map[rune]string{
	'క': "k", 'ఖ': "k", 'గ': "g", 'ఘ': "g", 'ఙ': "n",
	'చ': "ch", 'ఛ': "ch", 'జ': "j", 'ఝ': "j", 'ఞ': "n",
	'ట': "t", 'ఠ': "t", 'డ': "d", 'ఢ': "d", 'ణ': "n",
	'త': "t", 'థ': "t", 'ద': "d", 'ధ': "d", 'న': "n",
	'ప': "p", 'ఫ': "p", 'బ': "b", 'భ': "b", 'మ': "m",
	'య': "y", 'ర': "r", 'ఱ': "r", 'ల': "l", 'ళ': "l", 'ఴ': "l", 'వ': "v",
	'శ': "s", 'ష': "s", 'స': "s", 'హ': "h",
}

// Key reduces one name word, in either script, to how it sounds, so that the
// usual ways of spelling a Telugu name in English meet the Telugu script:
// "Butham", "Bhootham" and భూతం all become "butam"; "Reddy" and రెడ్డి "redi".
func Key(word string) string {
	s := Roman(word)
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	s = b.String()
	s = keyReplacer.Replace(s)
	// "yi" after a vowel is how Telugu writes a final "i": బాబాయి = babai.
	s = strings.ReplaceAll(s, "ayi", "ai")
	if strings.HasSuffix(s, "y") && len(s) > 1 {
		s = s[:len(s)-1] + "i"
	}
	// One letter for doubled letters: reddi -> redi, venkkata -> venkata.
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && s[i] == s[i-1] {
			continue
		}
		c := s[i]
		// m before a consonant is said n: narsimha and నరసింహ (narasimha) meet.
		if c == 'm' && i+1 < len(s) && !isVowel(s[i+1]) && s[i+1] != 'y' {
			c = 'n'
		}
		out = append(out, c)
	}
	return string(out)
}

var keyReplacer = strings.NewReplacer(
	"aiah", "aya", "aiya", "aya", // Nagaiah, Nagaiya = నాగయ్య
	"aa", "a", "ee", "i", "ii", "i", "oo", "u", "uu", "u",
	"th", "t", "dh", "d", "bh", "b", "kh", "k", "gh", "g", "ph", "p", "jh", "j",
	"ch", "c", "sh", "s", "ck", "k",
	"w", "v", "z", "j", "q", "k", "f", "p", "x", "ks",
)

func isVowel(c byte) bool { return strings.IndexByte("aeiou", c) >= 0 }

// skeleton drops an "a" between two consonants: Telugu writes the "a" that
// English spellings often leave out (నరసింహ "narasinha" vs "Narsimha").
func skeleton(k string) string {
	var out []byte
	for i := 0; i < len(k); i++ {
		if k[i] == 'a' && i > 0 && i+1 < len(k) && !isVowel(k[i-1]) && !isVowel(k[i+1]) {
			continue
		}
		out = append(out, k[i])
	}
	return string(out)
}

// SameName reports whether two name words (any script) are the same name.
func SameName(a, b string) bool {
	return sameKey(Key(a), Key(b))
}

func sameKey(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b || skeleton(a) == skeleton(b) {
		return true
	}
	// One slip in a long name (Mutyalu / Mutyala), never in a short one.
	return len(a) >= 6 && len(b) >= 6 && editDistance(a, b) <= 1
}

func editDistance(a, b string) int {
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
