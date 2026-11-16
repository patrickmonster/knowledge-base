package kb

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// attachmentText is the outcome of extracting one upload, decided before the
// completion transaction so parsing never holds database locks.
type attachmentText struct {
	status string // none, extracted, empty, failed
	code   string // text_error code when failed
	format string
	res    ExtractResult
}

func (s *Store) extractAttachmentText(ctx context.Context, f Attachment, file string) attachmentText {
	format := ExtractFormat(f.Filename)
	if s.Extractor == nil || format == "" {
		return attachmentText{status: "none"}
	}
	res, err := s.Extractor.Extract(ctx, format, file)
	if err != nil {
		code := ExtractCode(err)
		slog.Warn("attachment text extraction failed", "format", format, "code", code)
		return attachmentText{status: "failed", code: code, format: format}
	}
	if strings.TrimSpace(res.Text) == "" {
		return attachmentText{status: "empty", format: format, res: res}
	}
	return attachmentText{status: "extracted", format: format, res: res}
}

// attachmentTextBody is the artifact document body: a provenance header and the text.
func attachmentTextBody(f Attachment, sha string, text attachmentText) string {
	units := ""
	switch {
	case text.format == "pdf" && text.res.Units > 0:
		units = fmt.Sprintf(", %d쪽", text.res.Units)
	case text.format == "xlsx" && text.res.Units > 0:
		units = fmt.Sprintf(", 시트 %d개", text.res.Units)
	}
	var head strings.Builder
	fmt.Fprintf(&head, "> 첨부 파일 **%s**(%s%s)에서 자동 추출한 본문입니다. 첨부 ID `%s`, SHA-256 `%s`.\n", strings.NewReplacer("*", "\\*", "\n", " ").Replace(f.Filename), strings.ToUpper(text.format), units, f.ID, sha[:16])
	head.WriteString("> 서식·이미지·차트는 빠지고 표는 단순화됩니다. 날짜·수식은 저장된 값 그대로이므로 정확한 수치는 원본으로 확인하세요.\n")
	if text.res.Truncated {
		fmt.Fprintf(&head, "> 본문이 길어 앞부분 %d자만 담았습니다.\n", MaxExtractedRunes)
	}
	head.WriteString("\n")
	return head.String() + text.res.Text
}

// saveAttachmentText stores extracted text as an artifact document owned by the
// uploader and returns its id, or "" when there is nothing to store.
func saveAttachmentText(ctx context.Context, tx *sql.Tx, a Identity, f Attachment, sha string, text attachmentText) (string, error) {
	if text.status != "extracted" {
		return "", nil
	}
	out, err := createDocument(ctx, tx, a, DocumentInput{
		ProjectID: f.ProjectID,
		Title:     "첨부 본문: " + truncateRunes(f.Filename, 190),
		Kind:      "artifact",
		Content:   attachmentTextBody(f, sha, text),
		Tags:      []string{"첨부본문", text.format},
		Status:    "active",
	})
	if err != nil {
		return "", err
	}
	return out.(Document).ID, nil
}

// archiveAttachmentText archives the extracted text document of a file being
// deleted so search stops returning content of a removed file. A new revision
// keeps the history.
func archiveAttachmentText(ctx context.Context, tx *sql.Tx, a Identity, f Attachment) error {
	if f.TextDocumentID == nil {
		return nil
	}
	var revision int
	var status string
	err := tx.QueryRowContext(ctx, "SELECT revision,status FROM documents WHERE id=$1 AND project_id=$2 FOR UPDATE", *f.TextDocumentID, f.ProjectID).Scan(&revision, &status)
	if err == sql.ErrNoRows || (err == nil && status == "archived") {
		return nil
	}
	if err != nil {
		return err
	}
	archived := "archived"
	_, err = updateDocument(ctx, tx, a, UpdateInput{ProjectID: f.ProjectID, DocumentID: *f.TextDocumentID, ExpectedRevision: revision, Reason: "첨부 파일 삭제로 추출 본문 보관 처리", Patch: DocumentPatch{Status: &archived}})
	return err
}
