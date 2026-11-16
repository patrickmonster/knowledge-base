package kb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const MaxAttachmentBytes int64 = 25 << 20

// MaxAttachmentChunkBytes keeps one base64 chunk (about 683 KiB) and its JSON-RPC
// envelope under the 1 MiB request body limit.
const MaxAttachmentChunkBytes = 512 << 10

// MaxIssueImageBytes caps one image attached to a question or issue.
const MaxIssueImageBytes int64 = 10 << 20

// issueImageTypes are the raster formats accepted for issue images, keyed by the
// type http.DetectContentType reports. SVG is excluded because it can carry script.
var issueImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

func issueImageFilename(name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}
	switch strings.ToLower(name[i+1:]) {
	case "png", "jpg", "jpeg", "gif", "webp":
		return true
	}
	return false
}

// sniffImage reports the raster image type of the first bytes of r, or "".
func sniffImage(r io.Reader) (string, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	t := http.DetectContentType(head[:n])
	if !issueImageTypes[t] {
		return "", nil
	}
	return t, nil
}

type Attachment struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"project_id"`
	DocumentID       *string `json:"document_id"`
	DocumentRevision *int    `json:"document_revision"`
	IssueID          *string `json:"issue_id"`
	// IssuePart is body, answer or comment for issue images; IssueCommentID names the comment.
	IssuePart      *string `json:"issue_part,omitempty"`
	IssueCommentID *string `json:"issue_comment_id,omitempty"`
	Filename       string  `json:"filename"`
	SizeBytes      int64   `json:"size_bytes"`
	SHA256         *string `json:"sha256"`
	Status         string  `json:"status"`
	// TextStatus is none, extracted, empty or failed; extracted PDF/DOCX/XLSX
	// text lives in the artifact document TextDocumentID.
	TextStatus     string    `json:"text_status"`
	TextError      *string   `json:"text_error,omitempty"`
	TextDocumentID *string   `json:"text_document_id,omitempty"`
	TextTruncated  bool      `json:"text_truncated,omitempty"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type PrepareAttachmentInput struct {
	ProjectID      string `json:"project_id"`
	DocumentID     string `json:"document_id,omitempty"`
	IssueID        string `json:"issue_id,omitempty" jsonschema:"Attach an image (png, jpg, gif or webp, at most 10 MiB) to this question or issue instead of a document"`
	IssuePart      string `json:"issue_part,omitempty" jsonschema:"With issue_id: body (default), answer (the current answer; only its author) or comment (with issue_comment_id; only the comment's author)"`
	IssueCommentID string `json:"issue_comment_id,omitempty" jsonschema:"With issue_part comment: the comment to attach the image to"`
	Filename       string `json:"filename"`
	SizeBytes      int64  `json:"size_bytes" jsonschema:"File size in bytes, from 1 to 26214400"`
}
type AttachmentInput struct {
	ProjectID    string `json:"project_id"`
	AttachmentID string `json:"attachment_id"`
}
type UploadAttachmentChunkInput struct {
	ProjectID    string `json:"project_id"`
	AttachmentID string `json:"attachment_id"`
	Offset       int64  `json:"offset" jsonschema:"Byte offset of this chunk: 0 for the first chunk, then next_offset from the previous response"`
	DataBase64   string `json:"data_base64" jsonschema:"Standard base64 of this chunk's raw bytes, at most 524288 bytes decoded"`
}
type ListAttachmentsInput struct {
	ProjectID  string `json:"project_id"`
	DocumentID string `json:"document_id,omitempty"`
	IssueID    string `json:"issue_id,omitempty"`
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
	add(t, s, "prepare_attachment_upload", "Reserve a private file (max 25 MiB) for a file the user attached or that you produced (for example an .xlsx summary). Then PUT the raw bytes with Content-Type application/octet-stream to upload_url (one-time, 1 hour, no Authorization header; e.g. curl --fail-with-body -T <file> -H 'Content-Type: application/octet-stream' '<upload_url>'), or to upload_path with the same Bearer key. Prefer the PUT. Only if your network cannot reach upload_url, send the bytes with upload_attachment_chunk instead (base64, costly for large files). Confirm status ready with the PUT response or list_attachments. Text of .pdf, .docx and .xlsx files is extracted on upload into an artifact document (text_status, text_document_id) that search indexes; scanned PDFs need OCR and stay empty. Other files are not indexed. Still save a knowledge document summarizing the file's purpose and key points and pass its document_id; that summary and the extracted text document are linked as related automatically. To attach a screenshot or photo to a question or issue, pass issue_id instead (png, jpg, gif or webp, at most 10 MiB; the content must be that image type); issue_part answer or comment (with issue_comment_id) attaches it to your answer or comment.", true, func(in PrepareAttachmentInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in PrepareAttachmentInput) (any, error) {
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
		if !s.AllowExecutables && blockedFilename(in.Filename) {
			return nil, problem("BLOCKED_FILE", "Executable and script files cannot be attached. Compress or rename only if the file is safe documentation.")
		}
		if in.IssueID != "" {
			if in.DocumentID != "" {
				return nil, problem("INVALID_INPUT", "Link a file to either a document or an issue, not both.")
			}
			if err := belongsToProject(ctx, tx, "issues", in.IssueID, in.ProjectID); err != nil {
				return nil, err
			}
			if !issueImageFilename(in.Filename) || in.SizeBytes > MaxIssueImageBytes {
				return nil, problem("INVALID_INPUT", "Issue attachments must be png, jpg, gif or webp images of at most 10 MiB.")
			}
			if in.IssuePart == "" {
				in.IssuePart = "body"
			}
			if err := issueImageTarget(ctx, tx, a, in); err != nil {
				return nil, err
			}
		} else if in.IssuePart != "" || in.IssueCommentID != "" {
			return nil, problem("INVALID_INPUT", "issue_part and issue_comment_id need issue_id.")
		}
		if err := s.reserveQuota(ctx, tx, in.ProjectID, in.SizeBytes); err != nil {
			return nil, err
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
		f, err := one[Attachment](ctx, tx, `WITH a AS (INSERT INTO attachments(id,project_id,document_id,document_revision,filename,size_bytes,object_key,created_by,upload_token_hash,upload_key_id,issue_id,issue_part,issue_comment_id) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10::uuid,NULLIF($11,'')::uuid,NULLIF($12,''),NULLIF($13,'')::uuid) RETURNING *) SELECT to_jsonb(a)-'object_key'-'upload_token_hash'-'upload_key_id' FROM a`, id, in.ProjectID, in.DocumentID, revision, in.Filename, in.SizeBytes, attachmentKey(in.ProjectID, id), a.User.ID, tokenHash, keyID, in.IssueID, in.IssuePart, in.IssueCommentID)
		path := "/api/attachments/" + id + "/content?project_id=" + in.ProjectID
		out := map[string]any{"id": id, "attachment": f, "upload_path": path, "method": "PUT", "content_type": "application/octet-stream", "expires_in_seconds": 3600, "max_chunk_bytes": MaxAttachmentChunkBytes}
		if uploadToken != nil {
			out["upload_url"] = s.PublicURL + path + "&upload_token=" + uploadToken.(string)
			out["upload_url_note"] = "One-time link for this file only, valid for 1 hour, no Authorization header needed. Do not save it in knowledge documents."
		}
		return out, err
	})
	addCall(t, "upload_attachment_chunk", "Upload a reserved file through MCP when your network cannot reach upload_url. Send the raw bytes in order as standard base64 chunks of at most 524288 bytes (decoded): offset 0 first, then next_offset from each response. The reservation's 1-hour limit still applies. When the last chunk arrives the file is scanned and stored; the response then has complete: true and the attachment with status ready and sha256. Retrying a chunk with identical data is safe. Prefer the upload_url PUT whenever it works: base64 in tool calls is slow and costly for large files.", true, s.UploadAttachmentChunk)
	add(t, s, "get_attachment_usage", "Show the project's stored and reserved file bytes, its quota, and whether uploads are malware-scanned.", false, func(in ProjectInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ProjectInput) (any, error) {
		if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false); err != nil {
			return nil, err
		}
		return s.attachmentUsage(ctx, tx, in.ProjectID)
	})
	add(t, s, "list_attachments", "List project files and upload status. Optional document_id or issue_id filters files linked to that document or issue.", false, func(in ListAttachmentsInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ListAttachmentsInput) (any, error) {
		if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false); err != nil {
			return nil, err
		}
		limit, err := pagination(in.Limit, in.Offset)
		if err != nil {
			return nil, err
		}
		for _, id := range []string{in.DocumentID, in.IssueID} {
			if id != "" {
				if err := validID(id); err != nil {
					return nil, err
				}
			}
		}
		return many[Attachment](ctx, tx, `SELECT to_jsonb(a)-'object_key'-'upload_token_hash'-'upload_key_id' FROM attachments a WHERE project_id=$1 AND status<>'deleting' AND ($2='' OR document_id=NULLIF($2,'')::uuid) AND ($5='' OR issue_id=NULLIF($5,'')::uuid) ORDER BY created_at DESC,id LIMIT $3 OFFSET $4`, in.ProjectID, in.DocumentID, limit, in.Offset, in.IssueID)
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
	add(t, s, "delete_attachment", "Remove access to a file and queue its bucket object for deletion. Its extracted text document, if any, is archived. Existing download links expire within 5 minutes.", true, func(in AttachmentInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in AttachmentInput) (any, error) {
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
		if err = archiveAttachmentText(ctx, tx, a, f); err != nil {
			return nil, err
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
	if f.IssueID != nil {
		// Issue images are shown inline, so the bytes must match an allowed raster type.
		kind, err := sniffImage(file)
		if err != nil {
			return nil, err
		}
		if kind == "" {
			return nil, problem("INVALID_INPUT", "Issue attachments must be png, jpg, gif or webp images.")
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
	}
	if err = s.screenUpload(ctx, a, in, f, file); err != nil {
		return nil, err
	}
	if err = s.Objects.Put(ctx, attachmentKey(in.ProjectID, f.ID), f.Filename, file, n); err != nil {
		return nil, problem("STORAGE_UNAVAILABLE", "Upload failed. Prepare a new upload to retry.")
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	// The object is stored; text extraction can only add an index, never fail the upload.
	text := s.extractAttachmentText(ctx, f, file.Name())
	return s.Perform(ctx, a, "upload_attachment_complete", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		current, err := readAttachment(ctx, tx, a, in, true)
		if err != nil {
			return nil, err
		}
		if current.Status != "uploading" {
			return nil, problem("CONFLICT", "Upload state changed.")
		}
		// Chunks from upload_attachment_chunk are no longer needed once the object is stored.
		if _, err := tx.ExecContext(ctx, "DELETE FROM attachment_chunks WHERE attachment_id=$1", f.ID); err != nil {
			return nil, err
		}
		textDocumentID, err := saveAttachmentText(ctx, tx, a, f, digest, text)
		if err != nil {
			return nil, err
		}
		if err := linkAttachmentText(ctx, tx, a, f, textDocumentID); err != nil {
			return nil, err
		}
		return one[Attachment](ctx, tx, `WITH a AS (UPDATE attachments SET status='ready',sha256=$2,text_status=$3,text_error=NULLIF($4,''),text_document_id=NULLIF($5,'')::uuid,text_truncated=$6,updated_at=now() WHERE id=$1 RETURNING *) SELECT to_jsonb(a)-'object_key' FROM a`, f.ID, digest, text.status, text.code, textDocumentID, text.res.Truncated)
	})
}

type chunkProgress struct {
	AttachmentID  string `json:"attachment_id"`
	ReceivedBytes int64  `json:"received_bytes"`
	SizeBytes     int64  `json:"size_bytes"`
	NextOffset    int64  `json:"next_offset"`
	Complete      bool   `json:"complete"`
	// Set only when the file is stored (status ready).
	Attachment *Attachment `json:"attachment,omitempty"`
}

// UploadAttachmentChunk appends one base64 chunk to a pending reservation over MCP,
// for clients whose network cannot reach upload_url. Chunks are kept in the
// database until the last one arrives; the assembled file then goes through
// UploadAttachment, so size, scanning, bucket storage and SHA-256 match the PUT path.
func (s *Store) UploadAttachmentChunk(ctx context.Context, a Identity, in UploadAttachmentChunkInput) (any, error) {
	ctx = auditInput(ctx, in)
	ref := AttachmentInput{ProjectID: in.ProjectID, AttachmentID: in.AttachmentID}
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(in.DataBase64), ""))
	if err != nil || len(data) < 1 || len(data) > MaxAttachmentChunkBytes || in.Offset < 0 {
		return nil, problem("INVALID_INPUT", fmt.Sprintf("Send standard base64 of 1 to %d bytes and a non-negative offset.", MaxAttachmentChunkBytes))
	}
	out, err := s.Perform(ctx, a, "upload_attachment_chunk", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		f, err := readAttachment(ctx, tx, a, ref, true)
		if err != nil {
			return nil, err
		}
		if err = s.requireBucket(); err != nil {
			return nil, err
		}
		if f.CreatedBy != a.User.ID {
			return nil, problem("FORBIDDEN", "Only the uploader can complete this upload.")
		}
		end := in.Offset + int64(len(data))
		if f.Status == "ready" && end == f.SizeBytes {
			// Retried last chunk after a lost response. Compare sha256 to confirm the content.
			return chunkProgress{AttachmentID: f.ID, ReceivedBytes: f.SizeBytes, SizeBytes: f.SizeBytes, NextOffset: f.SizeBytes, Complete: true, Attachment: &f}, nil
		}
		if f.Status != "pending" || time.Since(f.CreatedAt) > time.Hour {
			return nil, problem("CONFLICT", "Upload is finishing, used or expired. Check list_attachments; prepare a new upload if it is not ready.")
		}
		var received int64
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(length(data)),0) FROM attachment_chunks WHERE attachment_id=$1", f.ID).Scan(&received); err != nil {
			return nil, err
		}
		progress := func(n int64) chunkProgress {
			return chunkProgress{AttachmentID: f.ID, ReceivedBytes: n, SizeBytes: f.SizeBytes, NextOffset: n, Complete: n == f.SizeBytes}
		}
		if in.Offset < received {
			var same bool
			err = tx.QueryRowContext(ctx, "SELECT data=$3 FROM attachment_chunks WHERE attachment_id=$1 AND position=$2", f.ID, in.Offset, data).Scan(&same)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !same) {
				return nil, problem("CONFLICT", fmt.Sprintf("Different data was already received at this offset. Continue from offset %d or prepare a new upload.", received))
			}
			if err != nil {
				return nil, err
			}
			return progress(received), nil
		}
		if in.Offset > received {
			return nil, problem("INVALID_INPUT", fmt.Sprintf("Chunks must be sent in order. Next offset is %d.", received))
		}
		if end > f.SizeBytes {
			return nil, problem("INVALID_INPUT", "Chunk exceeds the reserved file size.")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO attachment_chunks(attachment_id,position,data) VALUES($1,$2,$3)", f.ID, in.Offset, data); err != nil {
			return nil, err
		}
		// Bytes now arrive through MCP; retire the one-time PUT link for this file.
		if _, err = tx.ExecContext(ctx, "UPDATE attachments SET upload_token_hash=NULL,updated_at=now() WHERE id=$1", f.ID); err != nil {
			return nil, err
		}
		return progress(end), nil
	})
	if err != nil {
		return nil, err
	}
	p := out.(chunkProgress)
	if !p.Complete || p.Attachment != nil {
		return p, nil
	}
	stored, err := s.UploadAttachment(ctx, a, ref, &chunkReader{ctx: ctx, db: s.DB, id: p.AttachmentID})
	if err != nil {
		return nil, err
	}
	f := stored.(Attachment)
	p.Attachment = &f
	return p, nil
}

// chunkReader streams stored chunks in order without loading the whole file.
type chunkReader struct {
	ctx context.Context
	db  *sql.DB
	id  string
	pos int64
	buf []byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		err := r.db.QueryRowContext(r.ctx, "SELECT data FROM attachment_chunks WHERE attachment_id=$1 AND position=$2", r.id, r.pos).Scan(&r.buf)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		r.pos += int64(len(r.buf))
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
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
