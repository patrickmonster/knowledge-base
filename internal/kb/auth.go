package kb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

var passwordSlots = make(chan struct{}, 4)

func token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func emailAddress(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	m, err := mail.ParseAddress(email)
	if err != nil || m.Address != email || len(email) > 254 {
		return "", problem("INVALID_INPUT", "Enter a valid email address.")
	}
	return email, nil
}
func passwordWork(ctx context.Context, fn func() string) (string, error) {
	select {
	case passwordSlots <- struct{}{}:
		defer func() { <-passwordSlots }()
		return fn(), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func HashPassword(ctx context.Context, password string) (string, error) {
	if utf8.RuneCountInString(password) > 128 {
		return "", problem("INVALID_INPUT", "password must have 12–128 characters.")
	}
	if err := bounded("password", password, 12, 128); err != nil {
		return "", err
	}
	return passwordWork(ctx, func() string {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			panic(err)
		}
		hash := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
		return "$argon2id$v=19$m=19456,t=2,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash)
	})
}
func VerifyPassword(ctx context.Context, stored, password string) bool {
	parts := strings.Split(stored, "$")
	salt := make([]byte, 16)
	want := make([]byte, 32)
	valid := false
	if len(parts) == 6 && parts[1] == "argon2id" && parts[2] == "v=19" && parts[3] == "m=19456,t=2,p=1" {
		s, e1 := base64.RawStdEncoding.DecodeString(parts[4])
		w, e2 := base64.RawStdEncoding.DecodeString(parts[5])
		if e1 == nil && e2 == nil && len(s) == 16 && len(w) == 32 {
			salt = s
			want = w
			valid = true
		}
	}
	if len(password) > 1024 {
		return false
	}
	got, err := passwordWork(ctx, func() string { return string(argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)) })
	return err == nil && subtle.ConstantTimeCompare([]byte(got), want) == 1 && valid
}
func (s *Store) Bootstrap(ctx context.Context, email, name, password string) error {
	email, err := emailAddress(email)
	if err != nil {
		return err
	}
	if err = bounded("name", name, 1, 100); err != nil {
		return err
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return err
	}
	return transaction(ctx, s.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(51283168)"); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE global_role='admin')").Scan(&exists); err != nil {
			return err
		}
		if exists {
			return problem("CONFLICT", "An administrator already exists; bootstrap is only allowed once.")
		}
		id := uuid.NewString()
		_, err := tx.ExecContext(ctx, `INSERT INTO users(id,name,email,password_hash,global_role) VALUES($1,$2,$3,$4,'admin')`, id, name, email, hash)
		if err != nil {
			return err
		}
		return audit(ctx, tx, Event{ActorID: id, Action: "bootstrap", TargetID: id, Outcome: "success", RequestID: uuid.NewString()})
	})
}

// NeedsSetup reports whether no administrator exists yet, as on a fresh
// install. Only then may the first administrator be created.
func (s *Store) NeedsSetup(ctx context.Context) (bool, error) {
	var exists bool
	err := s.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE global_role='admin')").Scan(&exists)
	return !exists, err
}

// Setup creates the first administrator from the web console. It works only
// while no administrator exists.
func (s *Store) Setup(ctx context.Context, email, name, password string) error {
	needed, err := s.NeedsSetup(ctx)
	if err != nil {
		return err
	}
	if !needed {
		return problem("CONFLICT", "Setup is already complete. Sign in instead.")
	}
	return s.Bootstrap(ctx, email, name, password)
}
func (s *Store) RateLimit(ctx context.Context, bucket string, limit int, windowSeconds int) error {
	var attempts int
	err := s.DB.QueryRowContext(ctx, `INSERT INTO rate_limits(bucket) VALUES($1) ON CONFLICT(bucket) DO UPDATE SET
		attempts=CASE WHEN rate_limits.window_start<now()-make_interval(secs=>$2) THEN 1 ELSE rate_limits.attempts+1 END,
		window_start=CASE WHEN rate_limits.window_start<now()-make_interval(secs=>$2) THEN now() ELSE rate_limits.window_start END
		RETURNING attempts`, digest(bucket), windowSeconds).Scan(&attempts)
	if err != nil {
		return err
	}
	if attempts > limit {
		return problem("RATE_LIMITED", "Too many attempts. Please try again later.")
	}
	return nil
}

// LoginResult holds either a session token, or, when the user enabled MFA, a
// challenge token for CompleteMFALogin.
type LoginResult struct {
	Session  string
	MFAToken string
}

// Login signs in a user without MFA. Users with MFA must use BeginLogin.
func (s *Store) Login(ctx context.Context, email, password string) (string, error) {
	r, err := s.BeginLogin(ctx, email, password)
	if err != nil {
		return "", err
	}
	if r.MFAToken != "" {
		return "", problem("MFA_REQUIRED", "Enter the code from your authenticator app.")
	}
	return r.Session, nil
}

