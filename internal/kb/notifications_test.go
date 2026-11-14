package kb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"testing"
)

func TestMaskTarget(t *testing.T) {
	got := maskTarget("https://hooks.slack.com/services/T000/B000/secretXYZ1")
	if got != "https://hooks.slack.com/…XYZ1" || strings.Contains(got, "secret") {
		t.Fatalf("maskTarget leaked or changed format: %q", got)
	}
	if maskTarget("") != "" || maskTarget("::bad") != "***" {
		t.Fatal("unexpected mask for empty or invalid target")
	}
}

func TestValidateChannelTarget(t *testing.T) {
	for _, c := range []struct {
		kind, target string
		ok           bool
	}{
		{"slack", "https://hooks.slack.com/services/a/b/c", true},
		{"slack", "https://example.com/hook", false},
		{"webhook", "https://example.com/hook", true},
		{"webhook", "http://example.com/hook", false},
		{"webhook", "https://user:pass@example.com/hook", false},
		{"webhook", "", false},
		{"email", "", true},
		{"email", "someone@example.com", false},
	} {
		if err := validateChannelTarget(c.kind, c.target); (err == nil) != c.ok {
			t.Errorf("validateChannelTarget(%q, %q) = %v, want ok=%v", c.kind, c.target, err, c.ok)
		}
	}
}

func TestPublicAddress(t *testing.T) {
	for _, c := range []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true}, {"2606:4700:4700::1111", true},
		{"127.0.0.1", false}, {"10.1.2.3", false}, {"172.16.0.1", false}, {"192.168.1.1", false},
		{"169.254.169.254", false}, {"100.64.0.1", false}, {"0.0.0.0", false}, {"::1", false}, {"fd00::1", false}, {"fe80::1", false},
	} {
		if got := publicAddress(net.ParseIP(c.ip)); got != c.public {
			t.Errorf("publicAddress(%s) = %v, want %v", c.ip, got, c.public)
		}
	}
}

func TestSafeHTTPClientBlocksPrivateAddresses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("request reached a loopback server") }))
	defer srv.Close()
	resp, err := safeHTTPClient().Post(srv.URL, "application/json", strings.NewReader("{}"))
	if err == nil {
		resp.Body.Close()
		t.Fatal("loopback request was allowed")
	}
	if !errors.Is(err, errBlockedAddress) {
		t.Fatalf("want errBlockedAddress, got %v", err)
	}
}

func testPayload() notificationPayload {
	return notificationPayload{Event: eventIssueAssigned, ProjectID: "p", ProjectName: "결제", IssueID: "i", IssueTitle: "세션 만료?", IssueKind: "question",
		IssueStatus: "open", Blocking: true, AreaName: "인증", ActorName: "관리자", Recipients: []notificationRecipient{{ID: "u", Name: "담당자"}}, URL: "https://kb.example.com/#issues"}
}

func TestNotificationText(t *testing.T) {
	text := notificationText(testPayload())
	for _, want := range []string{"[결제] 질의 담당 배정: 세션 만료?", "받는 사람: 담당자", "작업자: 관리자", "작업 보류", "영역 인증", "https://kb.example.com/#issues", "issue://p/i"} {
		if !strings.Contains(text, want) {
			t.Errorf("notification text missing %q:\n%s", want, text)
		}
	}
}

