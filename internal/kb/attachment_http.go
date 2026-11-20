package kb

import (
	"bufio"
	"context"
	"crypto/subtle"
	"database/sql"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

type attachmentStorageStatus struct {
	State string `json:"state"`
	// Scanner is not_configured, connected or unavailable (clamd PING).
	// Uploads fail closed while a configured scanner is unavailable.
	Scanner   string    `json:"scanner"`
	CheckedAt time.Time `json:"checked_at"`
}

func (a *App) attachmentStorage(r *http.Request, id Identity) (any, error) {
	if err := a.Store.RateLimit(r.Context(), "storage-check:"+id.User.ID, 12, 60); err != nil {
		return nil, err
	}
	status := attachmentStorageStatus{State: "not_configured", Scanner: "not_configured"}
	if a.Store.Scanner != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		status.Scanner = "connected"
		if err := a.Store.Scanner.Ping(ctx); err != nil {
			// Never echo the private scanner address.
			status.Scanner = "unavailable"
		}
		cancel()
	}
	if a.Store.Objects != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		status.State = "connected"
		if err := a.Store.Objects.Check(ctx); err != nil {
			// SDK errors can contain private endpoints and request details.
			status.State = "unavailable"
		}
	}
	status.CheckedAt = time.Now().UTC()
	return status, nil
}

