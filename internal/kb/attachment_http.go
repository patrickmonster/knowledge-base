package kb

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

type attachmentStorageStatus struct {
	State     string    `json:"state"`
	CheckedAt time.Time `json:"checked_at"`
}

func (a *App) attachmentStorage(r *http.Request, id Identity) (any, error) {
	if err := a.Store.RateLimit(r.Context(), "storage-check:"+id.User.ID, 12, 60); err != nil {
		return nil, err
	}
	status := attachmentStorageStatus{State: "not_configured"}
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
	if auth := r.Header.Get("Authorization"); auth != "" {
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
