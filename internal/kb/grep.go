package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// grep_documents limits. A regex search cannot be narrowed in SQL, so the scan
// is bounded by document count and total bytes; fixed strings are prefiltered
// with LIKE/ILIKE (pg_trgm index) and rarely reach the caps.
const (
	grepMaxPattern     = 500
	grepMaxLimit       = 50
	grepMaxContext     = 5
	grepDefaultMatches = 5
	grepMaxMatches     = 50
	grepMaxLineRunes   = 400
	grepScanDocuments  = 2000
	grepScanBytes      = 32 << 20
)

type GrepInput struct {
	ProjectID     string   `json:"project_id"`
	Pattern       string   `json:"pattern" jsonschema:"Text to find, 1–500 characters. A fixed string unless regex is true"`
	Regex         bool     `json:"regex,omitempty" jsonschema:"Treat pattern as an RE2 regular expression (Go syntax), matched per line"`
	CaseSensitive bool     `json:"case_sensitive,omitempty" jsonschema:"Match case exactly; default false (case-insensitive)"`
	Status        string   `json:"status,omitempty" jsonschema:"draft, active, or archived; default active"`
	Kind          string   `json:"kind,omitempty" jsonschema:"brief, decision, history, artifact, reference, glossary, or wiki"`
	Tags          []string `json:"tags,omitempty" jsonschema:"Documents must have every tag (case-insensitive); at most 10"`
	Context       int      `json:"context,omitempty" jsonschema:"Lines of context before and after each match, 0–5; default 0"`
	MaxMatches    int      `json:"max_matches,omitempty" jsonschema:"Matching lines returned per document, 1–50; default 5"`
	Limit         int      `json:"limit,omitempty" jsonschema:"Documents per page, 1–50; default 20"`
	Offset        int      `json:"offset,omitempty" jsonschema:"Matching documents to skip; use next_offset from the previous page"`
}

