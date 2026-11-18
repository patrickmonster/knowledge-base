package kb

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Two-step sign-in (TOTP, RFC 6238) for web sessions.
//
// A password-verified login of a user with MFA enabled gets a short-lived
// challenge instead of a session; the session is issued only after a valid
// authenticator code or a one-time recovery code. MCP keys are not affected:
// they are issued from a web session, so MFA guards issuance, and existing
// keys keep working.

const (
	totpDigits           = 6
	totpPeriod           = 30
	totpSkew             = 1 // accept one step before and after (clock drift)
	totpSecretBytes      = 20
	mfaSetupTTL          = 15 * time.Minute
	mfaChallengeTTL      = 5 * time.Minute
	sessionTTL           = 7 * 24 * time.Hour // web login session lifetime
	mfaChallengeAttempts = 5
	recoveryCodeCount    = 10
	mfaIssuer            = "Knowledge Base"
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ParseMFAKey derives the sealing key from MFA_ENCRYPTION_KEY. Empty disables MFA.
func ParseMFAKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) < 32 {
		return nil, errors.New("MFA_ENCRYPTION_KEY must have at least 32 characters")
	}
	sum := sha256.Sum256([]byte("kb-mfa-key-v1\x00" + raw))
	return sum[:], nil
}

func hotp(secret []byte, counter int64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, v%mod)
}

func cleanCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(code))
}

func isTOTPCode(code string) bool {
	if len(code) != totpDigits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// totpStep returns the matched time step after lastStep, or 0 when the code
// is wrong or already used.
func totpStep(secret []byte, code string, now time.Time, lastStep int64) int64 {
	if !isTOTPCode(code) {
		return 0
	}
	current := now.Unix() / totpPeriod
	var found int64
	for d := -totpSkew; d <= totpSkew; d++ {
		step := current + int64(d)
		if subtle.ConstantTimeCompare([]byte(hotp(secret, step, totpDigits)), []byte(code)) == 1 && step > lastStep && found == 0 {
			found = step
		}
	}
	return found
}

func (s *Store) mfaCipher() (cipher.AEAD, error) {
	if len(s.MFAKey) != 32 {
		return nil, problem("MFA_UNAVAILABLE", "Two-step verification is not configured on this server (MFA_ENCRYPTION_KEY).")
	}
	block, err := aes.NewCipher(s.MFAKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Store) sealMFASecret(userID string, secret []byte) (string, error) {
	aead, err := s.mfaCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, secret, []byte("kb-mfa-v1:"+userID))
	return "v1:" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *Store) openMFASecret(userID, sealed string) ([]byte, error) {
	aead, err := s.mfaCipher()
	if err != nil {
		return nil, err
	}
	raw, ok := strings.CutPrefix(sealed, "v1:")
	data, derr := base64.RawStdEncoding.DecodeString(raw)
	if !ok || derr != nil || len(data) < aead.NonceSize() {
		return nil, problem("MFA_UNAVAILABLE", "Stored two-step verification data cannot be read. Ask an administrator to reset it.")
	}
	secret, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("kb-mfa-v1:"+userID))
	if err != nil {
		// Usually a changed MFA_ENCRYPTION_KEY.
		return nil, problem("MFA_UNAVAILABLE", "Stored two-step verification data cannot be read. Ask an administrator to reset it.")
	}
	return secret, nil
}

func newRecoveryCodes() []string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		for j := range b {
			b[j] = alphabet[int(b[j])%len(alphabet)]
		}
		codes[i] = string(b[:5]) + "-" + string(b[5:])
	}
	return codes
}

func recoveryDigest(code string) string {
	return digest("kb-recovery-v1:" + strings.ToLower(cleanCode(code)))
}

