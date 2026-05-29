package tts

// Text normalization: spell out numbers and currency amounts and collapse
// whitespace so the phonemizer sees plain words.

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	smallWords = []string{
		"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
		"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen",
		"seventeen", "eighteen", "nineteen",
	}
	tensWords  = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}
	scaleWords = []string{"", "thousand", "million", "billion", "trillion"}
)

// NumberToWords renders an integer as English words ("forty-two", "twelve hundred").
func NumberToWords(n int64) string {
	switch {
	case n == 0:
		return "zero"
	case n < 0:
		return "negative " + NumberToWords(-n)
	}
	u := uint64(n)

	// 100–9999 exact hundreds (not multiples of 1000) read as "twelve hundred".
	if u >= 100 && u <= 9999 && u%100 == 0 && u%1000 != 0 {
		if h := u / 100; h < 20 {
			return smallWords[h] + " hundred"
		}
	}

	var parts []string
	for _, scale := range scaleWords {
		if chunk := u % 1000; chunk > 0 {
			words := threeDigits(chunk)
			if scale != "" {
				words += " " + scale
			}
			parts = append(parts, words)
		}
		u /= 1000
		if u == 0 {
			break
		}
	}
	// scaleWords yields least-significant first; reverse for reading order.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, " ")
}

func threeDigits(n uint64) string {
	if n == 0 {
		return ""
	}
	var parts []string
	if h := n / 100; h > 0 {
		parts = append(parts, smallWords[h]+" hundred")
	}
	switch r := n % 100; {
	case r == 0:
	case r < 20:
		parts = append(parts, smallWords[r])
	default:
		t := tensWords[r/10]
		if o := smallWords[r%10]; o != "" {
			t += "-" + o
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " ")
}

var (
	reCurrency = regexp.MustCompile(`\$(\d+)(?:\.(\d{2}))?`)
	reNumber   = regexp.MustCompile(`\b(\d+)\b`)
	reSpaces   = regexp.MustCompile(`\s+`)
)

// Normalize runs the full text-normalization pipeline.
func Normalize(text string) string {
	s := expandCurrency(text)
	s = expandNumbers(s)
	return strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
}

// expandCurrency turns "$42.50" into "forty-two dollars and fifty cents".
func expandCurrency(text string) string {
	return reCurrency.ReplaceAllStringFunc(text, func(m string) string {
		g := reCurrency.FindStringSubmatch(m)
		dollars, _ := strconv.ParseInt(g[1], 10, 64)
		var cents int64
		if g[2] != "" {
			cents, _ = strconv.ParseInt(g[2], 10, 64)
		}
		var b strings.Builder
		b.WriteString(NumberToWords(dollars))
		b.WriteString(plural(dollars, " dollar", " dollars"))
		if cents > 0 {
			b.WriteString(" and ")
			b.WriteString(NumberToWords(cents))
			b.WriteString(plural(cents, " cent", " cents"))
		}
		return b.String()
	})
}

// expandNumbers replaces standalone integers with their word form.
func expandNumbers(text string) string {
	return reNumber.ReplaceAllStringFunc(text, func(m string) string {
		n, _ := strconv.ParseInt(m, 10, 64)
		return NumberToWords(n)
	})
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
