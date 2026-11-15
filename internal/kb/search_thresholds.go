package kb

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// HNSW review thresholds: when exact (full scan) vector search becomes slow
// enough that an approximate HNSW index is worth the work. The server sets
// defaults (HNSW_REVIEW_VECTOR_P95_MS, HNSW_REVIEW_SCANNED_CHUNKS) and a
// project owner or admin may override either value (set_search_thresholds).
// get_usage_stats compares the measured search_timings with them.

// HNSWThresholds holds the two review triggers. Zero fields mean "not set".
type HNSWThresholds struct {
	VectorP95MS   int `json:"vector_p95_ms" jsonschema:"Review when the 95th percentile exact vector scan time reaches this many milliseconds"`
	ScannedChunks int `json:"scanned_chunks" jsonschema:"Review when one exact vector scan compares this many chunks"`
}

const (
	defaultHNSWVectorP95MS   = 100
	defaultHNSWScannedChunks = 20000
	minHNSWVectorP95MS       = 1
	maxHNSWVectorP95MS       = 60000
	minHNSWScannedChunks     = 100
	maxHNSWScannedChunks     = 100000000
	// minHNSWVectorSamples is how many vector searches the window needs before
	// its p95 alone can call for a review; fewer samples are too noisy.
	minHNSWVectorSamples = 20
)

// DefaultHNSWThresholds are used when the server sets nothing.
var DefaultHNSWThresholds = HNSWThresholds{VectorP95MS: defaultHNSWVectorP95MS, ScannedChunks: defaultHNSWScannedChunks}

// ParseHNSWThresholds reads the server defaults; empty values keep the built-in defaults.
func ParseHNSWThresholds(vectorP95MS, scannedChunks string) (HNSWThresholds, error) {
	out := DefaultHNSWThresholds
	if vectorP95MS != "" {
		n, err := strconv.Atoi(vectorP95MS)
		if err != nil || n < minHNSWVectorP95MS || n > maxHNSWVectorP95MS {
			return out, fmt.Errorf("HNSW_REVIEW_VECTOR_P95_MS must be between %d and %d", minHNSWVectorP95MS, maxHNSWVectorP95MS)
		}
		out.VectorP95MS = n
	}
	if scannedChunks != "" {
		n, err := strconv.Atoi(scannedChunks)
		if err != nil || n < minHNSWScannedChunks || n > maxHNSWScannedChunks {
			return out, fmt.Errorf("HNSW_REVIEW_SCANNED_CHUNKS must be between %d and %d", minHNSWScannedChunks, maxHNSWScannedChunks)
		}
		out.ScannedChunks = n
	}
	return out, nil
}

func (s *Store) hnswDefaults() HNSWThresholds {
	d := s.HNSWReview
	if d.VectorP95MS == 0 {
		d.VectorP95MS = defaultHNSWVectorP95MS
	}
	if d.ScannedChunks == 0 {
		d.ScannedChunks = defaultHNSWScannedChunks
	}
	return d
}

// SearchThresholds is the effective setting of a project: each value with
// where it comes from ("project" override or "server" default).
type SearchThresholds struct {
	ProjectID           string         `json:"project_id"`
	VectorP95MS         int            `json:"vector_p95_ms"`
	VectorP95MSSource   string         `json:"vector_p95_ms_source" jsonschema:"project (override) or server (default)"`
	ScannedChunks       int            `json:"scanned_chunks"`
	ScannedChunksSource string         `json:"scanned_chunks_source" jsonschema:"project (override) or server (default)"`
	ServerDefault       HNSWThresholds `json:"server_default"`
	UpdatedAt           *string        `json:"updated_at,omitempty"`
}

func projectSearchThresholds(ctx context.Context, q Querier, projectID string, defaults HNSWThresholds) (SearchThresholds, error) {
	out := SearchThresholds{ProjectID: projectID, VectorP95MS: defaults.VectorP95MS, VectorP95MSSource: "server",
		ScannedChunks: defaults.ScannedChunks, ScannedChunksSource: "server", ServerDefault: defaults}
	var p95, chunks sql.NullInt64
	var updated sql.NullString
	err := q.QueryRowContext(ctx, `SELECT hnsw_vector_p95_ms,hnsw_scanned_chunks,to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM project_search_settings WHERE project_id=$1`, projectID).Scan(&p95, &chunks, &updated)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if p95.Valid {
		out.VectorP95MS, out.VectorP95MSSource = int(p95.Int64), "project"
	}
	if chunks.Valid {
		out.ScannedChunks, out.ScannedChunksSource = int(chunks.Int64), "project"
	}
	if updated.Valid {
		out.UpdatedAt = &updated.String
	}
	return out, nil
}

