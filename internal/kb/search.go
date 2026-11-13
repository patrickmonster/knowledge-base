package kb

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var words = regexp.MustCompile(`[\pL\pN]+`)
var hangul = regexp.MustCompile(`[가-힣]{2,}`)

// Lexemes indexes whole words and Korean bigrams, matching 결제 inside 결제를.
// It is lexical retrieval, not an embedding or synonym model.
func Lexemes(text string) []string {
	seen := map[string]bool{}
	result := []string{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, word := range words.FindAllString(strings.ToLower(norm.NFKC.String(text)), -1) {
		add(word)
		for _, part := range hangul.FindAllString(word, -1) {
			r := []rune(part)
			for i := 0; i+1 < len(r); i++ {
				add(string(r[i : i+2]))
			}
		}
	}
	return result
}
func QueryText(text string) string {
	terms := Lexemes(text)
	if len(terms) > 64 {
		terms = terms[:64]
	}
	for i, term := range terms {
		terms[i] = "'" + term + "'"
	}
	return strings.Join(terms, " | ")
}
func SplitChunks(text string) []string {
	r := []rune(text)
	result := []string{}
	for start := 0; start < len(r); start += 1400 {
		end := min(start+1600, len(r))
		result = append(result, string(r[start:end]))
		if end == len(r) {
			break
		}
	}
	return result
}