func TestNotifierPost(t *testing.T) {
	var mu sync.Mutex
	status := http.StatusOK
	var got map[string]any
	var headers http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &got)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	n := &Notifier{HTTP: srv.Client()}
	job := outboxJob{ID: "delivery-1", Event: eventIssueAssigned, Kind: "webhook", Target: srv.URL + "/hook", Payload: testPayload()}
	if err := n.deliver(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if got["issue_title"] != "세션 만료?" || headers.Get("X-KB-Event") != eventIssueAssigned || headers.Get("X-KB-Delivery") != "delivery-1" {
		t.Fatalf("unexpected webhook request: %v %v", got, headers)
	}
	for code, permanent := range map[int]bool{500: false, 429: false, 404: true, 302: true} {
		mu.Lock()
		status = code
		mu.Unlock()
		err := n.deliver(context.Background(), job)
		var de *deliveryError
		if !errors.As(err, &de) || de.Permanent != permanent {
			t.Fatalf("status %d: got %v, want permanent=%v", code, err, permanent)
		}
	}
	// Slack bodies are {"text": ...}; the target must be hooks.slack.com.
	job.Kind = "slack"
	err := n.deliver(context.Background(), job)
	var de *deliveryError
	if !errors.As(err, &de) || de.Code != "INVALID_TARGET" {
		t.Fatalf("slack delivery to a non-Slack host: %v", err)
	}
}

