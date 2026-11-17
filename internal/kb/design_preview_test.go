package kb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDesignPreviewPage(t *testing.T) {
	page := designPreviewPage(`<header class="a">x</header>`, `.a{color:red}</STYLE><script>alert(1)</script>`, true)
	if !strings.Contains(page, `<html lang="ko" class="dark"`) || !strings.Contains(page, `<header class="a">x</header>`) {
		t.Fatalf("page: %s", page)
	}
	// CSS cannot close its style element early.
	if strings.Contains(strings.ToLower(page), "</style><script>") || !strings.Contains(page, `<\/style><script>`) {
		t.Fatalf("style escape: %s", page)
	}
	if strings.Contains(designPreviewPage("", "", false), `class="dark"`) {
		t.Fatal("light page marked dark")
	}
	for _, want := range []string{"default-src 'none'", "sandbox", "frame-ancestors 'self'", "form-action 'none'"} {
		if !strings.Contains(designPreviewCSP, want) {
			t.Errorf("policy lacks %q", want)
		}
	}
	if strings.Contains(designPreviewCSP, "script-src") || strings.Contains(designPreviewCSP, "allow-") {
		t.Errorf("policy allows script: %s", designPreviewCSP)
	}
}

func TestDesignPreview(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Objects = &memoryObjects{files: map[string][]byte{}}
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "owner@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "design", Name: "Design"}).(Project)
	upload := func(name, body string) {
		t.Helper()
		out := mustCall(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: name, SizeBytes: int64(len(body))}).(map[string]any)
		if _, err := s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, out["attachment"].(Attachment).ID}, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	upload("site.css", ".bg-jacarta-700{background:#1c1d2e}")
	upload("logo.png", "\x89PNG\r\n\x1a\n"+strings.Repeat("\x00", 40))

	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	session := func(email string) (string, string) {
		t.Helper()
		cookie, err := s.Login(ctx, email, testPassword)
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.Session(ctx, cookie)
		if err != nil {
			t.Fatal(err)
		}
		return cookie, id.CSRF
	}
	post := func(cookie, origin string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/design-preview", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "kb_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	cookie, csrf := session("owner@example.com")
	const origin = "http://localhost:8080"
	markup := `<header class="bg-jacarta-700"><script>alert(1)</script>헤더</header>`

	// Every response, error pages included, is sandboxed and frameable only by this origin.
	checkHeaders := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		h := w.Header()
		if h.Get("Content-Security-Policy") != designPreviewCSP || h.Get("X-Frame-Options") != "SAMEORIGIN" ||
			h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s headers: %v", name, h)
		}
	}
	for name, c := range map[string]struct {
		cookie, origin string
		form           url.Values
		code           int
	}{
		"anonymous":    {"", origin, url.Values{"html": {markup}}, 401},
		"foreign site": {cookie, "https://evil.example", url.Values{"csrf": {csrf}, "html": {markup}}, 403},
		"no csrf":      {cookie, origin, url.Values{"html": {markup}}, 403},
		"too large":    {cookie, origin, url.Values{"csrf": {csrf}, "html": {strings.Repeat("a", maxDesignPreviewHTML+1)}}, 400},
		"not css":      {cookie, origin, url.Values{"csrf": {csrf}, "html": {markup}, "project_id": {p.ID}, "css": {"logo.png"}}, 400},
		"missing css":  {cookie, origin, url.Values{"csrf": {csrf}, "html": {markup}, "project_id": {p.ID}, "css": {"other.css"}}, 404},
		"no project":   {cookie, origin, url.Values{"csrf": {csrf}, "html": {markup}, "css": {"site.css"}}, 400},
	} {
		w := post(c.cookie, c.origin, c.form)
		if w.Code != c.code {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
		checkHeaders(name, w)
	}
	// Form navigations under no-referrer send Origin: null; only a same-origin fetch site passes.
	nullOrigin := func(site string) int {
		r := httptest.NewRequest("POST", "/api/design-preview", strings.NewReader(url.Values{"csrf": {csrf}, "html": {"<p>x</p>"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "null")
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		r.AddCookie(&http.Cookie{Name: "kb_session", Value: cookie})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if nullOrigin("same-origin") != 200 || nullOrigin("cross-site") != 403 || nullOrigin("") != 403 {
		t.Fatal("null origin handling")
	}
	// A non-member cannot read the project's CSS.
	adminCookie, adminCSRF := session("admin@example.com")
	if w := post(adminCookie, origin, url.Values{"csrf": {adminCSRF}, "html": {markup}, "project_id": {p.ID}, "css": {"site.css"}}); w.Code != 404 ||
		strings.Contains(w.Body.String(), "jacarta") {
		t.Fatalf("non-member css: %d %s", w.Code, w.Body.String())
	}

	w := post(cookie, origin, url.Values{"csrf": {csrf}, "html": {markup}, "project_id": {p.ID}, "css": {"SITE.css"}, "theme": {"dark"}})
	checkHeaders("preview", w)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, ".bg-jacarta-700{background:#1c1d2e}") || !strings.Contains(body, markup) || !strings.Contains(body, `class="dark"`) {
		t.Fatalf("preview: %d %s", w.Code, body)
	}
	w = post(cookie, origin, url.Values{"csrf": {csrf}, "html": {"<p>plain</p>"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<p>plain</p>") || strings.Contains(w.Body.String(), `class="dark"`) {
		t.Fatalf("preview without css: %d %s", w.Code, w.Body.String())
	}
}
