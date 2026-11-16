package kb

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFirstRunSetupCreatesTheOnlyAdministrator(t *testing.T) {
	s := emptyTestStore(t)
	const code = "setup-code-for-tests-only-0001"
	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080", SetupCode: code})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	status := func() map[string]any {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/setup", nil))
		var out struct {
			Data map[string]any `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("setup status: %d %s", w.Code, w.Body)
		}
		return out.Data
	}
	post := func(origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/setup", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	body := func(code, email string) string {
		b, _ := json.Marshal(map[string]string{"code": code, "name": "첫 관리자", "email": email, "password": testPassword})
		return string(b)
	}
	const origin = "http://localhost:8080"
	if d := status(); d["required"] != true || d["available"] != true {
		t.Fatalf("fresh install must require setup: %v", d)
	}
	if w := post("https://evil.example", body(code, "first@example.com")); w.Code != 403 {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	if w := post(origin, body("wrong-code", "first@example.com")); w.Code != 403 {
		t.Fatalf("wrong code: %d %s", w.Code, w.Body)
	}
	if w := post(origin, body("", "first@example.com")); w.Code != 403 {
		t.Fatalf("empty code: %d", w.Code)
	}
	if w := post(origin, `{"code":"`+code+`","name":"첫 관리자","email":"first@example.com","password":"short"}`); w.Code != 400 {
		t.Fatalf("short password: %d", w.Code)
	}
	if d := status(); d["required"] != true {
		t.Fatal("rejected attempts must not complete setup")
	}
	var denied int
	if err := s.DB.QueryRow("SELECT count(*) FROM audit_events WHERE action='bootstrap' AND outcome='denied'").Scan(&denied); err != nil || denied != 2 {
		t.Fatalf("denied attempts audited: %d %v", denied, err)
	}
	if w := post(origin, body(code, "First@Example.com")); w.Code != 200 {
		t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	if d := status(); d["required"] != false {
		t.Fatal("setup must close once an administrator exists")
	}
	// The correct code is useless afterwards.
	if w := post(origin, body(code, "second@example.com")); w.Code != 409 {
		t.Fatalf("second setup: %d %s", w.Code, w.Body)
	}
	id := loginAs(t, s, "first@example.com")
	if id.User.GlobalRole != "admin" || id.User.Name != "첫 관리자" {
		t.Fatalf("first user: %+v", id.User)
	}
	var users int
	if err := s.DB.QueryRow("SELECT count(*) FROM users").Scan(&users); err != nil || users != 1 {
		t.Fatalf("users: %d %v", users, err)
	}
}

func TestSetupUnavailableWithoutCodeOrOnExistingServer(t *testing.T) {
	s := emptyTestStore(t)
	if err := s.Setup(context.Background(), "", "", "a@example.com", "A", testPassword); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("empty server code must not allow setup: %v", err)
	}
	if err := s.Bootstrap(context.Background(), "admin@example.com", "관리자", testPassword); err != nil {
		t.Fatal(err)
	}
	if needed, err := s.NeedsSetup(context.Background()); err != nil || needed {
		t.Fatalf("needs setup after bootstrap: %v %v", needed, err)
	}
	if err := s.Setup(context.Background(), "code-code-code-code-code", "code-code-code-code-code", "b@example.com", "B", testPassword); errorCode(err) != "CONFLICT" {
		t.Fatalf("setup on existing server: %v", err)
	}
}
