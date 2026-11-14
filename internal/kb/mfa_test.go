package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTOTPReferenceVectors(t *testing.T) {
	// RFC 6238 appendix B, SHA-1, 8 digits.
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		want string
	}{{59, "94287082"}, {1111111109, "07081804"}, {1111111111, "14050471"}, {1234567890, "89005924"}, {2000000000, "69279037"}} {
		if got := hotp(secret, v.unix/totpPeriod, 8); got != v.want {
			t.Fatalf("T=%d: got %s want %s", v.unix, got, v.want)
		}
	}
	now := time.Unix(1234567890, 0)
	step := now.Unix() / totpPeriod
	code := hotp(secret, step, totpDigits)
	if totpStep(secret, code, now, 0) != step {
		t.Fatal("current code rejected")
	}
	if totpStep(secret, code[:3]+" "+code[3:], now, 0) != 0 {
		t.Fatal("totpStep must receive cleaned codes")
	}
	if totpStep(secret, cleanCode(code[:3]+" "+code[3:]), now, 0) != step {
		t.Fatal("spaced code rejected after cleaning")
	}
	if totpStep(secret, hotp(secret, step-1, totpDigits), now, 0) != step-1 || totpStep(secret, hotp(secret, step+1, totpDigits), now, 0) != step+1 {
		t.Fatal("one step of clock drift must be accepted")
	}
	if totpStep(secret, hotp(secret, step-2, totpDigits), now, 0) != 0 {
		t.Fatal("old code accepted")
	}
	if totpStep(secret, code, now, step) != 0 {
		t.Fatal("replayed code accepted")
	}
	if totpStep(secret, "12a456", now, 0) != 0 || totpStep(secret, "", now, 0) != 0 {
		t.Fatal("malformed code accepted")
	}
}

func TestMFASecretSealing(t *testing.T) {
	if k, err := ParseMFAKey(""); err != nil || k != nil {
		t.Fatal("empty key must disable MFA")
	}
	if _, err := ParseMFAKey("short"); err == nil {
		t.Fatal("short key accepted")
	}
	key, err := ParseMFAKey(strings.Repeat("k", 40))
	if err != nil || len(key) != 32 {
		t.Fatal(err)
	}
	s := &Store{MFAKey: key}
	sealed, err := s.sealMFASecret("user-a", []byte("secret-bytes"))
	if err != nil || strings.Contains(sealed, "secret-bytes") {
		t.Fatal("secret not sealed", err)
	}
	if got, err := s.openMFASecret("user-a", sealed); err != nil || string(got) != "secret-bytes" {
		t.Fatal("round trip failed", err)
	}
	if _, err := s.openMFASecret("user-b", sealed); errorCode(err) != "MFA_UNAVAILABLE" {
		t.Fatal("sealed secret is not bound to its user", err)
	}
	other, _ := ParseMFAKey(strings.Repeat("x", 40))
	if _, err := (&Store{MFAKey: other}).openMFASecret("user-a", sealed); errorCode(err) != "MFA_UNAVAILABLE" {
		t.Fatal("wrong key opened the secret", err)
	}
	if _, err := (&Store{}).sealMFASecret("user-a", []byte("x")); errorCode(err) != "MFA_UNAVAILABLE" {
		t.Fatal("sealing without a key must fail")
	}
	codes := newRecoveryCodes()
	if len(codes) != recoveryCodeCount || len(codes[0]) != 11 || codes[0][5] != '-' {
		t.Fatal("unexpected recovery code format", codes)
	}
	if recoveryDigest(codes[0]) != recoveryDigest(strings.ToUpper(strings.ReplaceAll(codes[0], "-", " "))) {
		t.Fatal("recovery codes must ignore case and separators")
	}
}