type GrepMatch struct {
	Line   int      `json:"line"`
	Text   string   `json:"text"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

type GrepDocument struct {
	DocumentID  string      `json:"document_id"`
	Title       string      `json:"title"`
	Kind        string      `json:"kind"`
	Status      string      `json:"status"`
	Revision    int         `json:"revision"`
	Tags        []string    `json:"tags"`
	UpdatedAt   time.Time   `json:"updated_at"`
	TitleMatch  bool        `json:"title_match"`
	MatchCount  int         `json:"match_count"`
	Matches     []GrepMatch `json:"matches"`
	MoreMatches bool        `json:"more_matches"`
	Citation    string      `json:"citation"`
}

type GrepResult struct {
	ProjectID        string         `json:"project_id"`
	Pattern          string         `json:"pattern"`
	Regex            bool           `json:"regex"`
	CaseSensitive    bool           `json:"case_sensitive"`
	Status           string         `json:"status"`
	Documents        []GrepDocument `json:"documents"`
	ScannedDocuments int            `json:"scanned_documents"`
	HasMore          bool           `json:"has_more"`
	NextOffset       *int           `json:"next_offset,omitempty"`
	ScanTruncated    bool           `json:"scan_truncated"`
	Warning          string         `json:"warning,omitempty"`
}

// grepMatcher compiles the pattern into an RE2 expression. Fixed strings are
// quoted so both modes share one line matcher with Go's linear-time engine.
func grepMatcher(in GrepInput) (*regexp.Regexp, error) {
	n := utf8.RuneCountInString(in.Pattern)
	if strings.TrimSpace(in.Pattern) == "" || n > grepMaxPattern {
		return nil, problem("INVALID_INPUT", fmt.Sprintf("pattern must have 1–%d characters.", grepMaxPattern))
	}
	if strings.ContainsAny(in.Pattern, "\n\r") && !in.Regex {
		return nil, problem("INVALID_INPUT", "pattern is matched per line; remove line breaks.")
	}
	expr := in.Pattern
	if !in.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if !in.CaseSensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, problem("INVALID_INPUT", "Invalid regular expression (RE2 syntax): "+err.Error())
	}
	return re, nil
}

// likePattern escapes LIKE metacharacters for a substring match with ESCAPE '\'.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// tagFilter normalizes tag filters shared by list_documents and grep_documents.
func tagFilter(in []string) ([]string, error) {
	tags := normalizeTags(in)
	if len(tags) > 10 {
		return nil, problem("INVALID_INPUT", "Filter by at most 10 tags.")
	}
	for i, tag := range tags {
		if utf8.RuneCountInString(tag) > 50 {
			return nil, problem("INVALID_INPUT", "Each tag filter must have at most 50 characters.")
		}
		tags[i] = strings.ToLower(tag)
	}
	return tags, nil
}

func grepDocuments(ctx context.Context, tx *sql.Tx, actor Identity, in GrepInput) (GrepResult, error) {
	out := GrepResult{ProjectID: in.ProjectID, Pattern: in.Pattern, Regex: in.Regex, CaseSensitive: in.CaseSensitive, Documents: []GrepDocument{}}
	if _, err := projectReadAccess(ctx, tx, actor.User.ID, in.ProjectID); err != nil {
		return out, err
	}
	re, err := grepMatcher(in)
	if err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if in.Limit < 1 || in.Limit > grepMaxLimit || in.Offset < 0 || in.Offset > 100000 {
		return out, problem("INVALID_INPUT", fmt.Sprintf("Invalid pagination: limit 1–%d; offset 0–100000.", grepMaxLimit))
	}
	if in.Context < 0 || in.Context > grepMaxContext {
		return out, problem("INVALID_INPUT", fmt.Sprintf("context must be 0–%d.", grepMaxContext))
	}
	if in.MaxMatches == 0 {
		in.MaxMatches = grepDefaultMatches
	}
	if in.MaxMatches < 1 || in.MaxMatches > grepMaxMatches {
		return out, problem("INVALID_INPUT", fmt.Sprintf("max_matches must be 1–%d.", grepMaxMatches))
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if !validStatus(in.Status) {
		return out, problem("INVALID_INPUT", "Unknown status.")
	}
	out.Status = in.Status
	if in.Kind != "" && !validKind(in.Kind) {
		return out, problem("INVALID_INPUT", "Unknown kind.")
	}
	tags, err := tagFilter(in.Tags)
	if err != nil {
		return out, err
	}
	// Fixed strings narrow the scan in SQL; LIKE/ILIKE use the pg_trgm index
	// when the pattern has at least one trigram. Go re-checks every line.
	like, op := "", "ILIKE"
	if !in.Regex {
		like = likePattern(in.Pattern)
		if in.CaseSensitive {
			op = "LIKE"
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT jsonb_build_object('id',d.id,'title',d.title,'kind',d.kind,'status',d.status,
		'revision',d.revision,'tags',d.tags,'updated_at',d.updated_at,'content',d.content)
	FROM documents d WHERE d.project_id=$1 AND d.status=$2 AND ($3='' OR d.kind=$3)
	AND NOT EXISTS (SELECT 1 FROM unnest($4::text[]) want WHERE NOT EXISTS (SELECT 1 FROM unnest(d.tags) have WHERE lower(have)=want))
	AND ($5='' OR d.content `+op+` $5 ESCAPE '\' OR d.title `+op+` $5 ESCAPE '\')
	ORDER BY d.updated_at DESC,d.id LIMIT $6`, in.ProjectID, in.Status, in.Kind, tags, like, grepScanDocuments+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	skipped, scannedBytes := 0, 0
	for rows.Next() {
		if out.ScannedDocuments == grepScanDocuments || scannedBytes >= grepScanBytes {
			out.ScanTruncated = true
			break
		}
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		var d struct {
			ID        string    `json:"id"`
			Title     string    `json:"title"`
			Kind      string    `json:"kind"`
			Status    string    `json:"status"`
			Revision  int       `json:"revision"`
			Tags      []string  `json:"tags"`
			UpdatedAt time.Time `json:"updated_at"`
			Content   string    `json:"content"`
		}
		if err = json.Unmarshal(raw, &d); err != nil {
			return out, err
		}
		out.ScannedDocuments++
		scannedBytes += len(d.Content)
		titleMatch := re.MatchString(d.Title)
		count, matches := grepLines(re, d.Content, in.Context, in.MaxMatches)
		if count == 0 && !titleMatch {
			continue
		}
		if skipped < in.Offset {
			skipped++
			continue
		}
		if len(out.Documents) == in.Limit {
			out.HasMore = true
			break
		}
		if d.Tags == nil {
			d.Tags = []string{}
		}
		out.Documents = append(out.Documents, GrepDocument{
			DocumentID: d.ID, Title: d.Title, Kind: d.Kind, Status: d.Status, Revision: d.Revision, Tags: d.Tags,
			UpdatedAt: d.UpdatedAt, TitleMatch: titleMatch, MatchCount: count, Matches: matches, MoreMatches: count > len(matches),
			Citation: fmt.Sprintf("kb://%s/documents/%s?revision=%d", in.ProjectID, d.ID, d.Revision),
		})
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if out.HasMore {
		next := in.Offset + len(out.Documents)
		out.NextOffset = &next
	}
	if out.ScanTruncated {
		out.Warning = fmt.Sprintf("Stopped after scanning %d documents; later (older) documents were not searched. Narrow with kind, tags, or status, or use a fixed-string pattern.", out.ScannedDocuments)
	}
	return out, nil
}

// grepLines returns the number of matching lines and up to max matches with
// their line numbers (1-based) and context. Long lines are cut to a window
// around the first match.
func grepLines(re *regexp.Regexp, content string, context, max int) (int, []GrepMatch) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	count, matches := 0, []GrepMatch{}
	for i, line := range lines {
		loc := re.FindStringIndex(line)
		if loc == nil {
			continue
		}
		count++
		if len(matches) == max {
			continue
		}
		m := GrepMatch{Line: i + 1, Text: clipLine(line, loc[0])}
		for j := max0(i - context); j < i; j++ {
			m.Before = append(m.Before, clipLine(lines[j], 0))
		}
		for j := i + 1; j <= i+context && j < len(lines); j++ {
			m.After = append(m.After, clipLine(lines[j], 0))
		}
		matches = append(matches, m)
	}
	return count, matches
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// clipLine shortens a line to grepMaxLineRunes, keeping the byte offset at in view.
func clipLine(line string, at int) string {
	runes := []rune(line)
	if len(runes) <= grepMaxLineRunes {
		return line
	}
	pos := utf8.RuneCountInString(line[:at])
	start := max0(pos - grepMaxLineRunes/3)
	if start+grepMaxLineRunes > len(runes) {
		start = len(runes) - grepMaxLineRunes
	}
	out := string(runes[start : start+grepMaxLineRunes])
	if start > 0 {
		out = "…" + out
	}
	if start+grepMaxLineRunes < len(runes) {
		out += "…"
	}
	return out
}
