package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

func decodeUser(raw []byte) (u User, err error) { err = json.Unmarshal(raw, &u); return }

type InviteInput struct {
	Email       string `json:"email"`
	Name        string `json:"name"`
	ResetUserID string `json:"reset_user_id,omitempty"`
}

func (s *Store) Invite(ctx context.Context, actor Identity, in InviteInput) (any, error) {
	ctx = auditInput(ctx, in)
	return s.Perform(ctx, actor, "invite_user", "", true, true, func(tx *sql.Tx) (any, error) {
		id := in.ResetUserID
		purpose := "reset"
		if id == "" {
			email, err := emailAddress(in.Email)
			if err != nil {
				return nil, err
			}
			if err = bounded("name", in.Name, 1, 100); err != nil {
				return nil, err
			}
			id = uuid.NewString()
			purpose = "invite"
			result, err := tx.ExecContext(ctx, `INSERT INTO users(id,email,name) VALUES($1,$2,$3) ON CONFLICT(email) DO NOTHING`, id, email, strings.TrimSpace(in.Name))
			if err != nil {
				return nil, err
			}
			n, _ := result.RowsAffected()
			if n == 0 {
				return nil, problem("CONFLICT", "This email is already registered. Use password reset instead.")
			}
		} else {
			if err := validID(id); err != nil {
				return nil, err
			}
			var disabled bool
			if err := tx.QueryRowContext(ctx, "SELECT disabled FROM users WHERE id=$1 FOR UPDATE", id).Scan(&disabled); err != nil {
				return nil, notFound(err)
			}
			if disabled {
				return nil, problem("FORBIDDEN", "Enable the account before issuing a reset link.")
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE invitations SET used_at=now() WHERE user_id=$1 AND used_at IS NULL", id); err != nil {
			return nil, err
		}
		raw := token()
		_, err := tx.ExecContext(ctx, `INSERT INTO invitations(token_hash,user_id,purpose,expires_at,created_by)
			VALUES($1,$2,$3,now()+interval '24 hours',$4)`, digest(raw), id, purpose, actor.User.ID)
		return map[string]any{"user_id": id, "invitation_token": raw, "expires_in_hours": 24}, err
	})
}

type UserUpdate struct {
	UserID     string `json:"user_id"`
	Disabled   bool   `json:"disabled"`
	GlobalRole string `json:"global_role"`
}

func (s *Store) UpdateUser(ctx context.Context, actor Identity, in UserUpdate) (any, error) {
	ctx = auditInput(ctx, in)
	return s.Perform(ctx, actor, "update_user", "", true, true, func(tx *sql.Tx) (any, error) {
		if err := validID(in.UserID); err != nil {
			return nil, err
		}
		if in.UserID == actor.User.ID {
			return nil, problem("FORBIDDEN", "Ask another administrator to change your own role or status.")
		}
		if in.GlobalRole != "admin" && in.GlobalRole != "member" {
			return nil, problem("INVALID_INPUT", "Unknown global role.")
		}
		result, err := tx.ExecContext(ctx, "UPDATE users SET disabled=$1,global_role=$2 WHERE id=$3", in.Disabled, in.GlobalRole, in.UserID)
		if err != nil {
			return nil, err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return nil, problem("NOT_FOUND", "User not found.")
		}
		if err := revokeCredentials(ctx, tx, in.UserID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE invitations SET used_at=now() WHERE user_id=$1 AND used_at IS NULL", in.UserID); err != nil {
			return nil, err
		}
		return map[string]any{"user_id": in.UserID, "disabled": in.Disabled, "global_role": in.GlobalRole}, nil
	})
}

type KeyInput struct {
	Name         string `json:"name"`
	Access       string `json:"access"`
	ProjectID    string `json:"project_id,omitempty"`
	Days         int    `json:"days,omitempty"`
	NeverExpires bool   `json:"never_expires,omitempty"`
}

func (s *Store) CreateKey(ctx context.Context, actor Identity, in KeyInput) (any, error) {
	ctx = auditInput(ctx, in)
	return s.Perform(ctx, actor, "create_api_key", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		if actor.KeyID != "" {
			return nil, problem("FORBIDDEN", "Sign in to manage keys.")
		}
		if err := bounded("key name", in.Name, 1, 100); err != nil {
			return nil, err
		}
		if in.Access != "read_only" && in.Access != "read_write" {
			return nil, problem("INVALID_INPUT", "Choose read_only or read_write.")
		}
		if in.NeverExpires && in.Days != 0 {
			return nil, problem("INVALID_INPUT", "Choose either no expiration or a lifetime in days.")
		}
		if !in.NeverExpires && in.Days == 0 {
			in.Days = 30
		}
		if !in.NeverExpires && (in.Days < 1 || in.Days > 90) {
			return nil, problem("INVALID_INPUT", "Key lifetime must be 1–90 days.")
		}
		if in.ProjectID != "" {
			if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, false); err != nil {
				return nil, err
			}
		}
		raw := "kb_" + token()
		id := uuid.NewString()
		_, err := tx.ExecContext(ctx, `INSERT INTO api_keys(id,user_id,token_hash,name,prefix,access,project_id,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,
			CASE WHEN $9 THEN NULL ELSE now()+make_interval(days=>$8) END)`, id, actor.User.ID, digest(raw), in.Name, raw[:10], in.Access, in.ProjectID, in.Days, in.NeverExpires)
		var expiresInDays any = in.Days
		if in.NeverExpires {
			expiresInDays = nil
		}
		return map[string]any{"id": id, "token": raw, "name": in.Name, "expires_in_days": expiresInDays, "never_expires": in.NeverExpires}, err
	})
}
func (s *Store) RevokeKey(ctx context.Context, actor Identity, id string) (any, error) {
	ctx = auditInput(ctx, map[string]any{"id": id})
	return s.Perform(ctx, actor, "revoke_api_key", "", true, false, func(tx *sql.Tx) (any, error) {
		if actor.KeyID != "" {
			return nil, problem("FORBIDDEN", "Sign in to manage keys.")
		}
		if err := validID(id); err != nil {
			return nil, err
		}
		r, err := tx.ExecContext(ctx, `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND (user_id=$2 OR
			EXISTS(SELECT 1 FROM users WHERE id=$2 AND global_role='admin'))`, id, actor.User.ID)
		if err != nil {
			return nil, err
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return nil, problem("NOT_FOUND", "Key not found.")
		}
		return map[string]any{"id": id, "revoked": true}, nil
	})
}
func (s *Store) ListKeys(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "list_api_keys", "", false, false, func(tx *sql.Tx) (any, error) {
		return many[map[string]any](ctx, tx, `SELECT jsonb_build_object('id',k.id,'user_id',k.user_id,'user_name',u.name,
		'name',k.name,'prefix',k.prefix,'access',k.access,'project_id',k.project_id,'expires_at',k.expires_at,'revoked_at',k.revoked_at)
		FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.user_id=$1 OR EXISTS(SELECT 1 FROM users WHERE id=$1 AND global_role='admin')
		ORDER BY k.created_at DESC LIMIT 200`, actor.User.ID)
	})
}
func (s *Store) Users(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "list_users", "", false, true, func(tx *sql.Tx) (any, error) {
		return many[map[string]any](ctx, tx, `SELECT jsonb_build_object('id',id,'email',email,'name',name,'global_role',global_role,
		'disabled',disabled,'activated',password_hash<>'',
		'mfa_enabled',EXISTS(SELECT 1 FROM user_mfa m WHERE m.user_id=users.id AND m.enabled_at IS NOT NULL)) FROM users ORDER BY created_at DESC LIMIT 200`)
	})
}

