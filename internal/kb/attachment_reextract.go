package kb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Re-extracting attachment text (첨부 재추출): files uploaded before text
// extraction existed, while it was switched off, or whose extraction failed
// are read back from the bucket and extracted again with the current parser.
// Like an upload, parsing runs outside any database transaction.

const (
	reextractDefaultBatch = 5
	reextractMaxBatch     = 20
)

type ReextractInput struct {
	ProjectID    string `json:"project_id"`
	AttachmentID string `json:"attachment_id,omitempty" jsonschema:"One file to extract again. Omit to retry up to limit files whose text is missing or failed"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Without attachment_id: how many files to retry, 1–20, default 5"`
}

// ReextractItem is the outcome for one file. Result is extracted (text saved),
// unchanged (extraction failed or came out the same, the existing text stays),
// failed, empty, none, or error (the file could not be processed at all).
type ReextractItem struct {
	AttachmentID   string `json:"attachment_id"`
	Filename       string `json:"filename"`
	Result         string `json:"result"`
	TextStatus     string `json:"text_status,omitempty"`
	TextError      string `json:"text_error,omitempty"`
	TextDocumentID string `json:"text_document_id,omitempty"`
	Message        string `json:"message,omitempty"`
}

type ReextractResult struct {
	Items []ReextractItem `json:"items"`
	// More is true when a batch run stopped at limit and files are still waiting.
	More     bool   `json:"more"`
	Guidance string `json:"guidance"`
}

const reextractGuidance = "Text of .pdf, .docx and .xlsx files only. A failed retry keeps any text already indexed. Scanned PDFs stay empty (no OCR). Run again with the same input to continue a batch; files retried most recently go last."

// ReextractAttachments extracts the text of one attachment again, or of the
// next files that have no usable text.
func (s *Store) ReextractAttachments(ctx context.Context, a Identity, in ReextractInput) (any, error) {
	ctx = auditInput(ctx, in)
	if in.AttachmentID != "" {
		item, err := s.reextractAttachment(ctx, a, AttachmentInput{ProjectID: in.ProjectID, AttachmentID: in.AttachmentID})
		if err != nil {
			return nil, err
		}
		return ReextractResult{Items: []ReextractItem{item}, Guidance: reextractGuidance}, nil
	}
	if in.Limit == 0 {
		in.Limit = reextractDefaultBatch
	}
	if in.Limit < 1 || in.Limit > reextractMaxBatch {
		return nil, problem("INVALID_INPUT", fmt.Sprintf("limit must be 1–%d.", reextractMaxBatch))
	}
	out, err := s.Perform(ctx, a, "reextract_attachment_list", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true); err != nil {
			return nil, err
		}
		if err := s.requireBucket(); err != nil {
			return nil, err
		}
		if s.Extractor == nil {
			return nil, problem("CONFLICT", "Text extraction is turned off on this server (ATTACHMENT_TEXT_EXTRACTION=false).")
		}
		// One more than limit tells the caller whether files remain.
		return many[string](ctx, tx, `SELECT to_jsonb(a.id::text) FROM attachments a WHERE a.project_id=$1 AND a.status='ready' AND a.issue_id IS NULL
			AND a.text_status IN ('failed','none') AND lower(a.filename) ~ '\.(pdf|docx|xlsx)$'
			ORDER BY a.updated_at,a.id LIMIT $2`, in.ProjectID, in.Limit+1)
	})
	if err != nil {
		return nil, err
	}
	ids := out.([]string)
	res := ReextractResult{Items: []ReextractItem{}, More: len(ids) > in.Limit, Guidance: reextractGuidance}
	if res.More {
		ids = ids[:in.Limit]
	}
	for _, id := range ids {
		item, err := s.reextractAttachment(ctx, a, AttachmentInput{ProjectID: in.ProjectID, AttachmentID: id})
		if err != nil {
			item = ReextractItem{AttachmentID: id, Result: "error", Message: PublicError(err)}
		}
		res.Items = append(res.Items, item)
	}
	return res, nil
}

func (s *Store) reextractAttachment(ctx context.Context, a Identity, ref AttachmentInput) (ReextractItem, error) {
	out, err := s.Perform(ctx, a, "reextract_attachment", ref.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		f, err := readAttachment(ctx, tx, a, ref, true)
		if err != nil {
			return nil, err
		}
		if err = s.requireBucket(); err != nil {
			return nil, err
		}
		if s.Extractor == nil {
			return nil, problem("CONFLICT", "Text extraction is turned off on this server (ATTACHMENT_TEXT_EXTRACTION=false).")
		}
		if f.IssueID != nil || ExtractFormat(f.Filename) == "" {
			return nil, problem("INVALID_INPUT", "Only .pdf, .docx and .xlsx files are extracted.")
		}
		if f.Status != "ready" {
			return nil, problem("CONFLICT", "File is not ready. Check list_attachments.")
		}
		return f, nil
	})
	if err != nil {
		return ReextractItem{}, err
	}
	f := out.(Attachment)
	item := ReextractItem{AttachmentID: f.ID, Filename: f.Filename}

	file, err := s.downloadAttachment(ctx, f)
	if err != nil {
		return item, err
	}
	defer os.Remove(file)
	text := s.extractAttachmentText(ctx, f, file)

	res, err := s.Perform(ctx, a, "reextract_attachment_complete", ref.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		cur, err := readAttachment(ctx, tx, a, ref, true)
		if err != nil {
			return nil, err
		}
		if cur.Status != "ready" || !sameString(cur.TextDocumentID, f.TextDocumentID) {
			return nil, problem("CONFLICT", "File changed while extracting. Try again.")
		}
		done, err := completeReextract(ctx, tx, a, cur, text)
		if err != nil {
			return nil, err
		}
		// Text indexed before summaries were linked gets its link on any retry.
		return done, linkAttachmentText(ctx, tx, a, cur, done.TextDocumentID)
	})
	if err != nil {
		return item, err
	}
	return res.(ReextractItem), nil
}

