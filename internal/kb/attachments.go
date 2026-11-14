package kb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const MaxAttachmentBytes int64 = 25 << 20

type Attachment struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	DocumentID       *string   `json:"document_id"`
	DocumentRevision *int      `json:"document_revision"`
	Filename         string    `json:"filename"`
	SizeBytes        int64     `json:"size_bytes"`
	SHA256           *string   `json:"sha256"`
	Status           string    `json:"status"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type PrepareAttachmentInput struct {
	ProjectID  string `json:"project_id"`
	DocumentID string `json:"document_id,omitempty"`
	Filename   string `json:"filename"`
	SizeBytes  int64  `json:"size_bytes" jsonschema:"File size in bytes, from 1 to 26214400"`
}
type AttachmentInput struct {
	ProjectID    string `json:"project_id"`
	AttachmentID string `json:"attachment_id"`
}
type ListAttachmentsInput struct {
	ProjectID  string `json:"project_id"`
	DocumentID string `json:"document_id,omitempty"`
	PageInput
}

func (s *Store) requireBucket() error {
	if s.Objects == nil {
		return problem("STORAGE_UNAVAILABLE", "Railway bucket is not configured.")
	}
	return nil
}
func attachmentKey(projectID, id string) string { return "attachments/" + projectID + "/" + id }
func registerAttachmentTools(t Tools, s *Store) {
	add(t, s, "prepare_attachment_upload", "Reserve a private file (max 25 MiB) for a file the user attached or that you produced (for example an .xlsx summary). Then PUT the raw bytes with Content-Type application/octet-stream to upload_url (one-time, 1 hour, no Authorization header; e.g. curl --fail-with-body -T <file> -H 'Content-Type: application/octet-stream' '<upload_url>'), or to upload_path with the same Bearer key. Do not send file bytes through MCP. Confirm status ready with the PUT response or list_attachments. File text is not indexed: also save a knowledge document summarizing the file and pass its document_id.", true, func(in PrepareAttachmentInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in PrepareAttachmentInput) (any, error) {
		if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true); err != nil {
			return nil, err
		}
		if err := s.requireBucket(); err != nil {
			return nil, err
		}
		if err := bounded("filename", in.Filename, 1, 200); err != nil {
			return nil, err
		}
		if in.Filename != strings.TrimSpace(in.Filename) || in.Filename == "." || in.Filename == ".." || strings.ContainsAny(in.Filename, "/\\") || strings.IndexFunc(in.Filename, unicode.IsControl) >= 0 || in.SizeBytes < 1 || in.SizeBytes > MaxAttachmentBytes {
			return nil, problem("INVALID_INPUT", "Use a plain filename and a file between 1 byte and 25 MiB.")
		}
		var revision *int
		if in.DocumentID != "" {
			if err := validID(in.DocumentID); err != nil {
				return nil, err
			}
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT revision FROM documents WHERE id=$1 AND project_id=$2", in.DocumentID, in.ProjectID).Scan(&n); err != nil {
				return nil, notFound(err)
			}
			revision = &n
		}
		id := uuid.NewString()
		// MCP callers get a one-time upload link so clients that cannot read their own
		// Bearer key (hosted connectors, sandboxes) can still PUT the file. Web sessions
		// keep using cookie + CSRF and never receive a link.
		var uploadToken, tokenHash, keyID any
		if a.KeyID != "" {
			raw := "kbu_" + token()
			uploadToken, tokenHash, keyID = raw, digest(raw), a.KeyID
		}
		f, err := one[Attachment](ctx, tx, `WITH a AS (INSERT INTO attachments(id,project_id,document_id,document_revision,filename,size_bytes,object_key,created_by,upload_token_hash,upload_key_id) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10::uuid) RETURNING *) SELECT to_jsonb(a)-'object_key'-'upload_token_hash'-'upload_key_id' FROM a`, id, in.ProjectID, in.DocumentID, revision, in.Filename, in.SizeBytes, attachmentKey(in.ProjectID, id), a.User.ID, tokenHash, keyID)
		path := "/api/attachments/" + id + "/content?project_id=" + in.ProjectID
		out := map[string]any{"id": id, "attachment": f, "upload_path": path, "method": "PUT", "content_type": "application/octet-stream", "expires_in_seconds": 3600}
		if uploadToken != nil {
			out["upload_url"] = s.PublicURL + path + "&upload_token=" + uploadToken.(string)
			out["upload_url_note"] = "One-time link for this file only, valid for 1 hour, no Authorization header needed. Do not save it in knowledge documents."
		}
		return out, err
	})
	add(t, s, "list_attachments", "List project files and upload status. Optional document_id filters files linked to that document.", false, func(in ListAttachmentsInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ListAttachmentsInput) (any, error) {
		if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false); err != nil {
			return nil, err
		}
		limit, err := pagination(in.Limit, in.Offset)
		if err != nil {
			return nil, err
		}
		if in.DocumentID != "" {
			if err := validID(in.DocumentID); err != nil {
				return nil, err
			}
		}
		return many[Attachment](ctx, tx, `SELECT to_jsonb(a)-'object_key' FROM attachments a WHERE project_id=$1 AND status<>'deleting' AND ($2='' OR document_id=NULLIF($2,'')::uuid) ORDER BY created_at DESC,id LIMIT $3 OFFSET $4`, in.ProjectID, in.DocumentID, limit, in.Offset)
	})
	add(t, s, "get_attachment_download", "Issue a private download link valid for 5 minutes. Anyone with the link can use it until expiry; do not save it in knowledge documents.", false, func(in AttachmentInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in AttachmentInput) (any, error) {
		f, err := readAttachment(ctx, tx, a, in, false)
		if err != nil {
			return nil, err
		}
		if err = s.requireBucket(); err != nil {
			return nil, err
		}
		if f.Status != "ready" {
			return nil, problem("CONFLICT", "File is not ready for download.")
		}
		u, err := s.Objects.DownloadURL(ctx, attachmentKey(in.ProjectID, in.AttachmentID), f.Filename)
		if err != nil {
			return nil, problem("STORAGE_UNAVAILABLE", "Could not issue a download link.")
		}
		return map[string]any{"attachment": f, "url": u, "expires_in_seconds": 300}, nil
	})
	add(t, s, "delete_attachment", "Remove access to a file and queue its bucket object for deletion. Existing download links expire within 5 minutes.", true, func(in AttachmentInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in AttachmentInput) (any, error) {
		f, err := readAttachment(ctx, tx, a, in, true)
		if err != nil {
			return nil, err
		}
		if err = s.requireBucket(); err != nil {
			return nil, err
		}
		if f.Status == "uploading" {
			return nil, problem("CONFLICT", "Upload is in progress. Try again later.")
		}
		_, err = tx.ExecContext(ctx, "UPDATE attachments SET status='deleting',updated_at=now() WHERE id=$1", f.ID)
		return map[string]any{"id": f.ID, "status": "deleting"}, err
	})
}
func readAttachment(ctx context.Context, tx *sql.Tx, a Identity, in AttachmentInput, write bool) (Attachment, error) {
	if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, write); err != nil {
		return Attachment{}, err
	}
	if err := validID(in.AttachmentID); err != nil {
		return Attachment{}, err
	}
	lock := " FOR SHARE"
	if write {
		lock = " FOR UPDATE"
	}
	f, err := one[Attachment](ctx, tx, `SELECT to_jsonb(a)-'object_key' FROM attachments a WHERE id=$1 AND project_id=$2 AND status<>'deleting'`+lock, in.AttachmentID, in.ProjectID)
	return f, notFound(err)
}

func (s *Store) UploadAttachment(ctx context.Context, a Identity, in AttachmentInput, body io.Reader) (result any, retErr error) {
	ctx = auditInput(ctx, in)
	out, err := s.Perform(ctx, a, "upload_attachment_begin", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		f, err := readAttachment(ctx, tx, a, in, true)
		if err != nil {
			return nil, err
		}
		if err = s.requireBucket(); err != nil {
			return nil, err
		}
		if f.CreatedBy != a.User.ID {
			return nil, problem("FORBIDDEN", "Only the uploader can complete this upload.")
		}
		if f.Status != "pending" || time.Since(f.CreatedAt) > time.Hour {
			return nil, problem("CONFLICT", "Upload is used or expired. Prepare a new upload.")
		}
		_, err = tx.ExecContext(ctx, "UPDATE attachments SET status='uploading',upload_token_hash=NULL,updated_at=now() WHERE id=$1", f.ID)
		return f, err
	})
	if err != nil {
		return nil, err
	}
	f := out.(Attachment)
	defer func() {
		if retErr == nil {
			return
		}
		logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := s.Record(logCtx, Event{ActorID: a.User.ID, ProjectID: in.ProjectID, TargetID: f.ID, Action: "upload_attachment_failed", Outcome: "error", ErrorCode: errorCode(retErr), RequestID: uuid.NewString()}); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	// Keep the durable row on any failure, including a crash or lost PUT response.
	// A later sweep removes failed uploads; ready files are never swept.
	file, err := os.CreateTemp("", "kb-attachment-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, f.SizeBytes+1))
	if err != nil || n != f.SizeBytes {
		return nil, problem("INVALID_INPUT", "Upload length does not match the reserved file size.")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err = s.Objects.Put(ctx, attachmentKey(in.ProjectID, f.ID), f.Filename, file, n); err != nil {
		return nil, problem("STORAGE_UNAVAILABLE", "Upload failed. Prepare a new upload to retry.")
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	return s.Perform(ctx, a, "upload_attachment_complete", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		current, err := readAttachment(ctx, tx, a, in, true)
		if err != nil {
			return nil, err
		}
		if current.Status != "uploading" {
			return nil, problem("CONFLICT", "Upload state changed.")
		}
		return one[Attachment](ctx, tx, `WITH a AS (UPDATE attachments SET status='ready',sha256=$2,updated_at=now() WHERE id=$1 RETURNING *) SELECT to_jsonb(a)-'object_key' FROM a`, f.ID, digest)
	})
}

// UploadTokenIdentity resolves a one-time upload link to the API key that reserved
// the file. The key must still be active, so revoking it also disables its links.
// Permissions and the uploader check are then enforced again by UploadAttachment.
func (s *Store) UploadTokenIdentity(ctx context.Context, attachmentID, projectID, raw string) (Identity, error) {
	invalid := problem("UNAUTHORIZED", "Invalid, used, or expired upload link. Prepare a new upload.")
	if !strings.HasPrefix(raw, "kbu_") || len(raw) != 47 || validID(attachmentID) != nil || validID(projectID) != nil {
		return Identity{}, invalid
	}
	var userJSON []byte
	var id, access string
	var project sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT to_jsonb(u)-'password_hash',k.id::text,k.access,k.project_id::text
		FROM attachments f JOIN api_keys k ON k.id=f.upload_key_id JOIN users u ON u.id=k.user_id
		WHERE f.id=$1 AND f.project_id=$2 AND f.upload_token_hash=$3 AND f.status='pending' AND f.created_at>now()-interval '1 hour'
		AND k.user_id=f.created_by AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND NOT u.disabled`,
		attachmentID, projectID, digest(raw)).Scan(&userJSON, &id, &access, &project)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, invalid
	}
	if err != nil {
		return Identity{}, err
	}
	u, err := decodeUser(userJSON)
	return Identity{User: u, KeyID: id, Access: access, ProjectID: project.String}, err
}

// No bucket lifecycle rule is needed. The database journals all object keys before PUT.
func (s *Store) CleanupAttachments(ctx context.Context) error {
	if s.Objects == nil {
		return nil
	}
	return transaction(ctx, s.DB, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id::text,object_key FROM attachments WHERE status='deleting' OR (status IN ('pending','uploading') AND updated_at<now()-interval '1 hour') ORDER BY updated_at LIMIT 20 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		type item struct{ id, key string }
		items := []item{}
		for rows.Next() {
			var f item
			if err = rows.Scan(&f.id, &f.key); err != nil {
				rows.Close()
				return err
			}
			items = append(items, f)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, f := range items {
			if err = s.Objects.Delete(ctx, f.key); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM attachments WHERE id=$1", f.id); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) RunAttachmentCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := s.CleanupAttachments(c)
			cancel()
			if err != nil {
				slog.Error("attachment cleanup failed")
			}
		}
	}
}