func replaceRecoveryCodes(ctx context.Context, tx *sql.Tx, userID string) ([]string, error) {
	if _, err := tx.ExecContext(ctx, "DELETE FROM mfa_recovery_codes WHERE user_id=$1", userID); err != nil {
		return nil, err
	}
	codes := newRecoveryCodes()
	for _, c := range codes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO mfa_recovery_codes(user_id,code_hash) VALUES($1,$2) ON CONFLICT DO NOTHING", userID, recoveryDigest(c)); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

var errMFACode = problem("INVALID_INPUT", "The verification code is incorrect or was already used.")

// verifySecondFactor checks an authenticator code (or, when allowed, an unused
// recovery code) for an enabled MFA and consumes it in tx. It returns the
// method used ("totp" or "recovery_code") or errMFACode.
func (s *Store) verifySecondFactor(ctx context.Context, tx *sql.Tx, userID, code string, allowRecovery bool) (string, error) {
	var sealed string
	var lastStep int64
	err := tx.QueryRowContext(ctx, "SELECT secret_ciphertext,last_step FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL FOR UPDATE", userID).Scan(&sealed, &lastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return "", problem("CONFLICT", "Two-step verification is not enabled.")
	}
	if err != nil {
		return "", err
	}
	code = cleanCode(code)
	if isTOTPCode(code) {
		secret, err := s.openMFASecret(userID, sealed)
		if err != nil {
			return "", err
		}
		step := totpStep(secret, code, time.Now(), lastStep)
		if step == 0 {
			return "", errMFACode
		}
		if _, err := tx.ExecContext(ctx, "UPDATE user_mfa SET last_step=$1 WHERE user_id=$2", step, userID); err != nil {
			return "", err
		}
		return "totp", nil
	}
	if !allowRecovery || len(code) != 10 {
		return "", errMFACode
	}
	res, err := tx.ExecContext(ctx, "UPDATE mfa_recovery_codes SET used_at=now() WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL", userID, recoveryDigest(code))
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", errMFACode
	}
	return "recovery_code", nil
}

type MFAStatus struct {
	Enabled           bool       `json:"enabled"`
	Available         bool       `json:"available"`
	EnabledAt         *time.Time `json:"enabled_at"`
	RecoveryCodesLeft int        `json:"recovery_codes_left"`
}

func (s *Store) mfaEnabled(ctx context.Context, q Querier, userID string) (bool, error) {
	var on bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL)", userID).Scan(&on)
	return on, err
}

func (s *Store) GetMFAStatus(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "view_mfa", "", false, false, func(tx *sql.Tx) (any, error) {
		if actor.KeyID != "" {
			return nil, problem("FORBIDDEN", "Sign in to manage two-step verification.")
		}
		out := MFAStatus{Available: len(s.MFAKey) == 32}
		var enabledAt sql.NullTime
		err := tx.QueryRowContext(ctx, "SELECT enabled_at FROM user_mfa WHERE user_id=$1", actor.User.ID).Scan(&enabledAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if enabledAt.Valid {
			out.Enabled, out.EnabledAt = true, &enabledAt.Time
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM mfa_recovery_codes WHERE user_id=$1 AND used_at IS NULL", actor.User.ID).Scan(&out.RecoveryCodesLeft); err != nil {
				return nil, err
			}
		}
		return out, nil
	})
}

