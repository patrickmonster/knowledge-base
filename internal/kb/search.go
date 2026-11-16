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

// taskLinkHeading is the generated block finish_task appends to history documents.
const taskLinkHeading = "## 작업 연결 정보"

var kbCitation = regexp.MustCompile(`kb://\S+`)

// IndexableText removes generated metadata before chunking: the task link block
// of history documents (run ID, start time, starting citations) and kb:// citation
// URLs, whose UUID fragments otherwise dominate keyword and vector matches.
// Stored document content is unchanged; only chunks are affected.
func IndexableText(kind, content string) string {
	if kind == "history" {
		lines := strings.Split(content, "\n")
		kept := make([]string, 0, len(lines))
		skip := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
				skip = trimmed == taskLinkHeading
			}
			if !skip {
				kept = append(kept, line)
			}
		}
		content = strings.Join(kept, "\n")
	}
	return strings.TrimSpace(kbCitation.ReplaceAllString(content, ""))
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