func (a *App) attachmentUpload(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.RateLimit(r.Context(), "upload-ip:"+a.clientIP(r), 120, 60); err != nil {
		a.fail(w, err)
		return
	}
	var id Identity
	var err error
	if raw := r.URL.Query().Get("upload_token"); raw != "" {
		// One-time link from prepare_attachment_upload. Never combined with other credentials.
		if r.Header.Get("Authorization") != "" {
			a.fail(w, problem("INVALID_INPUT", "Use either an upload link or a Bearer key, not both."))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != a.Config.PublicURL {
			a.fail(w, problem("FORBIDDEN", "Untrusted request origin."))
			return
		}
		id, err = a.Store.UploadTokenIdentity(r.Context(), r.PathValue("id"), r.URL.Query().Get("project_id"), raw)
	} else if auth := r.Header.Get("Authorization"); auth != "" {
		if origin := r.Header.Get("Origin"); origin != "" && origin != a.Config.PublicURL {
			a.fail(w, problem("FORBIDDEN", "Untrusted request origin."))
			return
		}
		raw, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok {
			a.fail(w, problem("UNAUTHORIZED", "Use a Bearer key."))
			return
		}
		id, err = a.Store.APIKey(r.Context(), raw)
	} else {
		cookie, e := r.Cookie("kb_session")
		if e != nil {
			a.fail(w, problem("UNAUTHORIZED", "Please sign in."))
			return
		}
		id, err = a.Store.Session(r.Context(), cookie.Value)
		if err == nil {
			if !a.origin(w, r) {
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(id.CSRF)) != 1 {
				a.fail(w, problem("FORBIDDEN", "Invalid CSRF token."))
				return
			}
		}
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if err = a.Store.RateLimit(r.Context(), "upload:"+id.User.ID, 30, 60); err != nil {
		a.fail(w, err)
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/octet-stream" {
		a.fail(w, problem("INVALID_INPUT", "PUT raw bytes as application/octet-stream."))
		return
	}
	out, err := a.Store.UploadAttachment(r.Context(), id, AttachmentInput{ProjectID: r.URL.Query().Get("project_id"), AttachmentID: r.PathValue("id")}, r.Body)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.json(w, out)
}

// attachmentImage streams an issue image to a signed-in member so the page can
// show it inline under img-src 'self'. Only issue images (checked as raster
// types on upload) are served, and the type is sniffed again before sending.
// With ?size=thumb it serves a small copy for the issue cards
// (serveIssueThumbnail).
func (a *App) attachmentImage(w http.ResponseWriter, r *http.Request) {
	a.attachmentInline(w, r, "view_issue_image", true)
}

// attachmentPreview streams a project file to a signed-in member for the
// preview on the 첨부 문서 page: raster images, PDF, plain text and HTML.
func (a *App) attachmentPreview(w http.ResponseWriter, r *http.Request) {
	a.attachmentInline(w, r, "preview_attachment", false)
}

// maxPreviewTextBytes caps a plain-text preview; larger files are cut there
// and marked with X-Preview-Truncated.
const maxPreviewTextBytes = 1 << 20

// previewTextExtensions are the file names shown as plain text. The bytes must
// also sniff as text, so a renamed HTML or binary file is not shown.
var previewTextExtensions = map[string]bool{".txt": true, ".md": true, ".csv": true, ".tsv": true, ".json": true, ".log": true, ".yaml": true, ".yml": true}

// previewHTMLExtensions are the file names rendered as a page in a sandboxed
// frame. The bytes must sniff as HTML or plain text.
var previewHTMLExtensions = map[string]bool{".html": true, ".htm": true}

// htmlPreviewCSP renders an HTML file without letting it act on this origin:
// sandbox (no allow-scripts, no allow-same-origin) gives it an opaque origin
// with scripts, forms, popups and top navigation off, even when the address is
// opened directly in a tab. Inline styles and data: images and fonts are
// allowed so the page keeps its look; nothing is fetched from any host, so a
// file cannot track who opened it. Only this app's own pages may frame it.
const htmlPreviewCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; frame-ancestors 'self'"

// inlineType decides from the first bytes, never from the stored name alone,
// how a file may be shown in the browser. It returns the Content-Type to send
// and the Content-Security-Policy for that response, or ok=false when the file
// must be downloaded instead.
//
// Images and text are sandboxed. A PDF opens in the browser's own viewer in a
// new tab, which a sandbox would block; default-src 'none' still stops it from
// loading anything, and the global X-Frame-Options keeps it out of frames.
// HTML (.html/.htm only) is served under htmlPreviewCSP, which turns script
// off and isolates it from this origin. SVG is never served inline.
func inlineType(filename string, head []byte, imagesOnly bool) (contentType, csp string, ok bool) {
	kind := http.DetectContentType(head)
	switch {
	case issueImageTypes[kind]:
		return kind, "default-src 'none'; sandbox", true
	case imagesOnly:
		return "", "", false
	case kind == "application/pdf":
		return kind, "default-src 'none'; frame-ancestors 'none'", true
	case previewHTMLExtensions[strings.ToLower(path.Ext(filename))] && (strings.HasPrefix(kind, "text/html") || strings.HasPrefix(kind, "text/plain")):
		return "text/html; charset=utf-8", htmlPreviewCSP, true
	case strings.HasPrefix(kind, "text/plain") && previewTextExtensions[strings.ToLower(path.Ext(filename))]:
		return kind, "default-src 'none'; sandbox", true
	}
	return "", "", false
}

// attachmentInline serves a ready file's bytes for display to a session user
// with read access to its project. It never accepts an MCP key or a link
// token, so the address can be shown in the page without leaking access.
func (a *App) attachmentInline(w http.ResponseWriter, r *http.Request, action string, issueImage bool) {
	cookie, err := r.Cookie("kb_session")
	if err != nil {
		a.fail(w, problem("UNAUTHORIZED", "Please sign in."))
		return
	}
	id, err := a.Store.Session(r.Context(), cookie.Value)
	if err != nil {
		a.fail(w, err)
		return
	}
	if err = a.Store.RateLimit(r.Context(), "image:"+id.User.ID, 600, 60); err != nil {
		a.fail(w, err)
		return
	}
	in := AttachmentInput{ProjectID: r.URL.Query().Get("project_id"), AttachmentID: r.PathValue("id")}
	out, err := a.Store.Perform(auditInput(r.Context(), in), id, action, in.ProjectID, false, false, func(tx *sql.Tx) (any, error) {
		f, err := readAttachment(r.Context(), tx, id, in, false)
		if err != nil {
			return nil, err
		}
		if err = a.Store.requireBucket(); err != nil {
			return nil, err
		}
		if issueImage && f.IssueID == nil {
			return nil, problem("NOT_FOUND", "Image not found.")
		}
		if f.Status != "ready" {
			if issueImage {
				return nil, problem("CONFLICT", "Image is not ready.")
			}
			return nil, problem("CONFLICT", "File is not ready.")
		}
		return f, nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	f := out.(Attachment)
	if issueImage && r.URL.Query().Get("size") == "thumb" {
		a.serveIssueThumbnail(w, r, f)
		return
	}
	body, err := a.Store.Objects.Get(r.Context(), attachmentKey(f.ProjectID, f.ID))
	if err != nil {
		if issueImage {
			a.fail(w, problem("STORAGE_UNAVAILABLE", "Could not read the image."))
		} else {
			a.fail(w, problem("STORAGE_UNAVAILABLE", "Could not read the file."))
		}
		return
	}
	defer body.Close()
	reader := bufio.NewReaderSize(io.LimitReader(body, f.SizeBytes), 512)
	head, _ := reader.Peek(512)
	kind, csp, ok := inlineType(f.Filename, head, issueImage)
	if !ok {
		if issueImage {
			a.fail(w, problem("NOT_FOUND", "Image not found."))
		} else {
			a.fail(w, problem("INVALID_INPUT", "This file type has no preview. Download it instead."))
		}
		return
	}
	size := f.SizeBytes
	h := w.Header()
	if strings.HasPrefix(kind, "text/plain") && size > maxPreviewTextBytes {
		size = maxPreviewTextBytes
		h.Set("X-Preview-Truncated", "true")
	}
	h.Set("Content-Type", kind)
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": f.Filename}))
	h.Set("Content-Security-Policy", csp)
	if csp == htmlPreviewCSP {
		// The preview dialog frames the page; frame-ancestors 'self' already
		// limits who may, and this keeps older browsers consistent with it.
		h.Set("X-Frame-Options", "SAMEORIGIN")
	}
	// File content never changes for an attachment ID; access is rechecked after expiry.
	h.Set("Cache-Control", "private, max-age=300")
	io.Copy(w, io.LimitReader(reader, size))
}
