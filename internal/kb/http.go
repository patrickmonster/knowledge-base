package kb

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed web/*
var webFiles embed.FS

type Config struct {
	PublicURL  string
	TrustProxy bool
}
type App struct {
	Store  *Store
	Tools  Tools
	Config Config
	secure bool
}

func NewApp(s *Store, c Config) (*App, error) {
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, problem("INVALID_INPUT", "PUBLIC_URL must be an origin, for example https://knowledge.example.com.")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, problem("INVALID_INPUT", "Public deployments require HTTPS.")
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	s.PublicURL = c.PublicURL
	return &App{Store: s, Tools: NewTools(s), Config: c, secure: u.Scheme == "https"}, nil
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/attachments/{id}/content", a.attachmentUpload)
	mux.HandleFunc("GET /api/attachments/{id}/image", a.attachmentImage)
	mux.HandleFunc("GET /api/attachments/{id}/preview", a.attachmentPreview)
	a.sessionRoute(mux, "GET /api/attachments/storage", a.attachmentStorage)
	mux.HandleFunc("GET /api/projects/{id}/export", a.projectExport)
	mux.HandleFunc("POST /api/projects/{id}/import", a.projectImport)
	mux.HandleFunc("GET /api/projects/{id}/agents.md", a.agentsDownload)
	mux.HandleFunc("POST /api/github/webhooks/{id}", a.githubWebhook)
	mux.HandleFunc("POST /api/discord/interactions", a.discordInteractions)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := a.Store.DB.PingContext(ctx); err != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		w.Write([]byte("ok"))
	})
	// Public, unauthenticated: commit and schema numbers only, so operators can
	// confirm which build and migrations are live without platform access.
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		v, err := serverVersion(ctx, a.Store.DB)
		if err != nil {
			commit, source := buildCommit()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"commit": commit, "commit_source": source, "schema_expected": len(migrationScripts)}, "error": map[string]string{"code": "DATABASE_UNAVAILABLE", "message": "Schema version is unavailable."}})
			return
		}
		a.json(w, v)
	})
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		if !a.origin(w, r) {
			return
		}
		if err := a.Store.RateLimit(r.Context(), "login-ip:"+a.clientIP(r), 50, 900); err != nil {
			a.fail(w, err)
			return
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		result, err := a.Store.BeginLogin(r.Context(), in.Email, in.Password)
		if err != nil {
			a.fail(w, err)
			return
		}
		if result.MFAToken != "" {
			http.SetCookie(w, a.mfaCookie(result.MFAToken, int(mfaChallengeTTL.Seconds())))
			a.json(w, map[string]any{"ok": false, "mfa_required": true})
			return
		}
		http.SetCookie(w, a.cookie(result.Session, 12*3600))
		a.json(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/login/mfa", func(w http.ResponseWriter, r *http.Request) {
		if !a.origin(w, r) {
			return
		}
		if err := a.Store.RateLimit(r.Context(), "mfa-ip:"+a.clientIP(r), 50, 900); err != nil {
			a.fail(w, err)
			return
		}
		var in struct {
			Code string `json:"code"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		cookie, err := r.Cookie("kb_mfa")
		if err != nil {
			a.fail(w, problem("UNAUTHORIZED", "Sign-in expired. Enter your email and password again."))
			return
		}
		raw, err := a.Store.CompleteMFALogin(r.Context(), cookie.Value, in.Code)
		if err != nil {
			// Keep the challenge for another try; otherwise the password step starts over.
			if code := errorCode(err); code != "RATE_LIMITED" && code != "MFA_INVALID_CODE" {
				http.SetCookie(w, a.mfaCookie("", -1))
			}
			a.fail(w, err)
			return
		}
		http.SetCookie(w, a.mfaCookie("", -1))
		http.SetCookie(w, a.cookie(raw, 12*3600))
		a.json(w, map[string]any{"ok": true})
	})
	// Public: tells the console whether to show first-run setup. It reveals
	// only whether an administrator exists.
	mux.HandleFunc("GET /api/setup", func(w http.ResponseWriter, r *http.Request) {
		needed, err := a.Store.NeedsSetup(r.Context())
		if err != nil {
			a.fail(w, err)
			return
		}
		a.json(w, map[string]any{"required": needed})
	})
	mux.HandleFunc("POST /api/setup", func(w http.ResponseWriter, r *http.Request) {
		if !a.origin(w, r) {
			return
		}
		if err := a.Store.RateLimit(r.Context(), "setup-ip:"+a.clientIP(r), 10, 900); err != nil {
			a.fail(w, err)
			return
		}
		var in struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if err := a.Store.Setup(r.Context(), in.Email, in.Name, in.Password); err != nil {
			a.fail(w, err)
			return
		}
		a.json(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/accept-invitation", func(w http.ResponseWriter, r *http.Request) {
		if !a.origin(w, r) {
			return
		}
		if err := a.Store.RateLimit(r.Context(), "invite-ip:"+a.clientIP(r), 20, 900); err != nil {
			a.fail(w, err)
			return
		}
		var in struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if err := a.Store.AcceptInvitation(r.Context(), in.Token, in.Password); err != nil {
			a.fail(w, err)
			return
		}
		a.json(w, map[string]any{"ok": true})
	})
	a.sessionRoute(mux, "GET /api/me", func(r *http.Request, id Identity) (any, error) {
		mfa, err := a.Store.mfaEnabled(r.Context(), a.Store.DB, id.User.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"user": id.User, "csrf_token": id.CSRF, "mcp_url": a.Config.PublicURL + "/mcp", "mfa_enabled": mfa}, nil
	})
	a.sessionRoute(mux, "GET /api/mfa", func(r *http.Request, id Identity) (any, error) { return a.Store.GetMFAStatus(r.Context(), id) })
	a.sessionRoute(mux, "POST /api/mfa/setup", func(r *http.Request, id Identity) (any, error) {
		var in struct{}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.SetupMFA(r.Context(), id)
	})
	a.sessionRoute(mux, "POST /api/mfa/enable", func(r *http.Request, id Identity) (any, error) {
		var in struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.EnableMFA(r.Context(), id, in.Password, in.Code)
	})
	a.sessionRoute(mux, "POST /api/mfa/disable", func(r *http.Request, id Identity) (any, error) {
		var in struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.DisableMFA(r.Context(), id, in.Password, in.Code)
	})
	a.sessionRoute(mux, "POST /api/mfa/recovery-codes", func(r *http.Request, id Identity) (any, error) {
		var in struct {
			Code string `json:"code"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.RegenerateRecoveryCodes(r.Context(), id, in.Code)
	})
	a.sessionRoute(mux, "POST /api/admin/users/mfa-reset", func(r *http.Request, id Identity) (any, error) {
		var in MFAResetInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.ResetMFA(r.Context(), id, in)
	})
	a.sessionRoute(mux, "POST /api/logout", func(r *http.Request, id Identity) (any, error) {
		return a.Store.Perform(r.Context(), id, "logout", "", true, false, func(tx *sql.Tx) (any, error) {
			_, err := tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=$1", id.SessionHash)
			return map[string]any{"ok": true}, err
		})
	})
	a.sessionRoute(mux, "POST /api/password", func(r *http.Request, id Identity) (any, error) {
		var in struct {
			Current string `json:"current"`
			Next    string `json:"next"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.ChangePassword(r.Context(), id, in.Current, in.Next)
	})
	a.sessionRoute(mux, "GET /api/keys", func(r *http.Request, id Identity) (any, error) { return a.Store.ListKeys(r.Context(), id) })
	a.sessionRoute(mux, "POST /api/keys", func(r *http.Request, id Identity) (any, error) {
		var in KeyInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.CreateKey(r.Context(), id, in)
	})
	a.sessionRoute(mux, "POST /api/keys/revoke", func(r *http.Request, id Identity) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.RevokeKey(r.Context(), id, in.ID)
	})
	a.sessionRoute(mux, "GET /api/admin/users", func(r *http.Request, id Identity) (any, error) { return a.Store.Users(r.Context(), id) })
	a.sessionRoute(mux, "POST /api/admin/invite", func(r *http.Request, id Identity) (any, error) {
		var in InviteInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		out, err := a.Store.Invite(r.Context(), id, in)
		if err == nil {
			m := out.(map[string]any)
			m["invitation_url"] = a.Config.PublicURL + "/#invite=" + m["invitation_token"].(string)
			delete(m, "invitation_token")
		}
		return out, err
	})
	a.sessionRoute(mux, "POST /api/admin/users", func(r *http.Request, id Identity) (any, error) {
		var in UserUpdate
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.UpdateUser(r.Context(), id, in)
	})
	a.sessionRoute(mux, "POST /api/admin/audit", func(r *http.Request, id Identity) (any, error) {
		var in AuditFilter
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.AuditLog(r.Context(), id, in)
	})
	a.sessionRoute(mux, "GET /api/admin/stats", func(r *http.Request, id Identity) (any, error) { return a.Store.Stats(r.Context(), id) })
	a.sessionRoute(mux, "GET /api/admin/discord", func(r *http.Request, id Identity) (any, error) { return a.Store.DiscordSettingsStatus(r.Context(), id) })
	a.sessionRoute(mux, "POST /api/admin/discord", func(r *http.Request, id Identity) (any, error) {
		var in DiscordSettingsInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.SetDiscordSettings(r.Context(), id, in)
	})
	a.sessionRoute(mux, "POST /api/admin/discord/channels", func(r *http.Request, id Identity) (any, error) {
		var in DiscordChannelInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.SetDiscordChannel(r.Context(), id, in)
	})
	a.sessionRoute(mux, "POST /api/admin/discord/register-commands", func(r *http.Request, id Identity) (any, error) {
		var in DiscordRegisterInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.RegisterDiscordCommands(r.Context(), id, in)
	})
	a.sessionRoute(mux, "GET /api/discord/link", func(r *http.Request, id Identity) (any, error) { return a.Store.DiscordLink(r.Context(), id) })
	a.sessionRoute(mux, "POST /api/discord/link-code", func(r *http.Request, id Identity) (any, error) { return a.Store.CreateDiscordLinkCode(r.Context(), id) })
	a.sessionRoute(mux, "POST /api/discord/unlink", func(r *http.Request, id Identity) (any, error) { return a.Store.UnlinkDiscord(r.Context(), id) })
	a.sessionRoute(mux, "GET /api/admin/retention", func(r *http.Request, id Identity) (any, error) { return a.Store.RetentionStatus(r.Context(), id) })
	a.sessionRoute(mux, "POST /api/admin/retention", func(r *http.Request, id Identity) (any, error) {
		var in RetentionInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.SetRetention(r.Context(), id, in)
	})
	a.sessionRoute(mux, "POST /api/vectors/map", func(r *http.Request, id Identity) (any, error) {
		var in VectorMapInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return a.Store.VectorMap(r.Context(), id, in)
	})
	a.sessionRoute(mux, "POST /api/vectors/neighbors", func(r *http.Request, id Identity) (any, error) {
		var in VectorNeighborInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		out, err := a.Store.VectorNeighbors(r.Context(), id, in)
		return out, notFound(err)
	})
	a.sessionRoute(mux, "POST /api/tools/{name}", func(r *http.Request, id Identity) (any, error) {
		tool, ok := a.Tools[r.PathValue("name")]
		if !ok {
			return nil, problem("NOT_FOUND", "Unknown tool.")
		}
		var raw json.RawMessage
		if err := decode(r, &raw); err != nil {
			return nil, err
		}
		return tool.Call(r.Context(), id, raw)
	})
	for _, method := range []string{"GET", "POST", "DELETE"} {
		mux.HandleFunc(method+" /mcp", a.mcp)
	}
	assets, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; script-src 'self'; style-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if a.secure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		limit, timeout := int64(1<<20), 30*time.Second
		if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/attachments/") && strings.HasSuffix(r.URL.Path, "/content") {
			limit, timeout = MaxAttachmentBytes+1, 120*time.Second
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/attachments/") && strings.HasSuffix(r.URL.Path, "/preview") {
			timeout = 120 * time.Second // a 25 MiB PDF read through from the bucket
		}
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/github/webhooks/") {
			limit = maxWebhookBody
		}
		if strings.HasPrefix(r.URL.Path, "/api/projects/") {
			switch {
			case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/import"):
				limit, timeout = MaxImportBytes, 120*time.Second
			case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/export"):
				timeout = 60 * time.Second
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (a *App) cookie(raw string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: "kb_session", Value: raw, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
}

// mfaCookie carries the pending second-factor challenge, only to /api/login/mfa.
func (a *App) mfaCookie(raw string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: "kb_mfa", Value: raw, Path: "/api/login/mfa", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
}
func (a *App) origin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != a.Config.PublicURL {
		a.fail(w, problem("FORBIDDEN", "Untrusted request origin."))
		return false
	}
	return true
}
func (a *App) clientIP(r *http.Request) string {
	if a.Config.TrustProxy {
		if ip := net.ParseIP(r.Header.Get("X-Real-IP")); ip != nil {
			return ip.String()
		}
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}
func decode(r *http.Request, out any) error {
	if media := strings.Split(r.Header.Get("Content-Type"), ";")[0]; media != "application/json" {
		return problem("INVALID_INPUT", "Use application/json.")
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return problem("INVALID_INPUT", "Invalid JSON body.")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return problem("INVALID_INPUT", "Expected one JSON object.")
	}
	return nil
}
func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := decode(r, out); err != nil {
		http.Error(w, PublicError(err), 400)
		return false
	}
	return true
}
func (a *App) sessionRoute(mux *http.ServeMux, pattern string, fn func(*http.Request, Identity) (any, error)) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
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
		if r.Method != "GET" {
			if !a.origin(w, r) {
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(id.CSRF)) != 1 {
				a.fail(w, problem("FORBIDDEN", "Invalid CSRF token."))
				return
			}
		}
		if err := a.Store.RateLimit(r.Context(), "web:"+id.User.ID, 240, 60); err != nil {
			a.fail(w, err)
			return
		}
		out, err := fn(r, id)
		if err != nil {
			a.fail(w, err)
			return
		}
		if r.URL.Path == "/api/logout" || r.URL.Path == "/api/password" {
			http.SetCookie(w, a.cookie("", -1))
		}
		a.json(w, out)
	})
}
func (a *App) mcp(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != a.Config.PublicURL {
		a.fail(w, problem("FORBIDDEN", "Untrusted request origin."))
		return
	}
	if err := a.Store.RateLimit(r.Context(), "mcp-ip:"+a.clientIP(r), 600, 60); err != nil {
		a.fail(w, err)
		return
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		raw = ""
	}
	id, err := a.Store.APIKey(r.Context(), raw)
	if err != nil {
		if logerr := a.Store.Record(r.Context(), Event{Action: "mcp_auth", Outcome: "denied", ErrorCode: errorCode(err), RequestID: uuid.NewString()}); logerr != nil {
			a.fail(w, logerr)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="knowledge-base"`)
		a.fail(w, err)
		return
	}
	if err := a.Store.RateLimit(r.Context(), "mcp:"+id.User.ID, 240, 60); err != nil {
		a.fail(w, err)
		return
	}
	// A fresh, stateless server binds every request to the currently verified key owner.
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return a.Tools.MCP(id) }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	h.ServeHTTP(w, r)
}
func (a *App) json(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func (a *App) fail(w http.ResponseWriter, err error) {
	code := errorCode(err)
	status := http.StatusInternalServerError
	switch code {
	case "UNAUTHORIZED", "MFA_INVALID_CODE", "MFA_REQUIRED":
		status = 401
	case "FORBIDDEN":
		status = 403
	case "NOT_FOUND":
		status = 404
	case "INVALID_INPUT":
		status = 400
	case "CONFLICT", "REVISION_CONFLICT":
		status = 409
	case "RATE_LIMITED":
		status = 429
		w.Header().Set("Retry-After", "900")
	case "STORAGE_UNAVAILABLE", "SCAN_UNAVAILABLE", "MFA_UNAVAILABLE", "WEBHOOK_UNAVAILABLE", "DISCORD_UNAVAILABLE":
		status = 503
	case "QUOTA_EXCEEDED":
		status = 413
	case "BLOCKED_FILE", "MALWARE_DETECTED":
		status = 422
	}
	if status == 500 {
		slog.Error("request failed", "code", code)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": PublicError(err)}})
}

// githubWebhook receives a GitHub webhook delivery. It is public: the
// delivery is trusted only after its HMAC signature matches the secret of the
// webhook named in the path.
func (a *App) githubWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if validID(id) != nil {
		a.fail(w, problem("NOT_FOUND", "Webhook not found."))
		return
	}
	if err := a.Store.RateLimit(r.Context(), "github-webhook:"+id, 600, 60); err != nil {
		a.fail(w, err)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "INVALID_INPUT", "message": "The payload is too large."}})
		return
	}
	out, err := a.Store.ReceiveGitHubWebhook(r.Context(), WebhookRequest{WebhookID: id, Event: r.Header.Get("X-GitHub-Event"), DeliveryID: r.Header.Get("X-GitHub-Delivery"),
		Signature: r.Header.Get("X-Hub-Signature-256"), ContentType: r.Header.Get("Content-Type"), Body: body})
	if err != nil {
		a.fail(w, err)
		return
	}
	a.json(w, out)
}