type AuditFilter struct {
	ActorID   string `json:"actor_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	BeforeID  int64  `json:"before_id,omitempty"`
}

func (s *Store) AuditLog(ctx context.Context, actor Identity, f AuditFilter) (any, error) {
	return s.Perform(ctx, actor, "view_audit", "", false, true, func(tx *sql.Tx) (any, error) {
		for _, id := range []string{f.ActorID, f.ProjectID} {
			if id != "" {
				if err := validID(id); err != nil {
					return nil, err
				}
			}
		}
		if f.Outcome != "" && f.Outcome != "success" && f.Outcome != "denied" && f.Outcome != "error" {
			return nil, problem("INVALID_INPUT", "Invalid outcome.")
		}
		return many[map[string]any](ctx, tx, `SELECT to_jsonb(a)||jsonb_build_object('actor_name',u.name) FROM audit_events a
		LEFT JOIN users u ON u.id=a.actor_id WHERE ($1='' OR a.actor_id=NULLIF($1,'')::uuid)
		AND ($2='' OR a.project_id=NULLIF($2,'')::uuid) AND ($3='' OR a.outcome=$3) AND ($4::bigint=0 OR a.id<$4)
		ORDER BY a.id DESC LIMIT 100`, f.ActorID, f.ProjectID, f.Outcome, f.BeforeID)
	})
}
func (s *Store) Stats(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "view_stats", "", false, true, func(tx *sql.Tx) (any, error) {
		return one[map[string]any](ctx, tx, `SELECT jsonb_build_object(
		'active_users',(SELECT count(*) FROM users WHERE NOT disabled AND password_hash<>''),
		'projects',(SELECT count(*) FROM projects),
		'active_keys',(SELECT count(*) FROM api_keys k JOIN users u ON u.id=k.user_id WHERE NOT u.disabled AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now())),
		'requests_24h',count(*),'denied_24h',count(*) FILTER(WHERE outcome='denied'),
		'errors_24h',count(*) FILTER(WHERE outcome='error'),
		'login_failures_24h',count(*) FILTER(WHERE action IN ('login','login_mfa') AND outcome='denied'),
		'average_duration_ms',coalesce(round(avg(duration_ms)),0)) FROM audit_events WHERE created_at>now()-interval '24 hours'`)
	})
}
func (s *Store) ChangePassword(ctx context.Context, actor Identity, current, next string) (any, error) {
	if err := s.RateLimit(ctx, "password-change:"+actor.User.ID, 10, 900); err != nil {
		return nil, err
	}
	var stored string
	if err := s.DB.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=$1", actor.User.ID).Scan(&stored); err != nil {
		return nil, err
	}
	if !VerifyPassword(ctx, stored, current) {
		return s.Perform(ctx, actor, "change_password", "", true, false, func(*sql.Tx) (any, error) { return nil, problem("UNAUTHORIZED", "Current password is incorrect.") })
	}
	hash, err := HashPassword(ctx, next)
	if err != nil {
		return nil, err
	}
	return s.Perform(ctx, actor, "change_password", "", true, false, func(tx *sql.Tx) (any, error) {
		res, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=$1 WHERE id=$2 AND password_hash=$3", hash, actor.User.ID, stored)
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil, problem("CONFLICT", "Credentials changed. Sign in again.")
		}
		if err := revokeCredentials(ctx, tx, actor.User.ID); err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE invitations SET used_at=now() WHERE user_id=$1 AND used_at IS NULL", actor.User.ID)
		return map[string]any{"sign_in_required": true}, err
	})
}
