package kb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInlineType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 40))
	for _, c := range []struct {
		name, file string
		head       []byte
		imagesOnly bool
		want       string
		sandbox    bool
	}{
		{"image", "a.png", png, false, "image/png", true},
		{"image named as text", "a.txt", png, true, "image/png", true},
		{"pdf", "a.pdf", []byte("%PDF-1.7\n"), false, "application/pdf", false},
		{"pdf on the image route", "a.pdf", []byte("%PDF-1.7\n"), true, "", false},
		{"text", "notes.MD", []byte("# 메모\n본문"), false, "text/plain; charset=utf-8", true},
		{"text on the image route", "a.txt", []byte("hello"), true, "", false},
		{"text without a text name", "a.bin", []byte("hello"), false, "", false},
		{"html named as text", "a.txt", []byte("<!DOCTYPE html><script>alert(1)</script>"), false, "", false},
		{"html page", "report.HTML", []byte("<!DOCTYPE html><html><body>보고서</body></html>"), false, "text/html; charset=utf-8", true},
		{"html fragment", "frag.htm", []byte("<div>표</div>"), false, "text/html; charset=utf-8", true},
		{"html on the image route", "a.html", []byte("<!DOCTYPE html>"), true, "", false},
		{"binary named as html", "a.html", []byte("\x00\x01\x02\x03binary"), false, "", false},
		{"pdf named as html", "a.html", []byte("%PDF-1.7\n"), false, "application/pdf", false},
		{"svg named as html", "a.html", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), false, "", false},
		{"svg", "a.svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), false, "", false},
		{"svg named as png", "a.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), false, "", false},
		{"office file", "a.docx", []byte("PK\x03\x04rest"), false, "", false},
	} {
		kind, csp, ok := inlineType(c.file, c.head, c.imagesOnly)
		if ok != (c.want != "") || kind != c.want {
			t.Errorf("%s: got %q ok=%v, want %q", c.name, kind, ok, c.want)
			continue
		}
		if ok && (!strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "sandbox") != c.sandbox) {
			t.Errorf("%s: unexpected policy %q", c.name, csp)
		}
	}
}

