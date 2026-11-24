package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// DefaultAttachmentQuotaBytes is used when ATTACHMENT_PROJECT_QUOTA_MB is unset.
const DefaultAttachmentQuotaBytes int64 = 1 << 30

// ParseAttachmentQuota reads ATTACHMENT_PROJECT_QUOTA_MB: empty uses 1024 MiB,
// 0 disables the limit, otherwise 25–1048576 MiB.
func ParseAttachmentQuota(raw string) (int64, error) {
	if raw == "" {
		return DefaultAttachmentQuotaBytes, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || (n != 0 && (n < 25 || n > 1<<20)) {
		return 0, errors.New("ATTACHMENT_PROJECT_QUOTA_MB must be 0 (unlimited) or between 25 and 1048576")
	}
	return n << 20, nil
}

// reserveQuota counts ready files and live reservations (pending/uploading)
// against the project quota. Locking the project row serializes concurrent
// reservations so two uploads cannot both squeeze under the limit.
func (s *Store) reserveQuota(ctx context.Context, tx *sql.Tx, projectID string, size int64) error {
	if s.AttachmentQuotaBytes <= 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM projects WHERE id=$1 FOR NO KEY UPDATE`, projectID); err != nil {
		return err
	}
	used, err := attachmentBytes(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if used+size > s.AttachmentQuotaBytes {
		return problem("QUOTA_EXCEEDED", fmt.Sprintf("Project file quota exceeded: %s used of %s. Delete files or ask an operator to raise ATTACHMENT_PROJECT_QUOTA_MB.",
			mib(used), mib(s.AttachmentQuotaBytes)))
	}
	return nil
}

func attachmentBytes(ctx context.Context, q Querier, projectID string) (int64, error) {
	var used int64
	err := q.QueryRowContext(ctx, `SELECT coalesce(sum(size_bytes),0) FROM attachments WHERE project_id=$1
		AND (status='ready' OR (status IN ('pending','uploading') AND updated_at>now()-interval '1 hour'))`, projectID).Scan(&used)
	return used, err
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

func (s *Store) attachmentUsage(ctx context.Context, q Querier, projectID string) (any, error) {
	used, err := attachmentBytes(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	scanner := "none"
	active, _, err := s.effectiveScanner(ctx, q)
	if err != nil {
		return nil, err
	}
	if active != nil {
		scanner = active.Name()
	}
	var quota any
	if s.AttachmentQuotaBytes > 0 {
		quota = s.AttachmentQuotaBytes
	}
	return map[string]any{"project_id": projectID, "used_bytes": used, "quota_bytes": quota,
		"malware_scanner": scanner, "executables_blocked": !s.AllowExecutables}, nil
}

// screenUpload rejects executables and infected files before they reach the
// bucket. Scanner failures fail closed. A rejected reservation is queued for
// deletion at once so it stops counting against the quota.
func (s *Store) screenUpload(ctx context.Context, a Identity, in AttachmentInput, f Attachment, file *os.File) error {
	reject := func(code, message string, details map[string]any) error {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if _, err := s.DB.ExecContext(c, `UPDATE attachments SET status='deleting',updated_at=now() WHERE id=$1 AND status='uploading'`, f.ID); err != nil {
			slog.Error("could not queue rejected attachment")
		}
		if details != nil {
			// Signature names are safe to keep; file content and names are not logged.
			_ = s.Record(c, Event{ActorID: a.User.ID, ProjectID: in.ProjectID, TargetID: f.ID, Action: "attachment_rejected", Outcome: "denied", ErrorCode: code, RequestID: uuid.NewString(), Details: details})
		}
		return problem(code, message)
	}
	if !s.AllowExecutables {
		head := make([]byte, 4096)
		n, err := io.ReadFull(file, head)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return err
		}
		if executableMagic(head[:n]) {
			return reject("BLOCKED_FILE", "Executable files cannot be attached.", map[string]any{"reason": "executable"})
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	scanner, _, err := s.effectiveScanner(ctx, s.DB)
	if err != nil {
		return err
	}
	if scanner == nil {
		return nil
	}
	signature, err := scanner.Scan(ctx, file)
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil && err == nil {
		err = seekErr
	}
	if err != nil {
		slog.Error("malware scan failed", "scanner", scanner.Name())
		return reject("SCAN_UNAVAILABLE", "The malware scanner is unavailable. Prepare a new upload and try again later.", nil)
	}
	if signature != "" {
		return reject("MALWARE_DETECTED", "The file was rejected by the malware scanner.", map[string]any{"reason": "malware", "scanner": scanner.Name(), "signature": signature})
	}
	return nil
}
