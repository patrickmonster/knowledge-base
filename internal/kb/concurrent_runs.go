package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// start_task can name the branch, worktree and paths a task works in. Other
// open tasks of the project on the same branch or worktree, or on overlapping
// paths, are returned as context.concurrent_runs. This only warns: nothing is
// blocked, and tasks that name none of the three behave as before.

const (
	maxScopeBranch   = 200
	maxScopeWorktree = 500
	maxScopePath     = 300
	maxScopePaths    = 20
	// concurrentStaleDays marks an open task as stale in a warning; it is still shown.
	concurrentStaleDays = 7
	// maxConcurrentRuns bounds the warning list, and maxConcurrentScan the open tasks compared.
	maxConcurrentRuns = 10
	maxConcurrentScan = 200
)

// TaskScope is the optional branch, worktree and paths of a task, normalized.
type TaskScope struct {
	Branch   string
	Worktree string
	Paths    []string
}

func (s TaskScope) empty() bool { return s.Branch == "" && s.Worktree == "" && len(s.Paths) == 0 }

// ConcurrentOverlap is one item a task shares with another open task.
type ConcurrentOverlap struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Theirs string `json:"theirs,omitempty"`
}

// ConcurrentRun is another open task working on the same ground.
type ConcurrentRun struct {
	TaskID        string              `json:"task_id"`
	Title         string              `json:"title"`
	CreatedBy     string              `json:"created_by"`
	CreatedByName string              `json:"created_by_name"`
	StartedAt     string              `json:"started_at"`
	AgeDays       int                 `json:"age_days"`
	Stale         bool                `json:"stale"`
	Overlaps      []ConcurrentOverlap `json:"overlaps"`
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// normalizeScope validates and normalizes the optional start_task scope fields.
func normalizeScope(branch, worktree string, paths []string) (TaskScope, error) {
	var out TaskScope
	out.Branch = strings.TrimPrefix(strings.TrimSpace(branch), "refs/heads/")
	out.Worktree = strings.TrimSpace(worktree)
	if out.Branch != "" {
		if err := bounded("branch", out.Branch, 1, maxScopeBranch); err != nil {
			return out, err
		}
		if hasControl(out.Branch) || strings.ContainsAny(out.Branch, " \\") {
			return out, problem("INVALID_INPUT", "branch must not contain spaces, backslashes or control characters.")
		}
	}
	if out.Worktree != "" {
		if err := bounded("worktree", out.Worktree, 1, maxScopeWorktree); err != nil {
			return out, err
		}
		if hasControl(out.Worktree) {
			return out, problem("INVALID_INPUT", "worktree must not contain control characters.")
		}
		out.Worktree = path.Clean(strings.ReplaceAll(out.Worktree, "\\", "/"))
	}
	if len(paths) > maxScopePaths {
		return out, problem("INVALID_INPUT", "At most 20 paths are allowed.")
	}
	seen := map[string]bool{}
	for _, p := range paths {
		raw := strings.TrimSpace(p)
		if n := utf8.RuneCountInString(raw); n < 1 || n > maxScopePath {
			return out, problem("INVALID_INPUT", "Each path must have 1–300 characters.")
		}
		if hasControl(raw) {
			return out, problem("INVALID_INPUT", "paths must not contain control characters.")
		}
		raw = strings.ReplaceAll(raw, "\\", "/")
		for _, seg := range strings.Split(raw, "/") {
			if seg == ".." {
				return out, problem("INVALID_INPUT", "paths must not contain '..'.")
			}
		}
		// A leading slash is ignored and "." names the whole repository.
		clean := strings.TrimPrefix(path.Clean(raw), "/")
		if clean == "" {
			clean = "."
		}
		if !seen[clean] {
			seen[clean] = true
			out.Paths = append(out.Paths, clean)
		}
	}
	return out, nil
}

// pathsOverlap reports whether a and b are equal or one contains the other
// at a path-segment boundary ("internal/kb" contains "internal/kb/a.go" but
// not "internal/kbx"). "." contains every path.
func pathsOverlap(a, b string) bool {
	if a == b || a == "." || b == "." {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// scopeOverlaps lists what two scopes share.
func scopeOverlaps(mine, theirs TaskScope) []ConcurrentOverlap {
	out := []ConcurrentOverlap{}
	if mine.Branch != "" && mine.Branch == theirs.Branch {
		out = append(out, ConcurrentOverlap{Kind: "branch", Value: mine.Branch})
	}
	if mine.Worktree != "" && mine.Worktree == theirs.Worktree {
		out = append(out, ConcurrentOverlap{Kind: "worktree", Value: mine.Worktree})
	}
	for _, a := range mine.Paths {
		for _, b := range theirs.Paths {
			if pathsOverlap(a, b) {
				out = append(out, ConcurrentOverlap{Kind: "path", Value: a, Theirs: b})
			}
		}
	}
	return out
}

// concurrentRuns finds other open tasks of the project sharing the branch,
// worktree or a path with the new task. The task itself is excluded.
func concurrentRuns(ctx context.Context, tx *sql.Tx, projectID, taskID string, mine TaskScope) ([]ConcurrentRun, error) {
	if mine.empty() {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.title,r.created_by,u.name,to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),floor(extract(epoch FROM now()-r.started_at)/86400)::int,r.branch,r.worktree,r.paths
		FROM task_runs r JOIN users u ON u.id=r.created_by
		WHERE r.project_id=$1 AND r.id<>$2 AND r.status='open' AND (r.branch<>'' OR r.worktree<>'' OR jsonb_array_length(r.paths)>0)
		ORDER BY r.started_at DESC,r.id LIMIT $3`, projectID, taskID, maxConcurrentScan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConcurrentRun
	for rows.Next() {
		var c ConcurrentRun
		var theirs TaskScope
		var raw []byte
		if err := rows.Scan(&c.TaskID, &c.Title, &c.CreatedBy, &c.CreatedByName, &c.StartedAt, &c.AgeDays, &theirs.Branch, &theirs.Worktree, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &theirs.Paths); err != nil {
			return nil, err
		}
		if c.Overlaps = scopeOverlaps(mine, theirs); len(c.Overlaps) == 0 {
			continue
		}
		c.Stale = c.AgeDays >= concurrentStaleDays
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > maxConcurrentRuns {
		out = out[:maxConcurrentRuns]
	}
	return out, nil
}
