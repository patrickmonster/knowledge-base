package kb

import (
	"context"
	"database/sql"
	"strings"
)

// Zero-result searches (결과 0건 검색어) show knowledge the team looks for but
// has not written. Only search_knowledge queries are recorded: the query text
// is normalized and cut to 200 characters, the searching user is not stored,
// and rows are purged after searchMissMaxDays (or AUDIT_RETENTION_DAYS when shorter).

const (
	searchMissMaxRunes = 200
	searchMissMaxDays  = 365
)

// normalizeSearchMiss collapses whitespace and lowercases so the same query
// typed differently groups together.
func normalizeSearchMiss(query string) string {
	q := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if r := []rune(q); len(r) > searchMissMaxRunes {
		q = strings.TrimSpace(string(r[:searchMissMaxRunes]))
	}
	return q
}

func recordSearchMiss(ctx context.Context, tx *sql.Tx, projectID, query, mode string) error {
	q := normalizeSearchMiss(query)
	if q == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO search_misses(project_id,query,mode) VALUES($1,$2,$3)`, projectID, q, mode)
	return err
}

// searchMissRetentionDays is the shorter of the fixed limit and the audit retention.
func (s *Store) searchMissRetentionDays() int {
	if d := s.Retention.AuditDays; d > 0 && d < searchMissMaxDays {
		return d
	}
	return searchMissMaxDays
}

// PurgeSearchMisses deletes expired zero-result searches in one bounded batch.
func (s *Store) PurgeSearchMisses(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM search_misses WHERE id IN (
		SELECT id FROM search_misses WHERE created_at<now()-make_interval(days=>$1) ORDER BY id LIMIT $2)`, s.searchMissRetentionDays(), retentionBatch)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