// downloadAttachment copies the stored object to a temporary file and checks it
// against the recorded SHA-256, so a replaced or damaged object is never indexed.
func (s *Store) downloadAttachment(ctx context.Context, f Attachment) (string, error) {
	rc, err := s.Objects.Get(ctx, attachmentKey(f.ProjectID, f.ID))
	if err != nil {
		return "", problem("STORAGE_UNAVAILABLE", "Could not read the file from the bucket.")
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "kb-reextract-*")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(rc, f.SizeBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", problem("STORAGE_UNAVAILABLE", "Could not read the file from the bucket.")
	}
	if n != f.SizeBytes || (f.SHA256 != nil && fmt.Sprintf("%x", hash.Sum(nil)) != *f.SHA256) {
		os.Remove(tmp.Name())
		return "", problem("CONFLICT", "The stored file does not match its recorded size and SHA-256; not extracting it.")
	}
	return tmp.Name(), nil
}

func sameString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// completeReextract stores the new extraction. A failed or empty retry never
// replaces text that is already indexed.
func completeReextract(ctx context.Context, tx *sql.Tx, a Identity, cur Attachment, text attachmentText) (ReextractItem, error) {
	item := ReextractItem{AttachmentID: cur.ID, Filename: cur.Filename, TextStatus: text.status, TextError: text.code}
	sha := ""
	if cur.SHA256 != nil {
		sha = *cur.SHA256
	}
	if len(sha) < 16 {
		return item, problem("CONFLICT", "File has no recorded SHA-256.")
	}
	touch := func(status, code, docID string, truncated bool) error {
		_, err := tx.ExecContext(ctx, `UPDATE attachments SET text_status=$2,text_error=NULLIF($3,''),text_document_id=NULLIF($4,'')::uuid,text_truncated=$5,updated_at=now() WHERE id=$1`, cur.ID, status, code, docID, truncated)
		return err
	}
	existing := ""
	if cur.TextDocumentID != nil {
		existing = *cur.TextDocumentID
	}
	if text.status != "extracted" {
		if existing != "" {
			// Keep the indexed text; only mark the attempt so batch runs move on.
			item.Result, item.TextStatus, item.TextError, item.TextDocumentID = "unchanged", cur.TextStatus, "", existing
			item.Message = fmt.Sprintf("Extraction gave %s%s; the existing text was kept.", text.status, codeSuffix(text.code))
			_, err := tx.ExecContext(ctx, "UPDATE attachments SET updated_at=now() WHERE id=$1", cur.ID)
			return item, err
		}
		item.Result = text.status
		return item, touch(text.status, text.code, "", false)
	}
	body := attachmentTextBody(cur, sha, text)
	if existing == "" {
		id, err := saveAttachmentText(ctx, tx, a, cur, sha, text)
		if err != nil {
			return item, err
		}
		item.Result, item.TextDocumentID = "extracted", id
		return item, touch("extracted", "", id, text.res.Truncated)
	}
	var revision int
	var status, content string
	if err := tx.QueryRowContext(ctx, "SELECT revision,status,content FROM documents WHERE id=$1 AND project_id=$2 FOR UPDATE", existing, cur.ProjectID).Scan(&revision, &status, &content); err != nil {
		return item, notFound(err)
	}
	item.TextDocumentID = existing
	if content != body || status != "active" {
		patch := DocumentPatch{Content: &body}
		if status != "active" {
			active := "active"
			patch.Status = &active
		}
		if _, err := updateDocument(ctx, tx, a, UpdateInput{ProjectID: cur.ProjectID, DocumentID: existing, ExpectedRevision: revision, Reason: "첨부 본문 재추출 (" + time.Now().UTC().Format("2006-01-02") + ")", Patch: patch}); err != nil {
			return item, err
		}
		item.Result = "extracted"
	} else {
		item.Result, item.Message = "unchanged", "Extraction gave the same text."
	}
	return item, touch("extracted", "", existing, text.res.Truncated)
}

func codeSuffix(code string) string {
	if code == "" {
		return ""
	}
	return " (" + strings.ToLower(code) + ")"
}

func registerReextractTool(t Tools, s *Store) {
	addCall(t, "reextract_attachment", "Extract the text of attached .pdf, .docx or .xlsx files again with the current parser: pass attachment_id for one file (for example to fix text extracted by an older version), or omit it to retry up to limit files (default 5, max 20) whose text is missing (text_status none: uploaded before extraction existed or while it was off) or failed. The file is read back from the bucket, checked against its SHA-256 and extracted outside the database; the `첨부 본문` document is created or updated (new revision) and search reindexes it. A failed or empty retry keeps the text already indexed. Needs edit access. Scanned PDFs stay empty (no OCR).", true, s.ReextractAttachments)
}