func TestNotifierEmail(t *testing.T) {
	job := outboxJob{ID: "mail-1", Event: eventIssueAnswered, Kind: "email", RecipientEmail: "owner@example.com", Payload: testPayload()}
	// The message text comes from the stored payload's event, not the job column.
	job.Payload.Event = eventIssueAnswered
	err := (&Notifier{}).deliver(context.Background(), job)
	var de *deliveryError
	if !errors.As(err, &de) || de.Code != "SMTP_NOT_CONFIGURED" || !de.Permanent {
		t.Fatalf("missing SMTP should fail permanently: %v", err)
	}
	var sent []byte
	var to []string
	n := &Notifier{SMTP: &SMTPConfig{Host: "smtp.example.com", Port: 587, From: "kb@example.com"}, sendMail: func(addr string, _ smtp.Auth, from string, rcpt []string, msg []byte) error {
		if addr != "smtp.example.com:587" || from != "kb@example.com" {
			t.Errorf("unexpected envelope %s %s", addr, from)
		}
		to, sent = rcpt, msg
		return nil
	}}
	job.Payload.IssueTitle = "줄바꿈\r\nBcc: attacker@example.com"
	if err := n.deliver(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(to) != 1 || to[0] != "owner@example.com" {
		t.Fatalf("unexpected recipients %v", to)
	}
	head, body, _ := strings.Cut(string(sent), "\r\n\r\n")
	if strings.Contains(head, "\r\nBcc:") || !strings.Contains(head, "Subject: =?utf-8?b?") {
		t.Fatalf("unsafe or unencoded headers:\n%s", head)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
	if err != nil || !strings.Contains(string(decoded), "답변 등록") {
		t.Fatalf("body not decodable: %v %q", err, decoded)
	}
	job.RecipientEmail = ""
	if err := n.deliver(context.Background(), job); !errors.As(err, &de) || de.Code != "NO_RECIPIENT_EMAIL" {
		t.Fatalf("missing recipient email: %v", err)
	}
}

func TestIssueNotifications(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "notify-owner@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "notify", Name: "알림"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: owner.User.ID, Role: "editor"})

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

	// Editors and MCP keys cannot configure channels.
	_, err := call(t, tools, owner, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "hook", Target: srv.URL})
	denied(t, err, "FORBIDDEN")
	keyOut, err := s.CreateKey(ctx, admin, KeyInput{Name: "rw", Access: "read_write", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "hook", Target: srv.URL})
	denied(t, err, "FORBIDDEN")

	hook := mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "hook", Target: srv.URL + "/secret-path"}).(NotificationChannel)
	if hook.Target != "" || strings.Contains(hook.TargetHint, "secret") || len(hook.Events) != len(notificationEvents) {
		t.Fatalf("channel leaked target or missed default events: %+v", hook)
	}
	mail := mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "email", Name: "mail", Events: []string{eventIssueAnswered}}).(NotificationChannel)
	// Updating without a target keeps the stored URL.
	off := false
	mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, ChannelID: hook.ID, Name: "renamed", Enabled: &off})
	on := true
	mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, ChannelID: hook.ID, Enabled: &on})

	issue := mustCall(t, tools, admin, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "배포 순서?", Body: "본문은 알림에 포함되지 않아야 함", AssigneeID: owner.User.ID}).(Issue)
	mustCall(t, tools, owner, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: issue.ID, Answer: "답변 원문"})
	// The actor is never notified about their own change: admin comments go to the assignee only.
	mustCall(t, tools, admin, "comment_issue", CommentIssueInput{ProjectID: p.ID, IssueID: issue.ID, Body: "확인"})
	mustCall(t, tools, admin, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: issue.ID, Status: "resolved"})

	var queued int
	if err := s.DB.QueryRow(`SELECT count(*) FROM notification_outbox WHERE project_id=$1`, p.ID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	// webhook: assigned, answered, commented, status = 4; email: answered = 1.
	if queued != 5 {
		t.Fatalf("queued %d notifications, want 5", queued)
	}
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
	if len(received) != 4 {
		t.Fatalf("webhook received %d events, want 4", len(received))
	}
	for _, body := range received {
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "본문은") || strings.Contains(string(raw), "답변 원문") || body["url"] != "https://kb.example.com/#issues" {
			t.Fatalf("payload leaked issue text or missed url: %s", raw)
		}
	}
	if received[0]["event"] != eventIssueAssigned || received[1]["event"] != eventIssueAnswered {
		t.Fatalf("unexpected event order: %v", received)
	}
	var mailStatus, mailError string
	if err := s.DB.QueryRow(`SELECT status,last_error FROM notification_outbox WHERE channel_id=$1`, mail.ID).Scan(&mailStatus, &mailError); err != nil {
		t.Fatal(err)
	}
	if mailStatus != "failed" || mailError != "SMTP_NOT_CONFIGURED" {
		t.Fatalf("email without SMTP: %s %s", mailStatus, mailError)
	}
	channels := mustCall(t, tools, admin, "list_notification_channels", ProjectInput{ProjectID: p.ID}).([]NotificationChannel)
	if len(channels) != 2 || channels[0].LastStatus == nil || *channels[0].LastStatus != "sent" || channels[0].Name != "renamed" {
		t.Fatalf("unexpected channel list: %+v", channels)
	}
	mustCall(t, tools, admin, "test_notification_channel", ChannelRef{ProjectID: p.ID, ChannelID: hook.ID})
	mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, ChannelID: hook.ID, Remove: true})
	if err := s.DB.QueryRow(`SELECT count(*) FROM notification_outbox WHERE channel_id=$1`, hook.ID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("removing a channel kept its outbox rows: %d %v", queued, err)
	}

	// Members removed from the project stop receiving notifications: a row
	// queued while they were a member is not mailed, and no new rows are queued.
	comments := mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "email", Name: "comments", Events: []string{eventIssueCommented}}).(NotificationChannel)
	mustCall(t, tools, admin, "comment_issue", CommentIssueInput{ProjectID: p.ID, IssueID: issue.ID, Body: "멤버일 때 댓글"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: owner.User.ID, Role: "remove"})
	s.Notifier = &Notifier{SMTP: &SMTPConfig{Host: "smtp.example.com", Port: 587, From: "kb@example.com"},
		sendMail: func(string, smtp.Auth, string, []string, []byte) error {
			t.Error("mail was sent to a user removed from the project")
			return nil
		}}
	for {
		n, err := s.ProcessNotificationBatch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	var status, code string
	if err := s.DB.QueryRow(`SELECT status,last_error FROM notification_outbox WHERE channel_id=$1`, comments.ID).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != "NO_RECIPIENT_EMAIL" {
		t.Fatalf("queued mail to a removed member: %s %s", status, code)
	}
	mustCall(t, tools, admin, "comment_issue", CommentIssueInput{ProjectID: p.ID, IssueID: issue.ID, Body: "제외 후 댓글"})
	if err := s.DB.QueryRow(`SELECT count(*) FROM notification_outbox WHERE channel_id=$1`, comments.ID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("notification queued for a removed member: %d %v", queued, err)
	}
}