type SetSearchThresholdsInput struct {
	ProjectID     string `json:"project_id"`
	VectorP95MS   int    `json:"vector_p95_ms,omitempty" jsonschema:"Milliseconds, 1–60000; 0 or omitted uses the server default"`
	ScannedChunks int    `json:"scanned_chunks,omitempty" jsonschema:"Chunks, 100–100000000; 0 or omitted uses the server default"`
}

func setSearchThresholds(ctx context.Context, tx *sql.Tx, a Identity, in SetSearchThresholdsInput, defaults HNSWThresholds) (SearchThresholds, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true)
	if err != nil {
		return SearchThresholds{}, err
	}
	if p.Role != "owner" && p.Role != "admin" {
		return SearchThresholds{}, problem("FORBIDDEN", "Owner or admin access is required.")
	}
	if in.VectorP95MS != 0 && (in.VectorP95MS < minHNSWVectorP95MS || in.VectorP95MS > maxHNSWVectorP95MS) {
		return SearchThresholds{}, problem("INVALID_INPUT", fmt.Sprintf("vector_p95_ms must be 0 (server default) or %d–%d.", minHNSWVectorP95MS, maxHNSWVectorP95MS))
	}
	if in.ScannedChunks != 0 && (in.ScannedChunks < minHNSWScannedChunks || in.ScannedChunks > maxHNSWScannedChunks) {
		return SearchThresholds{}, problem("INVALID_INPUT", fmt.Sprintf("scanned_chunks must be 0 (server default) or %d–%d.", minHNSWScannedChunks, maxHNSWScannedChunks))
	}
	if in.VectorP95MS == 0 && in.ScannedChunks == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_search_settings WHERE project_id=$1`, p.ID); err != nil {
			return SearchThresholds{}, err
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO project_search_settings(project_id,hnsw_vector_p95_ms,hnsw_scanned_chunks,updated_by)
		VALUES($1,NULLIF($2,0),NULLIF($3,0),$4)
		ON CONFLICT(project_id) DO UPDATE SET hnsw_vector_p95_ms=EXCLUDED.hnsw_vector_p95_ms,hnsw_scanned_chunks=EXCLUDED.hnsw_scanned_chunks,
			updated_by=EXCLUDED.updated_by,updated_at=now()`, p.ID, in.VectorP95MS, in.ScannedChunks, a.User.ID); err != nil {
		return SearchThresholds{}, err
	}
	return projectSearchThresholds(ctx, tx, p.ID, defaults)
}

// UsageHNSWReview compares the window's exact vector searches with the
// project's thresholds.
type UsageHNSWReview struct {
	Thresholds       SearchThresholds `json:"thresholds"`
	VectorSearches   int              `json:"vector_searches" jsonschema:"Searches in the window that ran an exact vector scan"`
	P95VectorMS      *float64         `json:"p95_vector_ms" jsonschema:"95th percentile vector scan time over all retrieval paths; null without vector searches"`
	MaxScannedChunks int              `json:"max_scanned_chunks"`
	VectorExceeded   bool             `json:"vector_exceeded"`
	ChunksExceeded   bool             `json:"chunks_exceeded"`
	Status           string           `json:"status" jsonschema:"review (a threshold is reached), ok, or insufficient_data (fewer than 20 vector searches and no chunk threshold reached)"`
	MinSamples       int              `json:"min_samples" jsonschema:"Vector searches needed before p95_vector_ms alone can call for a review"`
}

func hnswReview(t SearchThresholds, searches int, p95 *float64, maxChunks int) UsageHNSWReview {
	r := UsageHNSWReview{Thresholds: t, VectorSearches: searches, P95VectorMS: p95, MaxScannedChunks: maxChunks, MinSamples: minHNSWVectorSamples}
	r.VectorExceeded = p95 != nil && *p95 >= float64(t.VectorP95MS)
	r.ChunksExceeded = maxChunks >= t.ScannedChunks
	switch {
	case r.ChunksExceeded || (r.VectorExceeded && searches >= minHNSWVectorSamples):
		r.Status = "review"
	case searches < minHNSWVectorSamples:
		r.Status = "insufficient_data"
	default:
		r.Status = "ok"
	}
	return r
}

func registerSearchThresholdTools(t Tools, s *Store) {
	add(t, s, "set_search_thresholds", "Owner or admin: set the project's HNSW review thresholds that get_usage_stats (hnsw_review) compares search times with: vector_p95_ms (95th percentile exact vector scan time) and scanned_chunks (chunks one scan compares). Each call replaces both values; 0 or omitted uses the server default (HNSW_REVIEW_VECTOR_P95_MS, HNSW_REVIEW_SCANNED_CHUNKS). Returns the effective thresholds.", true,
		func(in SetSearchThresholdsInput) string { return in.ProjectID },
		func(ctx context.Context, tx *sql.Tx, a Identity, in SetSearchThresholdsInput) (any, error) {
			return setSearchThresholds(ctx, tx, a, in, s.hnswDefaults())
		})
}
