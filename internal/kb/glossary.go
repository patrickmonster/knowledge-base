package kb

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Glossary (용어집). A glossary document's title is the term; its synonyms
// come from a "## 동의어" section or a "동의어: a, b" line. Search expands a
// query with every term of each active glossary entry the query mentions, so
// "결제" also finds documents that only say "payment" or "PG".

const (
	maxGlossaryEntries = 500
	maxExpandedTerms   = 20
	maxGlossaryTerm    = 60
)

var (
	synonymHeading  = regexp.MustCompile(`(?i)^#{1,6}\s*(동의어|유의어|별칭|synonyms?|aliases?)\s*$`)
	synonymLine     = regexp.MustCompile(`(?i)^\s*(?:[-*]\s*)?(?:동의어|유의어|별칭|synonyms?|aliases?)\s*[:：]\s*(.+)$`)
	anyHeading      = regexp.MustCompile(`^#{1,6}\s`)
	bulletPrefix    = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	termTitlePrefix = regexp.MustCompile(`^\s*(?:용어|term)\s*[:：—-]\s*`)
	emptyTerms      = map[string]bool{"없음": true, "none": true, "n/a": true, "-": true, "해당 없음": true, "미확인": true}
)

// glossaryTerm returns the term a glossary title defines.
func glossaryTerm(title string) string {
	return strings.TrimSpace(termTitlePrefix.ReplaceAllString(title, ""))
}

// parseSynonyms extracts synonyms from glossary content, deduplicated
// case-insensitively and excluding the term itself.
func parseSynonyms(term, content string) []string {
	out := []string{}
	seen := map[string]bool{normalizeTerm(term): true}
	addAll := func(text string) {
		for _, part := range strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(",，、;；", r) }) {
			part = strings.TrimSpace(bulletPrefix.ReplaceAllString(part, ""))
			part = strings.Trim(part, "`'\"“”‘’.。 ")
			key := normalizeTerm(part)
			if key == "" || emptyTerms[key] || seen[key] || utf8.RuneCountInString(part) > maxGlossaryTerm {
				continue
			}
			seen[key] = true
			out = append(out, part)
		}
	}
	inSection := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case synonymHeading.MatchString(trimmed):
			inSection = true
		case anyHeading.MatchString(trimmed):
			inSection = false
		case inSection:
			addAll(trimmed)
		default:
			if m := synonymLine.FindStringSubmatch(trimmed); m != nil {
				addAll(m[1])
			}
		}
	}
	return out
}

func normalizeTerm(s string) string {
	return strings.ToLower(strings.TrimSpace(norm.NFKC.String(s)))
}

func hasHangul(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// mentions reports whether normalized query q contains normalized term t.
// Korean terms match as substrings (particles attach directly: 결제를);
// other terms must stand alone so "pg" does not match "upgrade", except that
// a Korean particle may follow them directly ("task run이", "PG를").
func mentions(q, t string) bool {
	if utf8.RuneCountInString(t) < 2 {
		return false
	}
	if hasHangul(t) {
		return strings.Contains(q, t)
	}
	for start := 0; ; {
		i := strings.Index(q[start:], t)
		if i < 0 {
			return false
		}
		i += start
		before, _ := utf8.DecodeLastRuneInString(q[:i])
		after, _ := utf8.DecodeRuneInString(q[i+len(t):])
		if !isWordRune(before) && (!isWordRune(after) || unicode.Is(unicode.Hangul, after)) {
			return true
		}
		start = i + 1
	}
}

func isWordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// expandWithGlossary returns the terms to add to query: every term of a
// glossary group the query mentions, minus terms already in the query.
func expandWithGlossary(query string, groups [][]string) []string {
	q := normalizeTerm(query)
	out := []string{}
	seen := map[string]bool{}
	for _, group := range groups {
		matched := false
		for _, term := range group {
			if mentions(q, normalizeTerm(term)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, term := range group {
			key := normalizeTerm(term)
			// Same boundary rule as matching: "pg" inside "upgrade" is not
			// already in the query, so PG is still added.
			if key == "" || seen[key] || mentions(q, key) {
				continue
			}
			seen[key] = true
			out = append(out, term)
			if len(out) == maxExpandedTerms {
				return out
			}
		}
	}
	return out
}

// glossaryExpansion loads active glossary entries in scope and expands query.
func glossaryExpansion(ctx context.Context, tx *sql.Tx, scope, query string) ([]string, error) {
	type entry struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	entries, err := many[entry](ctx, tx, `SELECT jsonb_build_object('title',title,'content',left(content,8000)) FROM documents
	WHERE project_id=ANY($1::text::uuid[]) AND kind='glossary' AND status='active' ORDER BY updated_at DESC,id LIMIT $2`, scope, maxGlossaryEntries)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	groups := make([][]string, 0, len(entries))
	for _, e := range entries {
		term := glossaryTerm(e.Title)
		groups = append(groups, append([]string{term}, parseSynonyms(term, e.Content)...))
	}
	return expandWithGlossary(query, groups), nil
}
