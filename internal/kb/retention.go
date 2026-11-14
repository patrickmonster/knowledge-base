package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// RetentionPolicy limits how long audit events and old document revisions are
// kept. A zero day count keeps that data forever (the default), so nothing is
// deleted until an operator opts in with environment variables.
type RetentionPolicy struct {
	AuditDays          int `json:"audit_days"`
	RevisionDays       int `json:"revision_days"`
	RevisionKeepLatest int `json:"revision_keep_latest"`
}

const (
	minAuditRetentionDays    = 90
	minRevisionRetentionDays = 30
	maxRetentionDays         = 3650
	defaultRevisionKeep      = 20
	retentionBatch           = 5000
)

// ParseRetention reads AUDIT_RETENTION_DAYS, REVISION_RETENTION_DAYS and
// REVISION_KEEP_LATEST values. Empty or "0" days disables that purge.
func ParseRetention(auditDays, revisionDays, keepLatest string) (RetentionPolicy, error) {
	p := RetentionPolicy{RevisionKeepLatest: defaultRevisionKeep}
	var err error
	if p.AuditDays, err = retentionDays("AUDIT_RETENTION_DAYS", auditDays, minAuditRetentionDays); err != nil {
		return p, err
	}
	if p.RevisionDays, err = retentionDays("REVISION_RETENTION_DAYS", revisionDays, minRevisionRetentionDays); err != nil {
		return p, err
	}
	if keepLatest != "" {
		n, err := strconv.Atoi(keepLatest)
		if err != nil || n < 1 || n > 1000 {
			return p, errors.New("REVISION_KEEP_LATEST must be between 1 and 1000")
		}
		p.RevisionKeepLatest = n
	}
	return p, nil
}

func retentionDays(name, raw string, min int) (int, error) {
	if raw == "" || raw == "0" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > maxRetentionDays {
		return 0, fmt.Errorf("%s must be 0 (keep forever) or between %d and %d", name, min, maxRetentionDays)
	}
	return n, nil
}

// ApplyRetention deletes expired audit events and revisions in bounded batches.
// Revisions are never deleted when they are a document's current revision, one
// of its latest RevisionKeepLatest revisions, or pinned by an attachment.
// Each run that deletes anything records a retention_purge audit event with
// counts only.
func (s *Store) ApplyRetention(ctx context.Context) (map[string]int64, error) {
	p := s.Retention
	out := map[string]int64{"audit_events": 0, "revisions": 0}
	if p.AuditDays == 0 && p.RevisionDays == 0 {
		return out, nil
	}
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		// One purge at a time across app instances.
		var locked bool
		if err := tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(51283168)").Scan(&locked); err != nil || !locked {
			return err
		}
		if p.RevisionDays > 0 {
			keep := p.RevisionKeepLatest
			if keep < 1 {
				keep = defaultRevisionKeep
			}
			res, err := tx.ExecContext(ctx, `DELETE FROM revisions r USING (
				SELECT r.document_id,r.revision FROM revisions r JOIN documents d ON d.id=r.document_id
				WHERE r.created_at<now()-make_interval(days=>$1) AND r.revision<d.revision-$2+1
				AND NOT EXISTS(SELECT 1 FROM attachments a WHERE a.document_id=r.document_id AND a.document_revision=r.revision)
				LIMIT $3) old WHERE r.document_id=old.document_id AND r.revision=old.revision`, p.RevisionDays, keep, retentionBatch)
			if err != nil {
				return err
			}
			out["revisions"], _ = res.RowsAffected()
		}
		if p.AuditDays > 0 {
			res, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE id IN (
				SELECT id FROM audit_events WHERE created_at<now()-make_interval(days=>$1) ORDER BY id LIMIT $2)`, p.AuditDays, retentionBatch)
			if err != nil {
				return err
			}
			out["audit_events"], _ = res.RowsAffected()
		}
		if out["audit_events"] == 0 && out["revisions"] == 0 {
			return nil
		}
		return audit(ctx, tx, Event{Action: "retention_purge", Outcome: "success", RequestID: uuid.NewString(),
			Details: map[string]any{"audit_events": out["audit_events"], "revisions": out["revisions"], "audit_days": p.AuditDays, "revision_days": p.RevisionDays, "revision_keep_latest": p.RevisionKeepLatest}})
	})
	return out, err
}

// RunRetention purges hourly; batches keep each transaction short, so a large
// backlog drains over several runs.
func (s *Store) RunRetention(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, cancel := context.WithTimeout(ctx, time.Minute)
			if _, err := s.ApplyRetention(c); err != nil {
				slog.Error("retention purge failed")
			}
			if _, err := s.PurgeSearchMisses(c); err != nil {
				slog.Error("search miss purge failed")
			}
			cancel()
		}
	}
}

// RetentionStatus reports the active policy and the last purge for admins.
func (s *Store) RetentionStatus(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "view_retention", "", false, true, func(tx *sql.Tx) (any, error) {
		last, err := one[map[string]any](ctx, tx, `SELECT coalesce((SELECT jsonb_build_object('at',created_at,'details',details)
			FROM audit_events WHERE action='retention_purge' ORDER BY id DESC LIMIT 1),'null'::jsonb)`)
		if err != nil {
			return nil, err
		}
		oldest, err := one[map[string]any](ctx, tx, `SELECT jsonb_build_object('oldest_audit_event',(SELECT min(created_at) FROM audit_events),
			'oldest_revision',(SELECT min(created_at) FROM revisions),'audit_events',(SELECT count(*) FROM audit_events),'revisions',(SELECT count(*) FROM revisions))`)
		if err != nil {
			return nil, err
		}
		return map[string]any{"policy": s.Retention, "last_purge": last, "data": oldest}, nil
	})
}
