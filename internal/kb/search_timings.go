package kb

import (
	"context"
	"database/sql"
	"time"
)

// Search latency samples (검색 시간 측정) show when exact vector search gets
// slow enough to need an approximate (HNSW) index. Each project-scoped search
// stores its phase times and the number of embedded chunks the exact scan
// compared, without query text or user. Rows share search_misses' retention.

const (
	searchSourceKnowledge = "search_knowledge"
	searchSourceTask      = "task_context"
)

// searchTiming holds per-phase milliseconds; nil means the phase did not run.
type searchTiming struct {
	Mode          string // requested mode after defaulting
	Embed         *float64
	Vector        *float64
	Lexical       *float64
	ScannedChunks int
}

// since returns the elapsed milliseconds from start, at microsecond resolution.
func since(start time.Time) *float64 {
	ms := float64(time.Since(start).Microseconds()) / 1000
	return &ms
}

func recordSearchTiming(ctx context.Context, tx *sql.Tx, projectID, source string, out SearchResult, total *float64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO search_timings(project_id,source,mode,retrieval,total_ms,embed_ms,vector_ms,lexical_ms,scanned_chunks,hits)
	VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, projectID, source, out.timing.Mode, out.Retrieval, *total,
		out.timing.Embed, out.timing.Vector, out.timing.Lexical, out.timing.ScannedChunks, len(out.Hits))
	return err
}

// PurgeSearchTimings deletes expired latency samples in one bounded batch.
func (s *Store) PurgeSearchTimings(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM search_timings WHERE id IN (
		SELECT id FROM search_timings WHERE created_at<now()-make_interval(days=>$1) ORDER BY id LIMIT $2)`, s.searchMissRetentionDays(), retentionBatch)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
