package kb

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestBuildCommitPrecedence(t *testing.T) {
	old := BuildCommit
	t.Cleanup(func() { BuildCommit = old })
	const built = "0123456789abcdef0123456789abcdef01234567"
	const deployed = "89abcdef0123456789abcdef0123456789abcdef"

	BuildCommit = built
	t.Setenv("RAILWAY_GIT_COMMIT_SHA", deployed)
	if c, src := buildCommit(); c != built || src != "build" {
		t.Fatalf("build flag must win: %s %s", c, src)
	}
	BuildCommit = ""
	if c, src := buildCommit(); c != deployed || src != "railway" {
		t.Fatalf("railway variable fallback: %s %s", c, src)
	}
	// Malformed values are ignored rather than echoed back.
	BuildCommit = "<script>"
	t.Setenv("RAILWAY_GIT_COMMIT_SHA", "not-a-sha")
	if c, src := buildCommit(); c == "<script>" || c == "not-a-sha" || (src != "vcs" && src != "vcs-modified" && src != "unknown") {
		t.Fatalf("malformed commit accepted: %s %s", c, src)
	}
}

func TestServerVersionReportsSchema(t *testing.T) {
	s := testStore(t)
	old := BuildCommit
	t.Cleanup(func() { BuildCommit = old })
	BuildCommit = "0123456789abcdef0123456789abcdef01234567"

	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/version", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d cache %q", w.Code, w.Header().Get("Cache-Control"))
	}
	var body struct {
		Data ServerVersion `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	v := body.Data
	if v.Commit != BuildCommit || v.CommitSource != "build" || v.SchemaExpected != len(migrationScripts) || v.SchemaVersion != v.SchemaExpected || !v.SchemaCurrent || v.StartedAt == "" {
		t.Fatalf("unexpected version: %+v", v)
	}

	// Removing the latest migration record shows the schema as behind.
	if _, err := s.DB.Exec(`DELETE FROM schema_migrations WHERE version=$1`, len(migrationScripts)); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, s, "admin@example.com")
	out := mustCall(t, NewTools(s), admin, "get_server_version", struct{}{}).(ServerVersion)
	if out.SchemaVersion != len(migrationScripts)-1 || out.SchemaCurrent {
		t.Fatalf("stale schema not reported: %+v", out)
	}
}