func (s *Store) BeginLogin(ctx context.Context, email, password string) (LoginResult, error) {
	email, err := emailAddress(email)
	if err != nil {
		return LoginResult{}, problem("UNAUTHORIZED", "Email or password is incorrect.")
	}
	if err = s.RateLimit(ctx, "login:"+email, 10, 900); err != nil {
		return LoginResult{}, err
	}
	type credential struct {
		User
		PasswordHash string `json:"password_hash"`
	}
	u, err := one[credential](ctx, s.DB, "SELECT to_jsonb(u) FROM users u WHERE email=$1", email)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LoginResult{}, err
	}
	matched := VerifyPassword(ctx, u.PasswordHash, password)
	if !matched || u.Disabled || u.ID == "" {
		if err := audit(ctx, s.DB, Event{TargetID: u.ID, Action: "login", Outcome: "denied", ErrorCode: "UNAUTHORIZED", RequestID: uuid.NewString()}); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{}, problem("UNAUTHORIZED", "Email or password is incorrect.")
	}
	raw := token()
	var out LoginResult
	err = transaction(ctx, s.DB, func(tx *sql.Tx) error {
		// Re-check under a lock: password reset or suspension must invalidate this login.
		var current string
		var disabled bool
		if err := tx.QueryRowContext(ctx, "SELECT password_hash,disabled FROM users WHERE id=$1 FOR SHARE", u.ID).Scan(&current, &disabled); err != nil {
			return err
		}
		if disabled || current != u.PasswordHash {
			return problem("UNAUTHORIZED", "Please sign in again.")
		}
		mfa, err := s.mfaEnabled(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if mfa {
			// Password verified; the session waits for the second factor.
			if len(s.MFAKey) != 32 {
				return problem("MFA_UNAVAILABLE", "Two-step verification is not configured on this server. Ask an administrator.")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO mfa_challenges(token_hash,user_id,credential_digest,expires_at)
				VALUES($1,$2,$3,now()+make_interval(secs=>$4))`, digest(raw), u.ID, digest(current), mfaChallengeTTL.Seconds()); err != nil {
				return err
			}
			out.MFAToken = raw
			return audit(ctx, tx, Event{ActorID: u.ID, Action: "login_password", Outcome: "success", RequestID: uuid.NewString(), Details: map[string]any{"mfa": "required"}})
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,csrf_token,expires_at) VALUES($1,$2,$3,now()+interval '12 hours')`, digest(raw), u.ID, token()); err != nil {
			return err
		}
		out.Session = raw
		return audit(ctx, tx, Event{ActorID: u.ID, Action: "login", Outcome: "success", RequestID: uuid.NewString()})
	})
	if err != nil {
		return LoginResult{}, err
	}
	return out, nil
}
func (s *Store) Session(ctx context.Context, raw string) (Identity, error) {
	if len(raw) != 43 {
		return Identity{}, problem("UNAUTHORIZED", "Please sign in.")
	}
	var userJSON []byte
	var csrf string
	err := s.DB.QueryRowContext(ctx, `SELECT to_jsonb(u)-'password_hash',s.csrf_token FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.expires_at>now() AND NOT u.disabled`, digest(raw)).Scan(&userJSON, &csrf)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, problem("UNAUTHORIZED", "Please sign in.")
	}
	if err != nil {
		return Identity{}, err
	}
	u, err := decodeUser(userJSON)
	return Identity{User: u, SessionHash: digest(raw), CSRF: csrf}, err
}
func (s *Store) APIKey(ctx context.Context, raw string) (Identity, error) {
	if !strings.HasPrefix(raw, "kb_") || len(raw) != 46 {
		return Identity{}, problem("UNAUTHORIZED", "Invalid API key.")
	}
	var userJSON []byte
	var id, access string
	var project sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT to_jsonb(u)-'password_hash',k.id::text,k.access,k.project_id::text
		FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.token_hash=$1 AND k.revoked_at IS NULL
		AND (k.expires_at IS NULL OR k.expires_at>now()) AND NOT u.disabled`, digest(raw)).Scan(&userJSON, &id, &access, &project)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, problem("UNAUTHORIZED", "Invalid, expired, or revoked API key.")
	}
	if err != nil {
		return Identity{}, err
	}
	u, err := decodeUser(userJSON)
	return Identity{User: u, KeyID: id, Access: access, ProjectID: project.String}, err
}

func (s *Store) AcceptInvitation(ctx context.Context, raw, password string) error {
	if len(raw) != 43 {
		return problem("INVALID_INPUT", "Invalid or expired invitation.")
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return err
	}
	return transaction(ctx, s.DB, func(tx *sql.Tx) error {
		var id, purpose string
		if err := tx.QueryRowContext(ctx, `SELECT user_id::text,purpose FROM invitations WHERE token_hash=$1 AND used_at IS NULL
			AND expires_at>now() FOR UPDATE`, digest(raw)).Scan(&id, &purpose); err != nil {
			return problem("INVALID_INPUT", "Invalid or expired invitation.")
		}
		var disabled bool
		if err := tx.QueryRowContext(ctx, "SELECT disabled FROM users WHERE id=$1 FOR UPDATE", id).Scan(&disabled); err != nil {
			return err
		}
		if disabled {
			return problem("FORBIDDEN", "This account is disabled.")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=$1 WHERE id=$2", hash, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE invitations SET used_at=now() WHERE user_id=$1 AND used_at IS NULL", id); err != nil {
			return err
		}
		if err := revokeCredentials(ctx, tx, id); err != nil {
			return err
		}
		return audit(ctx, tx, Event{ActorID: id, Action: purpose + "_accepted", TargetID: id, Outcome: "success", RequestID: uuid.NewString()})
	})
}
func revokeCredentials(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=$1", userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE api_keys SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL", userID)
	return err
}

func (s *Store) Cleanup(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<now();
		DELETE FROM rate_limits WHERE window_start<now()-interval '1 day';
		DELETE FROM invitations WHERE expires_at<now()-interval '7 days';
		DELETE FROM mfa_challenges WHERE expires_at<now();
		DELETE FROM user_mfa WHERE enabled_at IS NULL AND created_at<now()-interval '1 day';
		DELETE FROM github_webhook_deliveries WHERE received_at<now()-interval '30 days'`)
	return err
}
