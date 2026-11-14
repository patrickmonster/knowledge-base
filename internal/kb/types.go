package kb

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

type Error struct{ Code, Message string }

func (e *Error) Error() string           { return e.Code + ": " + e.Message }
func problem(code, message string) error { return &Error{code, message} }
func PublicError(err error) string {
	var known *Error
	if errors.As(err, &known) {
		return known.Error()
	}
	return "INTERNAL_ERROR: The operation could not be completed. Please retry."
}

type User struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	GlobalRole string `json:"global_role"`
	Disabled   bool   `json:"disabled"`
}
type Project struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id"`
	Role        string `json:"role,omitempty"`
	CreatedAt   string `json:"created_at"`
}
type Document struct {
	ID        string   `json:"id"`
	ProjectID string   `json:"project_id"`
	Title     string   `json:"title"`
	Kind      string   `json:"kind"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
	Status    string   `json:"status"`
	SourceURL *string  `json:"source_url"`
	Revision  int      `json:"revision"`
	CreatedBy string   `json:"created_by"`
	UpdatedBy string   `json:"updated_by"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	// Links is attached by get_document for the current revision only; it is
	// link metadata, never part of a revision snapshot.
	Links *DocumentLinks `json:"links,omitempty"`
	// Recheck is attached by get_document for a stale current revision only.
	Recheck *Recheck `json:"recheck,omitempty"`
}
type DocumentInput struct {
	ProjectID string   `json:"project_id"`
	Title     string   `json:"title"`
	Kind      string   `json:"kind" jsonschema:"brief, decision, history, artifact, or reference"`
	Content   string   `json:"content" jsonschema:"Markdown text; maximum 100000 characters"`
	Tags      []string `json:"tags,omitempty"`
	Status    string   `json:"status,omitempty" jsonschema:"draft, active (default), or archived"`
	SourceURL string   `json:"source_url,omitempty"`
}
type DocumentPatch struct {
	Title     *string   `json:"title,omitempty"`
	Kind      *string   `json:"kind,omitempty"`
	Content   *string   `json:"content,omitempty"`
	Tags      *[]string `json:"tags,omitempty"`
	Status    *string   `json:"status,omitempty"`
	SourceURL *string   `json:"source_url,omitempty" jsonschema:"Set to an empty string to clear the source URL"`
}
type SearchHit struct {
	DocumentID string  `json:"document_id"`
	ProjectID  string  `json:"project_id"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	Revision   int     `json:"revision"`
	UpdatedBy  string  `json:"updated_by"`
	UpdatedAt  string  `json:"updated_at"`
	SourceURL  *string `json:"source_url"`
	Position   int     `json:"position"`
	Excerpt    string  `json:"excerpt"`
	Score      float64 `json:"score"`
	Citation   string  `json:"citation"`
	Truncated  bool    `json:"truncated,omitempty"`

	// Set only by search_across_projects so mixed results stay readable.
	ProjectSlug string `json:"project_slug,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	// SupersededBy lists active documents that replace this hit; such hits are
	// ranked after current knowledge.
	SupersededBy []SupersedingDocument `json:"superseded_by,omitempty"`
	// Recheck is set when the hit is an active reference/brief not updated for
	// DefaultStaleDays.
	Recheck *Recheck `json:"recheck,omitempty"`
}
type TaskContext struct {
	Project         Project          `json:"project"`
	Task            string           `json:"task"`
	Retrieval       string           `json:"retrieval"`
	Sources         []SearchHit      `json:"sources"`
	RecentDecisions []map[string]any `json:"recent_decisions"`
	Truncated       bool             `json:"excerpts_truncated"`
	Guidance        string           `json:"guidance"`
	Warning         string           `json:"warning,omitempty"`
	UnindexedChunks int              `json:"unindexed_chunks"`
	// Issues lists open questions and shared work areas relevant to the task
	// so the agent asks area owners before changing shared work.
	Issues          *TaskIssues      `json:"issues,omitempty"`
	// Rules are the project (owner/admin) and personal commit and work rules to follow.
	Rules *RuleSet `json:"rules,omitempty"`
}

type SearchResult struct {
	Hits            []SearchHit `json:"hits"`
	Retrieval       string      `json:"retrieval"`
	Warning         string      `json:"warning,omitempty"`
	UnindexedChunks int         `json:"unindexed_chunks"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

func validID(id string) error {
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return problem("INVALID_INPUT", "A canonical lowercase UUID is required.")
	}
	return nil
}
func bounded(label, s string, min, max int) error {
	n := utf8.RuneCountInString(strings.TrimSpace(s))
	if n < min || n > max {
		return problem("INVALID_INPUT", fmt.Sprintf("%s must have %d–%d characters.", label, min, max))
	}
	return nil
}
func validKind(s string) bool {
	switch s {
	case "brief", "decision", "history", "artifact", "reference":
		return true
	}
	return false
}
func validStatus(s string) bool { return s == "draft" || s == "active" || s == "archived" }
func validateDocument(in *DocumentInput) error {
	if err := validID(in.ProjectID); err != nil {
		return err
	}
	if err := bounded("title", in.Title, 1, 200); err != nil {
		return err
	}
	if err := bounded("content", in.Content, 1, 100000); err != nil {
		return err
	}
	if !validKind(in.Kind) {
		return problem("INVALID_INPUT", "Unknown document kind.")
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if !validStatus(in.Status) {
		return problem("INVALID_INPUT", "Unknown document status.")
	}
	in.Tags = normalizeTags(in.Tags)
	if len(in.Tags) > 20 {
		return problem("INVALID_INPUT", "At most 20 tags are allowed.")
	}
	for _, tag := range in.Tags {
		if err := bounded("tag", tag, 1, 50); err != nil {
			return err
		}
	}
	if in.SourceURL != "" {
		u, err := url.Parse(in.SourceURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || len(in.SourceURL) > 2000 {
			return problem("INVALID_INPUT", "source_url must be an HTTP(S) URL, up to 2000 bytes.")
		}
	}
	return nil
}
// maxPageLimit is the largest limit list and search tools accept.
const maxPageLimit = 100

func pagination(limit, offset int) (int, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > maxPageLimit || offset < 0 || offset > 100000 {
		return 0, problem("INVALID_INPUT", fmt.Sprintf("Invalid pagination: limit 1–%d; offset 0–100000.", maxPageLimit))
	}
	return limit, nil
}

// normalizeTags trims and NFC-normalizes tags, drops empty ones and removes
// duplicates case-insensitively, keeping the first spelling and order.
// Callers such as finish_task may add a tag the client also sent.
func normalizeTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(norm.NFC.String(tag))
		key := strings.ToLower(tag)
		if tag == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tag)
	}
	return out
}
