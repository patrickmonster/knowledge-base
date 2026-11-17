package kb

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"html"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// Design previews render an HTML code block from a document (```html preview)
// as a page inside a sandboxed iframe, so a design written in a document can be
// seen as it looks rather than as code.
//
// The browser submits the block with a same-origin form POST targeting the
// iframe. The response is the block wrapped in a minimal page and is fully
// isolated: CSP sandbox (opaque origin, no script, no forms, no navigation of
// the parent) and default-src 'none', so it cannot read this origin's cookies,
// call the API or load anything but inline styles and data/https images.
// Only this response may be framed, and only by this origin.
const (
	maxDesignPreviewHTML = 256 << 10
	maxDesignPreviewCSS  = 2 << 20
	designPreviewCSP     = "default-src 'none'; style-src 'unsafe-inline'; img-src data: https:; font-src data: https:; " +
		"sandbox; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
)

var styleCloser = regexp.MustCompile(`(?i)</style`)

// designPreviewPage builds the framed page. The CSS is inlined because the
// sandboxed page has an opaque origin and could not fetch it with the session.
func designPreviewPage(body, css string, dark bool) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="ko"`)
	if dark {
		b.WriteString(` class="dark" style="color-scheme:dark"`)
	}
	b.WriteString(`><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>디자인 미리보기</title>`)
	b.WriteString(`<style>body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",sans-serif}html.dark body{background:#111418;color:#f1f3f5}</style>`)
	if css != "" {
		b.WriteString(`<style>`)
		b.WriteString(styleCloser.ReplaceAllString(css, `<\/style`))
		b.WriteString(`</style>`)
	}
	b.WriteString(`</head><body>`)
	b.WriteString(body)
	b.WriteString(`</body></html>`)
	return b.String()
}

func designPreviewHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", designPreviewCSP)
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Cache-Control", "no-store")
}

// designPreviewError shows a failure inside the frame instead of a JSON body
// the frame would print as text.
func designPreviewError(w http.ResponseWriter, status int, message string) {
	designPreviewHeaders(w)
	w.WriteHeader(status)
	io.WriteString(w, designPreviewPage(`<p style="margin:16px;font:13px/1.6 system-ui,sans-serif;color:#8a3b2e">미리보기를 만들지 못했습니다: `+
		html.EscapeString(message)+`</p>`, "", false))
}

func designPreviewSameOrigin(r *http.Request, publicURL string) bool {
	switch origin := r.Header.Get("Origin"); origin {
	case publicURL:
		return true
	case "null":
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	return false
}

func (a *App) designPreview(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("kb_session")
	if err != nil {
		designPreviewError(w, http.StatusUnauthorized, "로그인이 필요합니다.")
		return
	}
	id, err := a.Store.Session(r.Context(), cookie.Value)
	if err != nil {
		designPreviewError(w, http.StatusUnauthorized, "로그인이 필요합니다.")
		return
	}
	// A form cannot send the X-CSRF-Token header, so the token comes as a field.
	// Under the page's Referrer-Policy: no-referrer a form navigation carries
	// Origin: null, so a same-origin Sec-Fetch-Site (set by the browser, not
	// by page script) is accepted in its place; the CSRF token is still required.
	if !designPreviewSameOrigin(r, a.Config.PublicURL) {
		designPreviewError(w, http.StatusForbidden, "허용되지 않은 요청 출처입니다.")
		return
	}
	if err := r.ParseForm(); err != nil {
		designPreviewError(w, http.StatusBadRequest, "요청이 너무 크거나 형식이 잘못되었습니다.")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(id.CSRF)) != 1 {
		designPreviewError(w, http.StatusForbidden, "CSRF 토큰이 올바르지 않습니다. 페이지를 새로 고치세요.")
		return
	}
	if err := a.Store.RateLimit(r.Context(), "web:"+id.User.ID, 240, 60); err != nil {
		designPreviewError(w, http.StatusTooManyRequests, "요청이 너무 많습니다. 잠시 후 다시 시도하세요.")
		return
	}
	body := r.PostForm.Get("html")
	if len(body) > maxDesignPreviewHTML {
		designPreviewError(w, http.StatusBadRequest, "코드 블록이 256 KiB를 넘습니다.")
		return
	}
	css := ""
	if name := strings.TrimSpace(r.PostForm.Get("css")); name != "" {
		css, err = a.designPreviewCSS(r, id, r.PostForm.Get("project_id"), name)
		if err != nil {
			status := http.StatusBadRequest
			if errorCode(err) == "NOT_FOUND" {
				status = http.StatusNotFound
			}
			designPreviewError(w, status, PublicError(err))
			return
		}
	}
	designPreviewHeaders(w)
	io.WriteString(w, designPreviewPage(body, css, r.PostForm.Get("theme") == "dark"))
}

// designPreviewCSS reads the newest ready .css attachment with this file name
// in a project the user can read.
func (a *App) designPreviewCSS(r *http.Request, id Identity, projectID, name string) (string, error) {
	if !strings.EqualFold(path.Ext(name), ".css") || len(name) > 255 {
		return "", problem("INVALID_INPUT", "css= must name a .css attachment.")
	}
	in := map[string]any{"project_id": projectID, "filename": name}
	out, err := a.Store.Perform(auditInput(r.Context(), in), id, "design_preview_css", projectID, false, false, func(tx *sql.Tx) (any, error) {
		if err := validID(projectID); err != nil {
			return nil, err
		}
		if _, err := projectReadAccess(r.Context(), tx, id.User.ID, projectID); err != nil {
			return nil, err
		}
		if err := a.Store.requireBucket(); err != nil {
			return nil, err
		}
		f, err := one[Attachment](r.Context(), tx, `SELECT to_jsonb(a)-'object_key' FROM attachments a
			WHERE project_id=$1 AND lower(filename)=lower($2) AND status='ready' ORDER BY created_at DESC LIMIT 1`, projectID, name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, problem("NOT_FOUND", "No ready attachment named "+name+" in this project.")
		}
		return f, err
	})
	if err != nil {
		return "", err
	}
	f := out.(Attachment)
	if f.SizeBytes > maxDesignPreviewCSS {
		return "", problem("INVALID_INPUT", "The CSS attachment is larger than 2 MiB.")
	}
	obj, err := a.Store.Objects.Get(r.Context(), attachmentKey(f.ProjectID, f.ID))
	if err != nil {
		return "", problem("STORAGE_UNAVAILABLE", "Could not read the CSS attachment.")
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, maxDesignPreviewCSS+1))
	if err != nil {
		return "", problem("STORAGE_UNAVAILABLE", "Could not read the CSS attachment.")
	}
	if !strings.HasPrefix(http.DetectContentType(data), "text/plain") {
		return "", problem("INVALID_INPUT", "The CSS attachment is not a text file.")
	}
	return string(data), nil
}
