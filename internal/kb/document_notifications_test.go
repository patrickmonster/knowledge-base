package kb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDocumentNotificationDefaults(t *testing.T) {
	events, err := normalizeEvents(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e == eventDocumentCreated || e == eventDocumentPromoted {
			t.Fatalf("document events must be opt-in, default events: %v", events)
		}
	}
	events, err = normalizeEvents([]string{eventDocumentCreated, eventDocumentCreated, eventDocumentPromoted})
	if err != nil || len(events) != 2 {
		t.Fatalf("document events rejected or not deduplicated: %v %v", events, err)
	}
	if _, err := normalizeEvents([]string{"document_deleted"}); err == nil {
		t.Fatal("unknown event accepted")
	}
	kinds, err := normalizeDocKinds(nil)
	if err != nil || strings.Join(kinds, ",") != "brief,decision,reference,glossary" {
		t.Fatalf("default kinds %v %v", kinds, err)
	}
	if _, err := normalizeDocKinds([]string{"memo"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func documentPayload() notificationPayload {
	return notificationPayload{Event: eventDocumentPromoted, ProjectID: "p", ProjectName: "결제", DocTitle: strings.Repeat("긴 제목 ", 600),
		DocKind: "decision", DocStatus: "active", ActorName: "검토자", URL: "https://kb.example.com/#documents"}
}

func TestDocumentNotificationText(t *testing.T) {
	p := documentPayload()
	p.DocTitle = "배포 결정"
	text := notificationText(p)
	for _, want := range []string{"[결제]", "문서 승격", "배포 결정", "종류: decision", "상태: active", "작성자: 검토자", "https://kb.example.com/#documents"} {
		if !strings.Contains(text, want) {
			t.Errorf("document notification missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "받는 사람") || strings.Contains(text, "issue://") {
		t.Errorf("document notification has issue fields:\n%s", text)
	}
	created := p
	created.Event, created.DocStatus = eventDocumentCreated, "draft"
	if !strings.Contains(notificationSubject(created), "새 문서(draft)") || notificationRoute(created) != "/#reviews" || notificationRoute(p) != "/#documents" {
		t.Errorf("subject or route wrong: %q %q %q", notificationSubject(created), notificationRoute(created), notificationRoute(p))
	}
	if notificationRoute(notificationPayload{Event: eventIssueAssigned}) != "/#issues" {
		t.Error("issue events must keep linking to the issues page")
	}
}

func TestDocumentNotificationDiscordLimit(t *testing.T) {
	var body struct {
		Content string         `json:"content"`
		Allowed map[string]any `json:"allowed_mentions"`
	}
	if err := json.Unmarshal(discordBody(documentPayload()), &body); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(body.Content)); n > discordContentLimit || n < discordContentLimit-1 {
		t.Fatalf("discord content has %d characters", n)
	}
	if body.Allowed == nil {
		t.Fatal("mentions are not disabled")
	}
}

type outboxRow struct {
	Event, Kind, Status string
	Recipient           string
	Payload             string
}

func outboxRows(t *testing.T, s *Store, projectID string) []outboxRow {
	t.Helper()
	rows, err := s.DB.Query(`SELECT o.event,c.kind,o.status,coalesce(o.recipient_id::text,''),o.payload::text FROM notification_outbox o
		JOIN notification_channels c ON c.id=o.channel_id WHERE o.project_id=$1 ORDER BY o.created_at,o.id`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.Event, &r.Kind, &r.Status, &r.Recipient, &r.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestDocumentNotifications(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "doc-editor@example.com")
	gone := inviteAs(t, s, admin, "doc-gone@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "docnotify", Name: "문서알림"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: gone.User.ID, Role: "editor"})
	if _, err := s.DB.Exec(`UPDATE users SET disabled=true WHERE id=$1`, gone.User.ID); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	received := []map[string]any{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
	}))
	defer srv.Close()
	s.Notifier = &Notifier{HTTP: srv.Client()}
	s.PublicURL = "https://kb.example.com"

	// A channel created the old way (default events) never gets document events.
	legacy := mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "legacy", Target: srv.URL + "/legacy"}).(NotificationChannel)
	if strings.Join(legacy.DocKinds, ",") != "brief,decision,reference,glossary" {
		t.Fatalf("default document kinds: %v", legacy.DocKinds)
	}
	_, err := call(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "bad", Target: srv.URL, DocKinds: []string{"memo"}})
	denied(t, err, "INVALID_INPUT")
	hook := mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "docs", Target: srv.URL + "/docs",
		Events: []string{eventDocumentCreated, eventDocumentPromoted}}).(NotificationChannel)
	mail := mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "email", Name: "mail",
		Events: []string{eventDocumentPromoted}, DocKinds: []string{"decision", "history"}}).(NotificationChannel)
	// Updating without document_kinds keeps them; updating events keeps kinds.
	mail = mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, ChannelID: mail.ID, Name: "mail2"}).(NotificationChannel)
	if strings.Join(mail.DocKinds, ",") != "decision,history" || len(mail.Events) != 1 {
		t.Fatalf("update lost settings: %+v", mail)
	}

	create := func(who Identity, title, kind, status string) Document {
		return mustCall(t, tools, who, "create_document", DocumentInput{ProjectID: p.ID, Title: title, Kind: kind, Status: status,
			Content: "본문 비밀 내용 SECRET-BODY"}).(Document)
	}
	count := func() int { return len(outboxRows(t, s, p.ID)) }

	// Creation: decision (draft) by admin -> docs webhook only (mail listens to promotion).
	draft := create(admin, "배포 방식 결정", "decision", "draft")
	if rows := outboxRows(t, s, p.ID); len(rows) != 1 || rows[0].Event != eventDocumentCreated || rows[0].Kind != "webhook" || rows[0].Recipient != "" {
		t.Fatalf("created draft rows: %+v", rows)
	}
	// History is filtered out by default kinds.
	create(admin, "작업 이력", "history", "active")
	if count() != 1 {
		t.Fatalf("history was announced: %d rows", count())
	}
	// Editor creates an active reference: announced; the actor's own change is not sent to them (only admin remains).
	create(editor, "참고 자료", "reference", "active")
	if count() != 2 {
		t.Fatalf("active reference not announced: %d rows", count())
	}

	// Content-only edit is not a promotion.
	mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: draft.ID, ExpectedRevision: draft.Revision, Reason: "수정",
		Patch: DocumentPatch{Content: docPtr("다른 본문")}})
	if count() != 2 {
		t.Fatalf("content edit announced: %d rows", count())
	}
	// Approval promotes: webhook + one email row per active recipient (editor only; admin is the actor, disabled user skipped).
	review := mustCall(t, tools, admin, "review_document", ReviewInput{ProjectID: p.ID, DocumentID: draft.ID, ExpectedRevision: draft.Revision + 1, Decision: "approve", Reason: "확인"})
	if review.(Document).Status != "active" {
		t.Fatalf("review result: %+v", review)
	}
	rows := outboxRows(t, s, p.ID)
	promoted := 0
	for _, r := range rows {
		if r.Event != eventDocumentPromoted {
			continue
		}
		promoted++
		if r.Kind == "email" && r.Recipient != editor.User.ID {
			t.Fatalf("email promotion sent to %q, want only the editor", r.Recipient)
		}
		if strings.Contains(r.Payload, "SECRET-BODY") || strings.Contains(r.Payload, draft.ID) || strings.Contains(r.Payload, "다른 본문") {
			t.Fatalf("payload leaked body or document id: %s", r.Payload)
		}
	}
	if promoted != 2 {
		t.Fatalf("promotion rows = %d, want 2 (webhook + one email): %+v", promoted, rows)
	}
	// Promotion through update_document, and reject/archive is not announced.
	d2 := create(editor, "용어 기획", "decision", "draft")
	before := count()
	mustCall(t, tools, editor, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: d2.ID, ExpectedRevision: d2.Revision, Reason: "승격",
		Patch: DocumentPatch{Status: docPtr("active")}})
	if count() != before+2 { // webhook + email to admin
		t.Fatalf("update_document promotion rows: %d -> %d", before, count())
	}
	d3 := create(editor, "반려될 문서", "decision", "draft")
	before = count()
	mustCall(t, tools, admin, "review_document", ReviewInput{ProjectID: p.ID, DocumentID: d3.ID, ExpectedRevision: d3.Revision, Decision: "reject", Reason: "반려"})
	if count() != before {
		t.Fatalf("rejection announced")
	}

	// Delivery: the legacy channel got nothing; the docs webhook carries no body.
	for {
		n, err := s.ProcessNotificationBatch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("docs webhook received nothing")
	}
	for _, body := range received {
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "SECRET-BODY") || body["document_title"] == nil || !strings.HasPrefix(body["url"].(string), "https://kb.example.com/#") {
			t.Fatalf("webhook payload: %s", raw)
		}
	}
	var legacyRows int
	if err := s.DB.QueryRow(`SELECT count(*) FROM notification_outbox WHERE channel_id=$1`, legacy.ID).Scan(&legacyRows); err != nil || legacyRows != 0 {
		t.Fatalf("channel without document events got %d rows (%v)", legacyRows, err)
	}
	_ = hook
}