func TestAttachmentPreview(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Objects = &memoryObjects{files: map[string][]byte{}}
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "owner@example.com")
	viewer := inviteAs(t, s, admin, "viewer@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "preview", Name: "Preview"}).(Project)
	other := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "preview-other", Name: "Other"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})

	upload := func(name, body string) Attachment {
		t.Helper()
		out := mustCall(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: name, SizeBytes: int64(len(body))}).(map[string]any)
		f := out["attachment"].(Attachment)
		if _, err := s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, f.ID}, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
		return f
	}
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 40)
	pdf := "%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"
	image, document := upload("화면.png", png), upload("기획서.pdf", pdf)
	notes := upload("메모.md", "# 메모\n<script>alert(1)</script>\n")
	long := upload("big.log", strings.Repeat("0123456789abcde\n", maxPreviewTextBytes/16+10))
	page := upload("page.txt", "<!DOCTYPE html><html><body><script>alert(1)</script></body></html>")
	report := upload("보고서.html", "<!DOCTYPE html><html><body><h1>보고서</h1><script>alert(1)</script></body></html>")
	office := upload("표.xlsx", "PK\x03\x04"+strings.Repeat("\x00", 30))
	pending := mustCall(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: "later.png", SizeBytes: 10}).(map[string]any)["attachment"].(Attachment)

	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	sessions := map[string]string{} // one login per user, under the login rate limit
	get := func(email, projectID, id string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/attachments/"+id+"/preview?project_id="+projectID, nil)
		if email != "" {
			cookie, ok := sessions[email]
			if !ok {
				var err error
				if cookie, err = s.Login(ctx, email, testPassword); err != nil {
					t.Fatal(err)
				}
				sessions[email] = cookie
			}
			r.AddCookie(&http.Cookie{Name: "kb_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// Session members of the file's project only; an MCP key is not accepted.
	if w := get("", p.ID, image.ID); w.Code != 401 {
		t.Fatalf("anonymous preview: %d", w.Code)
	}
	keyResult, err := s.CreateKey(ctx, owner, KeyInput{Name: "preview", Access: "read_write", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/attachments/"+image.ID+"/preview?project_id="+p.ID, nil)
	r.Header.Set("Authorization", "Bearer "+keyResult.(map[string]any)["token"].(string))
	keyed := httptest.NewRecorder()
	handler.ServeHTTP(keyed, r)
	if keyed.Code != 401 {
		t.Fatalf("preview with an MCP key: %d", keyed.Code)
	}
	if w := get("admin@example.com", p.ID, image.ID); w.Code != 404 {
		t.Fatalf("non-member preview: %d", w.Code)
	}
	if w := get("viewer@example.com", other.ID, image.ID); w.Code == 200 {
		t.Fatal("file served under another project")
	}

	w := get("viewer@example.com", p.ID, image.ID)
	h := w.Header()
	if w.Code != 200 || w.Body.String() != png || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") || h.Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("image preview: %d %v", w.Code, h)
	}
	w = get("viewer@example.com", p.ID, document.ID)
	h = w.Header()
	if w.Code != 200 || w.Body.String() != pdf || h.Get("Content-Type") != "application/pdf" || h.Get("X-Frame-Options") != "DENY" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
		t.Fatalf("pdf preview: %d %v", w.Code, h)
	}
	// Text is sent as text/plain with nosniff and a sandbox, so markup in it never runs.
	w = get("viewer@example.com", p.ID, notes.ID)
	h = w.Header()
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<script>") || h.Get("Content-Type") != "text/plain; charset=utf-8" ||
		h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || h.Get("X-Preview-Truncated") != "" {
		t.Fatalf("text preview: %d %v", w.Code, h)
	}
	w = get("viewer@example.com", p.ID, long.ID)
	if w.Code != 200 || w.Body.Len() != maxPreviewTextBytes || w.Header().Get("X-Preview-Truncated") != "true" || w.Header().Get("Content-Length") != "1048576" {
		t.Fatalf("long text preview: %d, %d bytes, %v", w.Code, w.Body.Len(), w.Header())
	}
	// HTML renders in a sandbox: opaque origin, no script, nothing fetched, framed only by this app.
	w = get("viewer@example.com", p.ID, report.ID)
	h = w.Header()
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>보고서</h1>") || h.Get("Content-Type") != "text/html; charset=utf-8" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != htmlPreviewCSP || h.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("html preview: %d %v", w.Code, h)
	}
	for _, want := range []string{"sandbox;", "default-src 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(htmlPreviewCSP, want) {
			t.Fatalf("html policy lacks %q", want)
		}
	}
	if strings.Contains(htmlPreviewCSP, "allow-") || strings.Contains(htmlPreviewCSP, "script-src") {
		t.Fatalf("html policy lets the page act: %q", htmlPreviewCSP)
	}
	// Not shown inline: HTML under a text name, office files, files still uploading.
	for name, f := range map[string]Attachment{"html": page, "office": office} {
		if w := get("viewer@example.com", p.ID, f.ID); w.Code != 400 || strings.Contains(w.Body.String(), "alert(1)") {
			t.Fatalf("%s served inline: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := get("viewer@example.com", p.ID, pending.ID); w.Code != 409 {
		t.Fatalf("pending file preview: %d", w.Code)
	}
	mustCall(t, tools, owner, "delete_attachment", AttachmentInput{p.ID, image.ID})
	if w := get("viewer@example.com", p.ID, image.ID); w.Code != 404 {
		t.Fatalf("deleted file preview: %d", w.Code)
	}
	var audited int
	if err := s.DB.QueryRow(`SELECT count(*) FROM audit_events WHERE action='preview_attachment' AND outcome='success'`).Scan(&audited); err != nil || audited < 4 {
		t.Fatalf("previews were not audited: %d %v", audited, err)
	}
}
