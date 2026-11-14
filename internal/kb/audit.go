package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Identity struct {
	User        User
	KeyID       string
	ProjectID   string
	Access      string
	SessionHash string
	CSRF        string
}
type Event struct {
	ActorID, Action, ProjectID, TargetID, Outcome, ErrorCode, RequestID string
	DurationMS                                                          int64
	Details                                                             map[string]any
}

type auditInputKey struct{}

// Only identifiers and permission changes are copied. Never copy content, credentials or tokens.
func auditInput(ctx context.Context, input any) context.Context {
	raw, _ := json.Marshal(input)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	safe := map[string]any{}
	for _, key := range []string{"attachment_id", "document_id", "task_id", "user_id", "id", "reset_user_id", "role", "global_role", "disabled", "access", "expected_revision", "decision", "source_id", "target_id", "relation"} {
		if value, ok := fields[key]; ok {
			if text, ok := value.(string); ok && len(text) > 100 {
				continue
			}
			safe[key] = value
		}
	}
	return context.WithValue(ctx, auditInputKey{}, safe)
}

func audit(ctx context.Context, q Querier, e Event) error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	details, _ := json.Marshal(e.Details)
	_, err := q.ExecContext(ctx, `INSERT INTO audit_events(actor_id,action,project_id,target_id,outcome,error_code,request_id,duration_ms,details)
		VALUES(NULLIF($1,'')::uuid,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9)`,
		e.ActorID, e.Action, e.ProjectID, e.TargetID, e.Outcome, e.ErrorCode, e.RequestID, e.DurationMS, details)
	return err
}
func (s *Store) Record(ctx context.Context, e Event) error { return audit(ctx, s.DB, e) }
func errorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "INTERNAL_ERROR"
}

// Every authorized operation and its successful audit entry commit together.
// Failed operations roll back, then record a separate denial/error event.
func (s *Store) Perform(ctx context.Context, identity Identity, action, projectID string, write, admin bool,
	fn func(*sql.Tx) (any, error)) (any, error) {
	started := time.Now()
	e := Event{ActorID: identity.User.ID, Action: action, ProjectID: projectID, RequestID: uuid.NewString(), Outcome: "success"}
	if details, ok := ctx.Value(auditInputKey{}).(map[string]any); ok {
		e.Details = details
		for _, key := range []string{"attachment_id", "document_id", "task_id", "user_id", "id", "reset_user_id"} {
			if id, ok := details[key].(string); ok && id != "" {
				e.TargetID = id
				break
			}
		}
	}
	if projectID != "" && validID(projectID) != nil {
		e.ProjectID = ""
	}
	var result any
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		var disabled bool
		var role string
		// Keep suspension and role changes ordered against in-flight operations.
		if err := tx.QueryRowContext(ctx, "SELECT disabled,global_role FROM users WHERE id=$1 FOR SHARE", identity.User.ID).Scan(&disabled, &role); err != nil {
			return err
		}
		if disabled {
			return problem("UNAUTHORIZED", "Account is disabled.")
		}
		if identity.KeyID != "" {
			var keyAccess string
			var scope sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT access,project_id::text FROM api_keys WHERE id=$1 AND user_id=$2
				AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now()) FOR SHARE`, identity.KeyID, identity.User.ID).Scan(&keyAccess, &scope)
			if errors.Is(err, sql.ErrNoRows) {
				return problem("UNAUTHORIZED", "Key is expired or revoked.")
			}
			if err != nil {
				return err
			}
			if admin || (write && keyAccess == "read_only") {
				return problem("FORBIDDEN", "This key cannot perform this operation.")
			}
			if scope.Valid && ((projectID != "" && projectID != scope.String) || action == "create_project") {
				return problem("FORBIDDEN", "This key is restricted to another project.")
			}
		} else {
			var active bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=$1 AND user_id=$2 AND expires_at>now())`, identity.SessionHash, identity.User.ID).Scan(&active); err != nil {
				return err
			}
			if !active {
				return problem("UNAUTHORIZED", "Please sign in again.")
			}
		}
		if admin && role != "admin" {
			return problem("FORBIDDEN", "Administrator access is required.")
		}
		var err error
		result, err = fn(tx)
		if err != nil {
			return err
		}
		if e.TargetID == "" {
			switch out := result.(type) {
			case Attachment:
				e.TargetID = out.ID
			case StartTaskResult:
				e.TargetID = out.Run.ID
			case Document:
				e.TargetID = out.ID
			case Project:
				e.TargetID = out.ID
				e.ProjectID = out.ID
			case map[string]any:
				for _, key := range []string{"user_id", "id"} {
					if id, ok := out[key].(string); ok {
						e.TargetID = id
						break
					}
				}
			}
		}
		e.DurationMS = time.Since(started).Milliseconds()
		return audit(ctx, tx, e)
	})
	if err != nil {
		e.ErrorCode = errorCode(err)
		e.Outcome = "denied"
		if e.ErrorCode == "INTERNAL_ERROR" {
			e.Outcome = "error"
		}
		e.DurationMS = time.Since(started).Milliseconds()
		logctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if logerr := audit(logctx, s.DB, e); logerr != nil {
			return nil, errors.Join(err, logerr)
		}
	}
	return result, err
}

func projectAccess(ctx context.Context, q Querier, userID, projectID string, write bool) (Project, error) {
	if err := validID(projectID); err != nil {
		return Project{}, err
	}
	p, err := one[Project](ctx, q, `SELECT to_jsonb(p)||jsonb_build_object('role',m.role) FROM projects p
		JOIN project_members m ON m.project_id=p.id WHERE p.id=$1 AND m.user_id=$2 FOR SHARE OF m`, projectID, userID)
	if err != nil {
		return p, notFound(err)
	}
	if write && p.Role == "viewer" {
		return p, problem("FORBIDDEN", "Editor or owner access is required.")
	}
	return p, nil
}