func TestDocumentNotificationSkippedWhenAlone(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "docalone", Name: "혼자"}).(Project)
	mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "docs", Target: "https://example.com/hook",
		Events: []string{eventDocumentCreated}})
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "혼자 쓴 문서", Kind: "decision", Content: "x"})
	if rows := outboxRows(t, s, p.ID); len(rows) != 0 {
		t.Fatalf("author notified about their own change: %+v", rows)
	}
}

// A failure while queueing a notification must not fail or abort the save.
func TestDocumentNotificationFailureDoesNotBlockSave(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "doc-fail@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "docfail", Name: "실패"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "docs", Target: "https://example.com/hook",
		Events: []string{eventDocumentCreated, eventDocumentPromoted}})
	if _, err := s.DB.Exec(`CREATE FUNCTION kb_fail_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbox down'; END $$;
		CREATE TRIGGER kb_fail_outbox BEFORE INSERT ON notification_outbox FOR EACH ROW EXECUTE FUNCTION kb_fail_outbox()`); err != nil {
		t.Fatal(err)
	}
	d := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "저장은 성공", Kind: "decision", Status: "draft", Content: "x"}).(Document)
	var chunks int
	if err := s.DB.QueryRow(`SELECT count(*) FROM chunks WHERE document_id=$1`, d.ID).Scan(&chunks); err != nil || chunks == 0 {
		t.Fatalf("document not fully saved: chunks=%d err=%v", chunks, err)
	}
	mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: d.Revision, Reason: "승격", Patch: DocumentPatch{Status: docPtr("active")}})
	var status string
	var revision int
	if err := s.DB.QueryRow(`SELECT status,revision FROM documents WHERE id=$1`, d.ID).Scan(&status, &revision); err != nil || status != "active" || revision != 2 {
		t.Fatalf("promotion not saved: %s %d %v", status, revision, err)
	}
	if rows := outboxRows(t, s, p.ID); len(rows) != 0 {
		t.Fatalf("rows queued despite failure: %+v", rows)
	}
}

func docPtr[T any](v T) *T { return &v }