// SetupMFA starts (or restarts) enrollment with a new secret. It is not active
// until EnableMFA confirms a code from the authenticator app.
func (s *Store) SetupMFA(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "mfa_setup", "", true, false, func(tx *sql.Tx) (any, error) {
		if actor.KeyID != "" {
			return nil, problem("FORBIDDEN", "Sign in to manage two-step verification.")
		}
		secret := make([]byte, totpSecretBytes)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		sealed, err := s.sealMFASecret(actor.User.ID, secret)
		if err != nil {
			return nil, err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO user_mfa(user_id,secret_ciphertext) VALUES($1,$2)
			ON CONFLICT(user_id) DO UPDATE SET secret_ciphertext=EXCLUDED.secret_ciphertext,last_step=0,created_at=now()
			WHERE user_mfa.enabled_at IS NULL`, actor.User.ID, sealed)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, problem("CONFLICT", "Two-step verification is already enabled. Turn it off first to register a new device.")
		}
		encoded := totpEncoding.EncodeToString(secret)
		q := url.Values{"secret": {encoded}, "issuer": {mfaIssuer}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(totpDigits)}, "period": {fmt.Sprint(totpPeriod)}}
		uri := "otpauth://totp/" + url.PathEscape(mfaIssuer+":"+actor.User.Email) + "?" + q.Encode()
		return map[string]any{"secret": encoded, "otpauth_uri": uri, "expires_in_minutes": int(mfaSetupTTL.Minutes())}, nil
	})
}

// checkPassword verifies the current password for sensitive MFA changes.
func (s *Store) checkPassword(ctx context.Context, actor Identity, action, password string) (string, error) {
	if err := s.RateLimit(ctx, "mfa-manage:"+actor.User.ID, 10, 900); err != nil {
		return "", err
	}
	var stored string
	if err := s.DB.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=$1", actor.User.ID).Scan(&stored); err != nil {
		return "", err
	}
	if !VerifyPassword(ctx, stored, password) {
		_, err := s.Perform(ctx, actor, action, "", true, false, func(*sql.Tx) (any, error) {
			return nil, problem("INVALID_INPUT", "Current password is incorrect.")
		})
		return "", err
	}
	return stored, nil
}

func samePassword(ctx context.Context, tx *sql.Tx, userID, stored string) error {
	var current string
	if err := tx.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=$1 FOR SHARE", userID).Scan(&current); err != nil {
		return err
	}
	if current != stored {
		return problem("CONFLICT", "Credentials changed. Sign in again.")
	}
	return nil
}

// EnableMFA confirms setup with the current password and an authenticator
// code, issues recovery codes, and signs out the user's other sessions.
func (s *Store) EnableMFA(ctx context.Context, actor Identity, password, code string) (any, error) {
	if actor.KeyID != "" {
		return nil, problem("FORBIDDEN", "Sign in to manage two-step verification.")
	}
	stored, err := s.checkPassword(ctx, actor, "mfa_enable", password)
	if err != nil {
		return nil, err
	}
	return s.Perform(ctx, actor, "mfa_enable", "", true, false, func(tx *sql.Tx) (any, error) {
		if err := samePassword(ctx, tx, actor.User.ID, stored); err != nil {
			return nil, err
		}
		var sealed string
		var enabled, fresh bool
		err := tx.QueryRowContext(ctx, `SELECT secret_ciphertext,enabled_at IS NOT NULL,created_at>now()-make_interval(secs=>$2)
			FROM user_mfa WHERE user_id=$1 FOR UPDATE`, actor.User.ID, mfaSetupTTL.Seconds()).Scan(&sealed, &enabled, &fresh)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, problem("INVALID_INPUT", "Start two-step verification setup first.")
		}
		if err != nil {
			return nil, err
		}
		if enabled {
			return nil, problem("CONFLICT", "Two-step verification is already enabled.")
		}
		if !fresh {
			return nil, problem("INVALID_INPUT", "Setup expired. Start again.")
		}
		secret, err := s.openMFASecret(actor.User.ID, sealed)
		if err != nil {
			return nil, err
		}
		step := totpStep(secret, cleanCode(code), time.Now(), 0)
		if step == 0 {
			return nil, errMFACode
		}
		if _, err := tx.ExecContext(ctx, "UPDATE user_mfa SET enabled_at=now(),last_step=$1 WHERE user_id=$2", step, actor.User.ID); err != nil {
			return nil, err
		}
		codes, err := replaceRecoveryCodes(ctx, tx, actor.User.ID)
		if err != nil {
			return nil, err
		}
		// Sessions that signed in without the second factor end now.
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2", actor.User.ID, actor.SessionHash); err != nil {
			return nil, err
		}
		return map[string]any{"enabled": true, "recovery_codes": codes}, nil
	})
}

// DisableMFA turns MFA off with the current password and an authenticator or recovery code.
func (s *Store) DisableMFA(ctx context.Context, actor Identity, password, code string) (any, error) {
	if actor.KeyID != "" {
		return nil, problem("FORBIDDEN", "Sign in to manage two-step verification.")
	}
	stored, err := s.checkPassword(ctx, actor, "mfa_disable", password)
	if err != nil {
		return nil, err
	}
	return s.Perform(ctx, actor, "mfa_disable", "", true, false, func(tx *sql.Tx) (any, error) {
		if err := samePassword(ctx, tx, actor.User.ID, stored); err != nil {
			return nil, err
		}
		if _, err := s.verifySecondFactor(ctx, tx, actor.User.ID, code, true); err != nil {
			return nil, err
		}
		if err := clearMFA(ctx, tx, actor.User.ID); err != nil {
			return nil, err
		}
		return map[string]any{"enabled": false}, nil
	})
}

// RegenerateRecoveryCodes replaces all recovery codes after an authenticator code.
func (s *Store) RegenerateRecoveryCodes(ctx context.Context, actor Identity, code string) (any, error) {
	if actor.KeyID != "" {
		return nil, problem("FORBIDDEN", "Sign in to manage two-step verification.")
	}
	if err := s.RateLimit(ctx, "mfa-manage:"+actor.User.ID, 10, 900); err != nil {
		return nil, err
	}
	return s.Perform(ctx, actor, "mfa_recovery_codes", "", true, false, func(tx *sql.Tx) (any, error) {
		if _, err := s.verifySecondFactor(ctx, tx, actor.User.ID, code, false); err != nil {
			return nil, err
		}
		codes, err := replaceRecoveryCodes(ctx, tx, actor.User.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"recovery_codes": codes}, nil
	})
}

func clearMFA(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM mfa_challenges WHERE user_id=$1", userID)
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM mfa_recovery_codes WHERE user_id=$1", userID)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM user_mfa WHERE user_id=$1", userID)
	}
	return err
}

type MFAResetInput struct {
	UserID string `json:"user_id"`
}

// ResetMFA lets a global administrator remove another user's MFA (lost
// device). The user's sessions end; MCP keys are kept.
func (s *Store) ResetMFA(ctx context.Context, actor Identity, in MFAResetInput) (any, error) {
	ctx = auditInput(ctx, in)
	return s.Perform(ctx, actor, "mfa_reset", "", true, true, func(tx *sql.Tx) (any, error) {
		if err := validID(in.UserID); err != nil {
			return nil, err
		}
		if in.UserID == actor.User.ID {
			return nil, problem("FORBIDDEN", "Turn off your own two-step verification on the account page.")
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL)", in.UserID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, problem("NOT_FOUND", "Two-step verification is not enabled for this user.")
		}
		if err := clearMFA(ctx, tx, in.UserID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=$1", in.UserID); err != nil {
			return nil, err
		}
		return map[string]any{"user_id": in.UserID, "mfa_enabled": false}, nil
	})
}

// CompleteMFALogin finishes a password-verified sign-in with a second factor
// and returns the new session token.
func (s *Store) CompleteMFALogin(ctx context.Context, raw, code string) (string, error) {
	expired := problem("UNAUTHORIZED", "Sign-in expired. Enter your email and password again.")
	if len(raw) != 43 {
		return "", expired
	}
	var userID, credential string
	var attempts int
	err := s.DB.QueryRowContext(ctx, `UPDATE mfa_challenges SET attempts=attempts+1 WHERE token_hash=$1 AND expires_at>now()
		RETURNING user_id::text,credential_digest,attempts`, digest(raw)).Scan(&userID, &credential, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		if err := audit(ctx, s.DB, Event{Action: "login_mfa", Outcome: "denied", ErrorCode: "UNAUTHORIZED", RequestID: uuid.NewString()}); err != nil {
			return "", err
		}
		return "", expired
	}
	if err != nil {
		return "", err
	}
	if attempts > mfaChallengeAttempts {
		if _, err := s.DB.ExecContext(ctx, "DELETE FROM mfa_challenges WHERE token_hash=$1", digest(raw)); err != nil {
			return "", err
		}
		if err := audit(ctx, s.DB, Event{TargetID: userID, Action: "login_mfa", Outcome: "denied", ErrorCode: "RATE_LIMITED", RequestID: uuid.NewString()}); err != nil {
			return "", err
		}
		return "", expired
	}
	if err := s.RateLimit(ctx, "mfa:"+userID, 10, 900); err != nil {
		return "", err
	}
	session := token()
	var method string
	err = transaction(ctx, s.DB, func(tx *sql.Tx) error {
		var current string
		var disabled bool
		if err := tx.QueryRowContext(ctx, "SELECT password_hash,disabled FROM users WHERE id=$1 FOR SHARE", userID).Scan(&current, &disabled); err != nil {
			return err
		}
		if disabled || digest(current) != credential {
			return expired
		}
		m, err := s.verifySecondFactor(ctx, tx, userID, code, true)
		if err != nil {
			return err
		}
		method = m
		if _, err := tx.ExecContext(ctx, "DELETE FROM mfa_challenges WHERE token_hash=$1", digest(raw)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,csrf_token,expires_at) VALUES($1,$2,$3,now()+make_interval(secs=>$4))`, digest(session), userID, token(), sessionTTL.Seconds()); err != nil {
			return err
		}
		return audit(ctx, tx, Event{ActorID: userID, Action: "login", Outcome: "success", RequestID: uuid.NewString(), Details: map[string]any{"mfa": method}})
	})
	if err != nil {
		if errorCode(err) == "INTERNAL_ERROR" {
			return "", err
		}
		if errors.Is(err, errMFACode) {
			err = problem("MFA_INVALID_CODE", "The verification code is incorrect or was already used.")
		}
		if logerr := audit(ctx, s.DB, Event{TargetID: userID, Action: "login_mfa", Outcome: "denied", ErrorCode: errorCode(err), RequestID: uuid.NewString()}); logerr != nil {
			return "", logerr
		}
		return "", err
	}
	return session, nil
}
