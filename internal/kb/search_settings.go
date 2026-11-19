package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// SearchPolicy holds search settings a global administrator changes in the web
// console (운영 현황 → 검색 설정). The environment only provides the default for
// values nobody has set, like the retention policy.
type SearchPolicy struct {
	// IssueScoreFloor is the lowest vector similarity (cosine, 0-1) a question
	// or issue must reach to join search results. Measurements on bge-m3 put
	// the matching question at 0.62-0.65 and unrelated ones at 0.40-0.49, so
	// the default sits between them. 0 turns the floor off.
	IssueScoreFloor float64 `json:"issue_score_floor"`
}

const (
	defaultIssueScoreFloor = 0.5
	minIssueScoreFloor     = 0.1
	maxIssueScoreFloor     = 0.95
)

func validIssueScoreFloor(v float64) bool {
	return v == 0 || (v >= minIssueScoreFloor && v <= maxIssueScoreFloor)
}

// ParseSearchPolicy reads ISSUE_SCORE_FLOOR. Empty uses the built-in default.
func ParseSearchPolicy(issueScoreFloor string) (SearchPolicy, error) {
	p := SearchPolicy{IssueScoreFloor: defaultIssueScoreFloor}
	if issueScoreFloor == "" {
		return p, nil
	}
	v, err := strconv.ParseFloat(issueScoreFloor, 64)
	if err != nil || !validIssueScoreFloor(v) {
		return p, fmt.Errorf("ISSUE_SCORE_FLOOR must be 0 (no floor) or between %g and %g", minIssueScoreFloor, maxIssueScoreFloor)
	}
	p.IssueScoreFloor = v
	return p, nil
}

// effectiveSearch overlays the values an administrator saved in the web
// console on the server defaults (environment). sources tells, per value,
// whether it comes from "admin" or the "server" default.
func (s *Store) effectiveSearch(ctx context.Context, q Querier) (SearchPolicy, map[string]string, error) {
	p := s.Search
	if !validIssueScoreFloor(p.IssueScoreFloor) {
		p.IssueScoreFloor = defaultIssueScoreFloor
	}
	sources := map[string]string{"issue_score_floor": "server"}
	var floor sql.NullFloat64
	err := q.QueryRowContext(ctx, `SELECT issue_score_floor FROM search_settings`).Scan(&floor)
	if errors.Is(err, sql.ErrNoRows) {
		return p, sources, nil
	}
	if err != nil {
		return p, sources, err
	}
	if floor.Valid {
		p.IssueScoreFloor, sources["issue_score_floor"] = floor.Float64, "admin"
	}
	return p, sources, nil
}

// SearchSettingsStatus reports the active search settings for admins.
func (s *Store) SearchSettingsStatus(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "view_search_settings", "", false, true, func(tx *sql.Tx) (any, error) {
		policy, sources, err := s.effectiveSearch(ctx, tx)
		if err != nil {
			return nil, err
		}
		var updated sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM search_settings`).Scan(&updated); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		out := map[string]any{"policy": policy, "sources": sources, "defaults": s.Search,
			"limits": map[string]any{"issue_score_floor_min": minIssueScoreFloor, "issue_score_floor_max": maxIssueScoreFloor}}
		if updated.Valid {
			out["updated_at"] = updated.String
		}
		return out, nil
	})
}

// SearchSettingsInput is the web console form. A null (or missing) value goes
// back to the server default; 0 turns that floor off.
type SearchSettingsInput struct {
	IssueScoreFloor *float64 `json:"issue_score_floor"`
}

// SetSearchSettings saves the search settings. Only a global administrator in
// a web session may call it (Perform rejects keys for admin operations). The
// floor applies to the next search; it hides matches rather than deleting
// anything, so raising it too far is undone by lowering it again.
func (s *Store) SetSearchSettings(ctx context.Context, actor Identity, in SearchSettingsInput) (any, error) {
	ctx = auditInput(ctx, in)
	return s.Perform(ctx, actor, "update_search_settings", "", true, true, func(tx *sql.Tx) (any, error) {
		if in.IssueScoreFloor != nil && !validIssueScoreFloor(*in.IssueScoreFloor) {
			return nil, problem("INVALID_INPUT", fmt.Sprintf("issue_score_floor must be 0 (no floor) or between %g and %g.", minIssueScoreFloor, maxIssueScoreFloor))
		}
		if in.IssueScoreFloor == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM search_settings`); err != nil {
				return nil, err
			}
		} else if _, err := tx.ExecContext(ctx, `INSERT INTO search_settings(id,issue_score_floor,updated_by) VALUES(true,$1,$2)
			ON CONFLICT(id) DO UPDATE SET issue_score_floor=EXCLUDED.issue_score_floor,updated_by=EXCLUDED.updated_by,updated_at=now()`,
			in.IssueScoreFloor, actor.User.ID); err != nil {
			return nil, err
		}
		policy, sources, err := s.effectiveSearch(ctx, tx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"policy": policy, "sources": sources, "defaults": s.Search}, nil
	})
}

// aboveIssueScoreFloor keeps the vector hits reaching the floor. Keyword hits
// are not scored this way and are never dropped here.
func aboveIssueScoreFloor(hits []IssueSearchHit, floor float64) []IssueSearchHit {
	if floor <= 0 {
		return hits
	}
	kept := make([]IssueSearchHit, 0, len(hits))
	for _, h := range hits {
		if h.Score >= floor {
			kept = append(kept, h)
		}
	}
	return kept
}