func mfaTestKey(t *testing.T) []byte {
	t.Helper()
	key, err := ParseMFAKey("integration-test-only-mfa-encryption-key")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// enableMFAFor runs setup and confirmation, returning the decoded secret and recovery codes.
func enableMFAFor(t *testing.T, s *Store, id Identity) ([]byte, []string) {
	t.Helper()
	ctx := context.Background()
	out, err := s.SetupMFA(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := totpEncoding.DecodeString(out.(map[string]any)["secret"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.(map[string]any)["otpauth_uri"].(string), "otpauth://totp/") {
		t.Fatal("missing otpauth URI")
	}
	code := hotp(secret, time.Now().Unix()/totpPeriod, totpDigits)
	enabled, err := s.EnableMFA(ctx, id, testPassword, code)
	if err != nil {
		t.Fatal(err)
	}
	return secret, enabled.(map[string]any)["recovery_codes"].([]string)
}

func TestMFAEnrollmentAndLogin(t *testing.T) {
	s := testStore(t)
	s.MFAKey = mfaTestKey(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	other := loginAs(t, s, "admin@example.com") // a second browser

	// Setup alone does not enable MFA; a wrong password or code does not either.
	out, err := s.SetupMFA(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := totpEncoding.DecodeString(out.(map[string]any)["secret"].(string))
	now := time.Now().Unix() / totpPeriod
	_, err = s.EnableMFA(ctx, admin, "wrong-password-123456", hotp(secret, now, totpDigits))
	denied(t, err, "INVALID_INPUT")
	_, err = s.EnableMFA(ctx, admin, testPassword, "000000")
	if hotp(secret, now, totpDigits) != "000000" {
		denied(t, err, "INVALID_INPUT")
	}
	if _, err := s.Login(ctx, "admin@example.com", testPassword); err != nil {
		t.Fatal("pending setup must not require MFA:", err)
	}
	var stored string
	if err := s.DB.QueryRow("SELECT secret_ciphertext FROM user_mfa WHERE user_id=$1", admin.User.ID).Scan(&stored); err != nil || strings.Contains(stored, totpEncoding.EncodeToString(secret)) {
		t.Fatal("secret stored in plain text", err)
	}

	enabled, err := s.EnableMFA(ctx, admin, testPassword, hotp(secret, now, totpDigits))
	if err != nil {
		t.Fatal(err)
	}
	codes := enabled.(map[string]any)["recovery_codes"].([]string)
	if len(codes) != recoveryCodeCount {
		t.Fatal("recovery codes missing")
	}
	if _, err := s.SetupMFA(ctx, admin); errorCode(err) != "CONFLICT" {
		t.Fatal("setup over an enabled MFA must be refused", err)
	}
	// Other sessions end; the confirming session stays.
	if _, err := s.GetMFAStatus(ctx, other); errorCode(err) != "UNAUTHORIZED" {
		t.Fatal("other session survived enabling MFA", err)
	}
	st, err := s.GetMFAStatus(ctx, admin)
	if err != nil || !st.(MFAStatus).Enabled || st.(MFAStatus).RecoveryCodesLeft != recoveryCodeCount {
		t.Fatal("status wrong", st, err)
	}

	// Password alone no longer signs in.
	_, err = s.Login(ctx, "admin@example.com", testPassword)
	denied(t, err, "MFA_REQUIRED")
	r, err := s.BeginLogin(ctx, "admin@example.com", testPassword)
	if err != nil || r.Session != "" || r.MFAToken == "" {
		t.Fatal("expected an MFA challenge", r, err)
	}
	// The code used to enable is a replay.
	_, err = s.CompleteMFALogin(ctx, r.MFAToken, hotp(secret, now, totpDigits))
	denied(t, err, "MFA_INVALID_CODE")
	session, err := s.CompleteMFALogin(ctx, r.MFAToken, hotp(secret, now+1, totpDigits))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, session); err != nil {
		t.Fatal("MFA session invalid", err)
	}
	if _, err := s.CompleteMFALogin(ctx, r.MFAToken, codes[0]); errorCode(err) != "UNAUTHORIZED" {
		t.Fatal("challenge reusable after success", err)
	}

	// Recovery codes work once.
	r, _ = s.BeginLogin(ctx, "admin@example.com", testPassword)
	if _, err := s.CompleteMFALogin(ctx, r.MFAToken, strings.ToUpper(codes[0])); err != nil {
		t.Fatal("recovery code rejected", err)
	}
	r, _ = s.BeginLogin(ctx, "admin@example.com", testPassword)
	_, err = s.CompleteMFALogin(ctx, r.MFAToken, codes[0])
	denied(t, err, "MFA_INVALID_CODE")

	// A challenge allows a limited number of attempts.
	for range mfaChallengeAttempts - 1 {
		if _, err := s.CompleteMFALogin(ctx, r.MFAToken, "badcode-xx"); errorCode(err) != "MFA_INVALID_CODE" {
			t.Fatal(err)
		}
	}
	_, err = s.CompleteMFALogin(ctx, r.MFAToken, codes[1])
	denied(t, err, "UNAUTHORIZED")

	// A password change invalidates a pending challenge.
	r, _ = s.BeginLogin(ctx, "admin@example.com", testPassword)
	if _, err := s.DB.Exec("UPDATE users SET password_hash='$argon2id$changed' WHERE id=$1", admin.User.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.CompleteMFALogin(ctx, r.MFAToken, codes[2])
	denied(t, err, "UNAUTHORIZED")
}

func TestMFAManagementAndAdminReset(t *testing.T) {
	s := testStore(t)
	s.MFAKey = mfaTestKey(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "member@example.com")
	secret, codes := enableMFAFor(t, s, member)

	// Regenerating needs an authenticator code, not a recovery code.
	_, err := s.RegenerateRecoveryCodes(ctx, member, codes[0])
	denied(t, err, "INVALID_INPUT")
	if _, err := s.DB.Exec("UPDATE user_mfa SET last_step=0 WHERE user_id=$1", member.User.ID); err != nil {
		t.Fatal(err)
	}
	out, err := s.RegenerateRecoveryCodes(ctx, member, hotp(secret, time.Now().Unix()/totpPeriod, totpDigits))
	if err != nil {
		t.Fatal(err)
	}
	fresh := out.(map[string]any)["recovery_codes"].([]string)
	r, _ := s.BeginLogin(ctx, "member@example.com", testPassword)
	_, err = s.CompleteMFALogin(ctx, r.MFAToken, codes[0])
	denied(t, err, "MFA_INVALID_CODE")

	// Admin reset: only global administrators, never for themselves.
	_, err = s.ResetMFA(ctx, member, MFAResetInput{UserID: admin.User.ID})
	denied(t, err, "FORBIDDEN")
	_, err = s.ResetMFA(ctx, admin, MFAResetInput{UserID: admin.User.ID})
	denied(t, err, "FORBIDDEN")
	_, err = s.ResetMFA(ctx, admin, MFAResetInput{UserID: admin.User.ID[:8]})
	denied(t, err, "INVALID_INPUT")
	key := mustCreateKey(t, s, member)
	if _, err := s.ResetMFA(ctx, admin, MFAResetInput{UserID: member.User.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMFAStatus(ctx, member); errorCode(err) != "UNAUTHORIZED" {
		t.Fatal("reset must end the user's sessions", err)
	}
	if _, err := s.APIKey(ctx, key); err != nil {
		t.Fatal("reset must keep MCP keys", err)
	}
	_, err = s.ResetMFA(ctx, admin, MFAResetInput{UserID: member.User.ID})
	denied(t, err, "NOT_FOUND")
	member = loginAs(t, s, "member@example.com")

	// Disable with password and a recovery code.
	_, codes = enableMFAFor(t, s, member)
	_, err = s.DisableMFA(ctx, member, testPassword, "bad-code00")
	denied(t, err, "INVALID_INPUT")
	_, err = s.DisableMFA(ctx, member, "wrong-password-123456", codes[0])
	denied(t, err, "INVALID_INPUT")
	if _, err := s.DisableMFA(ctx, member, testPassword, codes[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "member@example.com", testPassword); err != nil {
		t.Fatal("password login after disabling MFA", err)
	}
	_, err = s.DisableMFA(ctx, member, testPassword, fresh[0])
	denied(t, err, "CONFLICT")

	// Without the server key, setup is unavailable and enrolled users fail closed.
	enableMFAFor(t, s, member)
	s.MFAKey = nil
	_, err = s.SetupMFA(ctx, admin)
	denied(t, err, "MFA_UNAVAILABLE")
	_, err = s.BeginLogin(ctx, "member@example.com", testPassword)
	denied(t, err, "MFA_UNAVAILABLE")

	// Audit records never carry secrets or codes.
	var leaked bool
	if err := s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM audit_events WHERE details::text LIKE '%'||$1||'%' OR details::text LIKE '%'||$2||'%')`,
		totpEncoding.EncodeToString(secret), fresh[1]).Scan(&leaked); err != nil || leaked {
		t.Fatal("MFA material leaked into audit details", err)
	}
	var mfaEvents int
	if err := s.DB.QueryRow(`SELECT count(*) FROM audit_events WHERE action IN ('mfa_setup','mfa_enable','mfa_disable','mfa_reset','mfa_recovery_codes','login_mfa')`).Scan(&mfaEvents); err != nil || mfaEvents < 6 {
		t.Fatal("MFA actions not audited", mfaEvents, err)
	}
}

func mustCreateKey(t *testing.T, s *Store, id Identity) string {
	t.Helper()
	out, err := s.CreateKey(context.Background(), id, KeyInput{Name: "mfa-test", Access: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	return out.(map[string]any)["token"].(string)
}

func TestHTTPMFALogin(t *testing.T) {
	s := testStore(t)
	s.MFAKey = mfaTestKey(t)
	admin := loginAs(t, s, "admin@example.com")
	secret, _ := enableMFAFor(t, s, admin)
	ts := httptest.NewUnstartedServer(nil)
	origin := "http://" + ts.Listener.Addr().String()
	app, err := NewApp(s, Config{PublicURL: origin})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = app.Handler()
	ts.Start()
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	post := func(path string, input any) (int, map[string]any, *http.Response) {
		t.Helper()
		b, _ := json.Marshal(input)
		r, _ := http.NewRequest("POST", ts.URL+path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var data map[string]any
		_ = json.Unmarshal(raw, &data)
		return res.StatusCode, data, res
	}
	status, data, res := post("/api/login", map[string]any{"email": "admin@example.com", "password": testPassword})
	if status != 200 || data["data"].(map[string]any)["mfa_required"] != true {
		t.Fatal("expected MFA step", status, data)
	}
	for _, c := range res.Cookies() {
		if c.Name == "kb_session" && c.MaxAge >= 0 {
			t.Fatal("session issued before the second factor")
		}
		if c.Name == "kb_mfa" && (!c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/api/login/mfa") {
			t.Fatal("challenge cookie flags missing")
		}
	}
	if status, _, _ := post("/api/login/mfa", map[string]any{"code": "badbad-bad"}); status != 401 {
		t.Fatal("wrong code accepted", status)
	}
	code := hotp(secret, time.Now().Unix()/totpPeriod+1, totpDigits)
	if status, data, _ := post("/api/login/mfa", map[string]any{"code": code}); status != 200 {
		t.Fatal("MFA login failed", status, data)
	}
	r, _ := http.NewRequest("GET", ts.URL+"/api/me", nil)
	res, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var me map[string]any
	_ = json.NewDecoder(res.Body).Decode(&me)
	if res.StatusCode != 200 || me["data"].(map[string]any)["mfa_enabled"] != true {
		t.Fatal("/api/me after MFA login", res.StatusCode, me)
	}
}
