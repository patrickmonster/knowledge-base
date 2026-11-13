package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testPassword = "test-only-long-password-123!"

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run real PostgreSQL integration tests")
	}
	ctx := context.Background()
	root, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "kbtest_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = root.DB.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	s, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close(); root.DB.Exec("DROP SCHEMA " + schema + " CASCADE"); root.DB.Close() })
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("migration is not repeatable:", err)
	}
	if err = s.Bootstrap(ctx, "admin@example.com", "관리자", testPassword); err != nil {
		t.Fatal(err)
	}
	return s
}
func loginAs(t *testing.T, s *Store, email string) Identity {
	t.Helper()
	raw, err := s.Login(context.Background(), email, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Session(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func inviteAs(t *testing.T, s *Store, admin Identity, email string) Identity {
	t.Helper()
	out, err := s.Invite(context.Background(), admin, InviteInput{Email: email, Name: email})
	if err != nil {
		t.Fatal(err)
	}
	raw := out.(map[string]any)["invitation_token"].(string)
	if err = s.AcceptInvitation(context.Background(), raw, testPassword); err != nil {
		t.Fatal(err)
	}
	if err = s.AcceptInvitation(context.Background(), raw, testPassword); err == nil {
		t.Fatal("invitation was reusable")
	}
	return loginAs(t, s, email)
}
func call(t *testing.T, tools Tools, id Identity, name string, input any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return tools[name].Call(context.Background(), id, raw)
}
func mustCall(t *testing.T, tools Tools, id Identity, name string, input any) any {
	t.Helper()
	out, err := call(t, tools, id, name, input)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}
func denied(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || errorCode(err) != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestTeamPermissionsAndHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "editor@example.com")
	viewer := inviteAs(t, s, admin, "viewer@example.com")
	outsider := inviteAs(t, s, admin, "outsider@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "payments", Name: "결제 프로젝트"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "private", Name: "비공개"}).(Project)
	for _, member := range []struct {
		id   Identity
		role string
	}{{editor, "editor"}, {viewer, "viewer"}} {
		mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: member.id.User.ID, Role: member.role})
	}
	in := DocumentInput{ProjectID: p.ID, Title: "결제 설계", Kind: "decision", Content: "결제를 위한 환불 정책을 기록합니다.", Tags: []string{"결제"}}
	d := mustCall(t, tools, editor, "create_document", in).(Document)
	_, err := call(t, tools, viewer, "create_document", in)
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, outsider, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: d.ID})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, outsider, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "결제"})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, outsider, "get_document_history", DocumentRef{ProjectID: p.ID, DocumentID: d.ID})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, editor, "set_project_member", MemberInput{ProjectID: p.ID, UserID: outsider.User.ID, Role: "editor"})
	denied(t, err, "FORBIDDEN")
	hits := mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "결제"}).(SearchResult).Hits
	if len(hits) != 1 || hits[0].DocumentID != d.ID {
		t.Fatalf("Korean retrieval failed: %#v", hits)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := fmt.Sprintf("수정 버전 %d", i)
			raw, _ := json.Marshal(UpdateInput{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: 1, Reason: "동시 수정 검증", Patch: DocumentPatch{Content: &text}})
			_, err := tools["update_document"].Call(ctx, editor, raw)
			errorsCh <- err
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	ok, conflicts := 0, 0
	for err := range errorsCh {
		if err == nil {
			ok++
		} else if errorCode(err) == "REVISION_CONFLICT" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("concurrent updates: %d succeeded, %d conflicted", ok, conflicts)
	}
	old := mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: d.ID, Revision: 1}).(Document)
	if old.Content != in.Content {
		t.Fatal("old revision was lost")
	}
	keyOut, err := s.CreateKey(ctx, editor, KeyInput{Name: "read-only", Access: "read_only", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	keyMap := keyOut.(map[string]any)
	key, err := s.APIKey(ctx, keyMap["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "create_document", in)
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, key, "list_documents", ListInput{ProjectID: other.ID})
	denied(t, err, "FORBIDDEN")
	ps := mustCall(t, tools, key, "list_projects", PageInput{}).([]Project)
	if len(ps) != 1 || ps[0].ID != p.ID {
		t.Fatal("key scope leaked projects", ps)
	}
	if _, err = s.RevokeKey(ctx, editor, keyMap["id"].(string)); err != nil {
		t.Fatal(err)
	}
	_, err = s.APIKey(ctx, keyMap["token"].(string))
	denied(t, err, "UNAUTHORIZED")
	_, err = call(t, tools, key, "list_documents", ListInput{ProjectID: p.ID})
	denied(t, err, "UNAUTHORIZED")
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "remove"})
	_, err = call(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: d.ID})
	denied(t, err, "NOT_FOUND")
	_, err = s.Users(ctx, editor)
	denied(t, err, "FORBIDDEN")
	_, err = s.AuditLog(ctx, editor, AuditFilter{})
	denied(t, err, "FORBIDDEN")
	events, err := s.AuditLog(ctx, admin, AuditFilter{Outcome: "denied"})
	if err != nil || len(events.([]map[string]any)) < 8 {
		t.Fatalf("denied actions not audited: %v %v", events, err)
	}
	var raw string
	if err = s.DB.QueryRow(`SELECT coalesce(string_agg(details::text,' '),'') FROM audit_events`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, testPassword) || strings.Contains(raw, keyMap["token"].(string)) || strings.Contains(raw, in.Content) {
		t.Fatal("secrets leaked to audit log")
	}
	var role string
	if err = s.DB.QueryRow(`SELECT details->>'role' FROM audit_events WHERE action='set_project_member' AND target_id=$1 ORDER BY id DESC LIMIT 1`, viewer.User.ID).Scan(&role); err != nil || role != "remove" {
		t.Fatalf("permission change target/details missing: %s %v", role, err)
	}
	if _, err = s.DB.Exec("ALTER TABLE audit_events RENAME TO temporarily_unavailable_audit"); err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, admin, "create_project", CreateProjectInput{Slug: "must-rollback", Name: "must rollback"})
	if err == nil {
		t.Fatal("write succeeded without audit")
	}
	if _, err = s.DB.Exec("ALTER TABLE temporarily_unavailable_audit RENAME TO audit_events"); err != nil {
		t.Fatal(err)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM projects WHERE slug='must-rollback'").Scan(&count)
	if count != 0 {
		t.Fatal("mutation did not roll back when audit failed")
	}
}

func TestAccountLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "member@example.com")
	out, err := s.CreateKey(ctx, member, KeyInput{Name: "before-reset", Access: "read_write"})
	if err != nil {
		t.Fatal(err)
	}
	rawKey := out.(map[string]any)["token"].(string)
	reset, err := s.Invite(ctx, admin, InviteInput{ResetUserID: member.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AcceptInvitation(ctx, reset.(map[string]any)["invitation_token"].(string), testPassword); err != nil {
		t.Fatal(err)
	}
	_, err = s.APIKey(ctx, rawKey)
	denied(t, err, "UNAUTHORIZED")
	_, err = call(t, NewTools(s), member, "whoami", struct{}{})
	denied(t, err, "UNAUTHORIZED")
	member = loginAs(t, s, "member@example.com")
	if _, err = s.UpdateUser(ctx, admin, UserUpdate{UserID: member.User.ID, Disabled: true, GlobalRole: "member"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Login(ctx, "member@example.com", testPassword)
	denied(t, err, "UNAUTHORIZED")
	var anonymous bool
	var target string
	if err = s.DB.QueryRow(`SELECT actor_id IS NULL,target_id FROM audit_events WHERE action='login' AND outcome='denied' ORDER BY id DESC LIMIT 1`).Scan(&anonymous, &target); err != nil {
		t.Fatal(err)
	}
	if !anonymous || target != member.User.ID {
		t.Fatal("failed login must identify the attempted account, not an authenticated actor")
	}
	_, err = call(t, NewTools(s), member, "whoami", struct{}{})
	denied(t, err, "UNAUTHORIZED")
	if _, err = s.UpdateUser(ctx, admin, UserUpdate{UserID: member.User.ID, Disabled: false, GlobalRole: "member"}); err != nil {
		t.Fatal(err)
	}
	member = loginAs(t, s, "member@example.com")
	if _, err = s.ChangePassword(ctx, member, testPassword, "brand-new-long-password-123"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Login(ctx, "member@example.com", testPassword)
	denied(t, err, "UNAUTHORIZED")
	if _, err = s.Login(ctx, "member@example.com", "brand-new-long-password-123"); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		_, _ = s.Login(ctx, "unknown@example.com", "invalid-password")
	}
	_, err = s.Login(ctx, "unknown@example.com", "invalid-password")
	denied(t, err, "RATE_LIMITED")
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(clone)
}
func TestHTTPLoginAndMCP(t *testing.T) {
	s := testStore(t)
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
	csrf := ""
	request := func(method, path string, input any, originHeader string) (int, map[string]any, *http.Response) {
		t.Helper()
		var body io.Reader
		if input != nil {
			b, _ := json.Marshal(input)
			body = bytes.NewReader(b)
		}
		r, _ := http.NewRequest(method, ts.URL+path, body)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", originHeader)
		r.Header.Set("X-CSRF-Token", csrf)
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
	status, _, _ := request("POST", "/api/login", map[string]any{"email": "admin@example.com", "password": testPassword}, "https://evil.example")
	if status != 403 {
		t.Fatal("cross-origin login accepted", status)
	}
	status, _, res := request("POST", "/api/login", map[string]any{"email": "admin@example.com", "password": testPassword}, origin)
	if status != 200 {
		t.Fatal("login failed", status)
	}
	if len(res.Cookies()) != 1 || !res.Cookies()[0].HttpOnly || res.Cookies()[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie flags missing")
	}
	status, data, _ := request("GET", "/api/me", nil, origin)
	if status != 200 {
		t.Fatal(data)
	}
	csrf = data["data"].(map[string]any)["csrf_token"].(string)
	saved := csrf
	csrf = "wrong"
	status, _, _ = request("POST", "/api/keys", KeyInput{Name: "test", Access: "read_only"}, origin)
	if status != 403 {
		t.Fatal("CSRF check bypassed")
	}
	csrf = saved
	status, data, _ = request("POST", "/api/keys", KeyInput{Name: "test", Access: "read_only"}, origin)
	if status != 200 {
		t.Fatal(data)
	}
	key := data["data"].(map[string]any)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{key["token"].(string)}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) < 10 {
		t.Fatalf("MCP tools unavailable: %v %v", listed, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("MCP user lookup failed: %v %v", result, err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "create_project", Arguments: map[string]any{"name": "blocked", "slug": "blocked"}})
	if err != nil || !result.IsError {
		t.Fatalf("MCP read-only key can write: %v %v", result, err)
	}
	status, _, _ = request("POST", "/api/keys/revoke", map[string]any{"id": key["id"]}, origin)
	if status != 200 {
		t.Fatal("key revocation failed")
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
	if err == nil {
		t.Fatal("revoked key remains usable over MCP")
	}
	status, _, _ = request("POST", "/api/logout", map[string]any{}, origin)
	if status != 200 {
		t.Fatal("logout failed")
	}
	status, _, _ = request("GET", "/api/me", nil, origin)
	if status != 401 {
		t.Fatal("logout did not invalidate session")
	}
}

func TestExpirationAndArchive(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	tools := NewTools(s)
	out, err := s.Invite(ctx, admin, InviteInput{Email: "expired@example.com", Name: "expired"})
	if err != nil {
		t.Fatal(err)
	}
	invite := out.(map[string]any)["invitation_token"].(string)
	if _, err = s.DB.Exec("UPDATE invitations SET expires_at=now()-interval '1 minute' WHERE token_hash=$1", digest(invite)); err != nil {
		t.Fatal(err)
	}
	if err = s.AcceptInvitation(ctx, invite, testPassword); err == nil {
		t.Fatal("expired invitation accepted")
	}
	out, err = s.CreateKey(ctx, admin, KeyInput{Name: "expired key", Access: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	key := out.(map[string]any)["token"].(string)
	if _, err = s.DB.Exec("UPDATE api_keys SET expires_at=now()-interval '1 minute' WHERE token_hash=$1", digest(key)); err != nil {
		t.Fatal(err)
	}
	_, err = s.APIKey(ctx, key)
	denied(t, err, "UNAUTHORIZED")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "archive", Name: "archive"}).(Project)
	d := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "오래된 결제 방침", Kind: "decision", Content: "보관한 결제 방침", Status: "archived"}).(Document)
	hits := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "결제"}).(SearchResult).Hits
	if len(hits) != 0 {
		t.Fatal("archived document appeared in default search")
	}
	hits = mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "결제", Status: "archived"}).(SearchResult).Hits
	if len(hits) != 1 || hits[0].DocumentID != d.ID {
		t.Fatal("explicit archived search failed")
	}
	if _, err = s.DB.Exec("UPDATE sessions SET expires_at=now()-interval '1 minute' WHERE token_hash=$1", admin.SessionHash); err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, admin, "whoami", struct{}{})
	denied(t, err, "UNAUTHORIZED")
}
