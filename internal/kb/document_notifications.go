package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Document notifications (문서 생성·승격 알림).
//
// A new document, or a draft promoted to active, is announced on project
// channels that opted in to document_created / document_promoted and listed
// the document's kind. Messages carry the title, kind, status and the name of
// whoever made the change — never the body.

// notifyDocumentBestEffort queues document notifications inside the caller's
// transaction but behind a savepoint: the outbox rows commit or roll back with
// the document change, yet a failure to queue only drops the notification and
// never fails or aborts the document save.
func notifyDocumentBestEffort(ctx context.Context, tx *sql.Tx, actor Identity, d Document, event string) {
	if _, err := tx.ExecContext(ctx, "SAVEPOINT document_notify"); err != nil {
		slog.Error("document notification skipped", "event", event)
		return
	}
	if err := notifyDocument(ctx, tx, actor, d, event); err != nil {
		slog.Error("document notification failed", "event", event)
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT document_notify"); err != nil {
			slog.Error("document notification rollback failed")
			return
		}
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT document_notify"); err != nil {
		slog.Error("document notification release failed")
	}
}

// notifyDocument queues event for each enabled channel that subscribes to it
// and to the document's kind. Recipients are the project's active members
// other than the actor; with nobody else to tell, nothing is queued. Email
// channels get one row per recipient, webhook and Slack channels one row.
func notifyDocument(ctx context.Context, tx *sql.Tx, actor Identity, d Document, event string) error {
	type channel struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	channels, err := many[channel](ctx, tx, `SELECT jsonb_build_object('id',c.id,'kind',c.kind) FROM notification_channels c
		WHERE c.project_id=$1 AND c.enabled AND $2=ANY(c.events) AND $3=ANY(c.document_kinds) ORDER BY c.id`, d.ProjectID, event, d.Kind)
	if err != nil || len(channels) == 0 {
		return err
	}
	recipients, err := many[notificationRecipient](ctx, tx, `SELECT jsonb_build_object('id',u.id,'name',u.name) FROM users u
		JOIN project_members m ON m.user_id=u.id AND m.project_id=$1 WHERE NOT u.disabled AND u.id<>$2 ORDER BY u.name,u.id`, d.ProjectID, actor.User.ID)
	if err != nil || len(recipients) == 0 {
		return err
	}
	payload := notificationPayload{Event: event, ProjectID: d.ProjectID, DocTitle: d.Title, DocKind: d.Kind, DocStatus: d.Status,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := tx.QueryRowContext(ctx, `SELECT p.name,coalesce((SELECT u.name FROM users u WHERE u.id=$2),'') FROM projects p WHERE p.id=$1`,
		d.ProjectID, actor.User.ID).Scan(&payload.ProjectName, &payload.ActorName); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	for _, c := range channels {
		targets := []any{nil}
		if c.Kind == "email" {
			targets = targets[:0]
			for _, r := range recipients {
				targets = append(targets, r.ID)
			}
		}
		for _, recipient := range targets {
			if _, err := tx.ExecContext(ctx, `INSERT INTO notification_outbox(id,project_id,channel_id,event,recipient_id,payload) VALUES($1,$2,$3,$4,$5,$6)`,
				uuid.NewString(), d.ProjectID, c.ID, event, recipient, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

// notificationRoute is the web console page a notification links to.
func notificationRoute(p notificationPayload) string {
	switch p.Event {
	case eventDocumentCreated, eventDocumentPromoted:
		if p.DocStatus == "draft" {
			return "/#reviews"
		}
		return "/#documents"
	}
	return "/#issues"
}

func documentSubject(p notificationPayload) string {
	switch p.Event {
	case eventDocumentCreated:
		return fmt.Sprintf("새 문서(%s): %s", p.DocStatus, p.DocTitle)
	case eventDocumentPromoted:
		return "문서 승격(draft → active): " + p.DocTitle
	}
	return p.DocTitle
}

func writeDocumentText(b *strings.Builder, p notificationPayload) {
	fmt.Fprintf(b, "종류: %s · 상태: %s\n작성자: %s\n", p.DocKind, p.DocStatus, p.ActorName)
}
