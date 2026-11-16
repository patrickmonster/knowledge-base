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
// deleted until a global administrator sets a period in the web console. The
// environment variables only provide the default for values nobody has set.
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
	out := map[string]int64{"audit_events": 0, "revisions": 0}
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		// One purge at a time across app instances.
		var locked bool
		if err := tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(51283168)").Scan(&locked); err != nil || !locked {
			return err
		}
		// Read the policy inside the lock so a change made in the web console
		// applies to the next run without a restart.
		p, _, err := s.effectiveRetention(ctx, tx)
		if err != nil {
			return err
		}
		if p.AuditDays == 0 && p.RevisionDays == 0 {
			return nil
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
			if _, err := s.PurgeSearchTimings(c); err != nil {
				slog.Error("search timing purge failed")
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
		policy, sources, err := s.effectiveRetention(ctx, tx)
		if err != nil {
			return nil, err
		}
		var updated sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM retention_settings`).Scan(&updated); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		out := map[string]any{"policy": policy, "sources": sources, "defaults": s.Retention, "last_purge": last, "data": oldest}
		if updated.Valid {
			out["updated_at"] = updated.String
		}
		return out, nil
	})
}

// effectiveRetention overlays the values an administrator saved in the web
// console on the server defaults (environment). sources tells, per value,
// whether it comes from "admin" or the "server" default.
func (s *Store) effectiveRetention(ctx context.Context, q Querier) (RetentionPolicy, map[string]string, error) {
	p := s.Retention
	if p.RevisionKeepLatest < 1 {
		p.RevisionKeepLatest = defaultRevisionKeep
	}
	sources := map[string]string{"audit_days": "server", "revision_days": "server", "revision_keep_latest": "server"}
	var audit, revision, keep sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT audit_days,revision_days,revision_keep_latest FROM retention_settings`).Scan(&audit, &revision, &keep)
	if errors.Is(err, sql.ErrNoRows) {
		return p, sources, nil
	}
	if err != nil {
		return p, sources, err
	}
	if audit.Valid {
		p.AuditDays, sources["audit_days"] = int(audit.Int64), "admin"
	}
	if revision.Valid {
		p.RevisionDays, sources["revision_days"] = int(revision.Int64), "admin"
	}
	if keep.Valid {
		p.RevisionKeepLatest, sources["revision_keep_latest"] = int(keep.Int64), "admin"
	}
	return p, sources, nil
}

// RetentionInput is the web console form. A null (or missing) value goes back
// to the server default; 0 days keeps that data forever.
type RetentionInput struct {
	AuditDays          *int `json:"audit_days"`
	RevisionDays       *int `json:"revision_days"`
	RevisionKeepLatest *int `json:"revision_keep_latest"`
}

func validRetentionDays(name string, v *int, min int) error {
	if v != nil && *v != 0 && (*v < min || *v > maxRetentionDays) {
		return problem("INVALID_INPUT", fmt.Sprintf("%s must be 0 (keep forever) or between %d and %d.", name, min, maxRetentionDays))
	}
	return nil
}

// SetRetention saves the retention policy. Only a global administrator in a
// web session may call it (Perform rejects keys for admin operations). Expired
// data is deleted by the hourly job, so a shorter period removes records that
// are already older than it within about an hour, and deleted records cannot be
// restored.
func (s *Store) SetRetention(ctx context.Context, actor Identity, in RetentionInput) (any, error) {
	ctx = auditInput(ctx, in)
	return s.Perform(ctx, actor, "update_retention", "", true, true, func(tx *sql.Tx) (any, error) {
		if err := validRetentionDays("audit_days", in.AuditDays, minAuditRetentionDays); err != nil {
			return nil, err
		}
		if err := validRetentionDays("revision_days", in.RevisionDays, minRevisionRetentionDays); err != nil {
			return nil, err
		}
		if in.RevisionKeepLatest != nil && (*in.RevisionKeepLatest < 1 || *in.RevisionKeepLatest > 1000) {
			return nil, problem("INVALID_INPUT", "revision_keep_latest must be between 1 and 1000.")
		}
		if in.AuditDays == nil && in.RevisionDays == nil && in.RevisionKeepLatest == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM retention_settings`); err != nil {
				return nil, err
			}
		} else if _, err := tx.ExecContext(ctx, `INSERT INTO retention_settings(id,audit_days,revision_days,revision_keep_latest,updated_by)
			VALUES(true,$1,$2,$3,$4)
			ON CONFLICT(id) DO UPDATE SET audit_days=EXCLUDED.audit_days,revision_days=EXCLUDED.revision_days,
				revision_keep_latest=EXCLUDED.revision_keep_latest,updated_by=EXCLUDED.updated_by,updated_at=now()`,
			in.AuditDays, in.RevisionDays, in.RevisionKeepLatest, actor.User.ID); err != nil {
			return nil, err
		}
		policy, sources, err := s.effectiveRetention(ctx, tx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"policy": policy, "sources": sources, "defaults": s.Retention}, nil
	})
}
