package kb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecheckDigestSchedulePeriod(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, seoul)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		name   string
		cfg    RecheckDigestSchedule
		now    time.Time
		period string
		due    bool
	}{
		{"off", RecheckDigestSchedule{}, at("2026-11-16 10:00"), "", false},
		{"daily before hour", RecheckDigestSchedule{Frequency: "daily", Hour: 9}, at("2026-11-16 08:59"), "2026-11-16", false},
		{"daily at hour", RecheckDigestSchedule{Frequency: "daily", Hour: 9}, at("2026-11-16 09:00"), "2026-11-16", true},
		// 2026-11-16 is a Monday; the week starts there.
		{"weekly monday before hour", RecheckDigestSchedule{Frequency: "weekly", Hour: 9}, at("2026-11-16 08:00"), "2026-11-16", false},
		{"weekly monday after hour", RecheckDigestSchedule{Frequency: "weekly", Hour: 9}, at("2026-11-16 09:30"), "2026-11-16", true},
		{"weekly sunday is still that week", RecheckDigestSchedule{Frequency: "weekly", Hour: 9}, at("2026-11-22 23:30"), "2026-11-16", true},
		{"weekly next monday is a new period", RecheckDigestSchedule{Frequency: "weekly", Hour: 9}, at("2026-11-23 09:00"), "2026-11-23", true},
		// 23:30 UTC on Sunday is already Monday 08:30 in Seoul.
		{"seoul date, not utc", RecheckDigestSchedule{Frequency: "daily", Hour: 0}, time.Date(2026, 11, 15, 23, 30, 0, 0, time.UTC), "2026-11-16", true},
	} {
		period, due := c.cfg.periodStart(c.now)
		if period != c.period || due != c.due {
			t.Errorf("%s: got %q %v, want %q %v", c.name, period, due, c.period, c.due)
		}
	}
}

func TestNormalizeEventsRecheckDigest(t *testing.T) {
	// Issue events stay the default; the digest is opt-in.
	def, err := normalizeEvents(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range def {
		if e == eventRecheckDigest {
			t.Fatalf("recheck_digest must not be subscribed by default: %v", def)
		}
	}
	got, err := normalizeEvents([]string{eventRecheckDigest, eventRecheckDigest})
	if err != nil || len(got) != 1 || got[0] != eventRecheckDigest {
		t.Fatalf("normalizeEvents(recheck_digest) = %v, %v", got, err)
	}
	if _, err := normalizeEvents([]string{"recheck"}); err == nil {
		t.Fatal("unknown event was accepted")
	}
}

func TestDigestTitle(t *testing.T) {
	got := digestTitle("  배포\n<!channel> 절차  ")
	if strings.ContainsAny(got, "<>\n") || got != "배포 ‹!channel› 절차" {
		t.Fatalf("digestTitle = %q", got)
	}
	if long := digestTitle(strings.Repeat("가", 200)); len([]rune(long)) != digestTitleRunes || !strings.HasSuffix(long, "…") {
		t.Fatalf("long title not shortened: %d runes", len([]rune(long)))
	}
}

func sampleDigestPayload(groups, items int) notificationPayload {
	body := &recheckDigestBody{}
	for g := 0; g < groups; g++ {
		group := digestGroup{OwnerName: strings.Repeat("담", 10), Total: items + 3}
		for i := 0; i < items; i++ {
			group.Items = append(group.Items, digestItem{Title: strings.Repeat("제목", 40), Kind: "reference", DaysOverdue: 30 + i, Citation: "kb://p/documents/d"})
		}
		body.Groups = append(body.Groups, group)
		body.Total += group.Total
	}
	return notificationPayload{Event: eventRecheckDigest, ProjectID: "p", ProjectName: "결제", ActorName: "knowledge-base",
		CreatedAt: "2026-11-16T01:00:00Z", URL: "https://kb.example.com/#documents", Digest: body}
}

func TestRecheckDigestText(t *testing.T) {
	small := notificationPayload{Event: eventRecheckDigest, ProjectID: "p", ProjectName: "결제", CreatedAt: "2026-11-16T01:00:00Z", URL: "https://kb.example.com/#documents",
		Digest: &recheckDigestBody{Total: 3, Groups: []digestGroup{
			{OwnerName: "민수", Total: 2, Items: []digestItem{{Title: "배포 절차", Kind: "reference", DaysOverdue: 12}, {Title: "운영 기획", Kind: "brief", DaysOverdue: 3}}},
			{Inactive: true, Total: 1, Items: []digestItem{{Title: "옛 문서", Kind: "reference", DaysOverdue: 40}}},
		}}}
	text := notificationText(small)
	for _, want := range []string{"[결제] 재확인 필요 문서 3건 (2026-11-16)", "민수 · 2건", "- 배포 절차 (reference) · 기한 12일 지남", "담당자 확인 필요", "https://kb.example.com/#documents"} {
		if !strings.Contains(text, want) {
			t.Errorf("digest text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "웹 콘솔에서 확인하세요") {
		t.Errorf("nothing was omitted but the text says so:\n%s", text)
	}

	// A large digest stays within Discord's limit, says how many it left out,
	// and still ends with the link.
	big := sampleDigestPayload(15, 5)
	text = notificationText(big)
	if n := len([]rune(text)); n > digestTextBudget {
		t.Fatalf("digest text is %d runes, over the %d budget", n, digestTextBudget)
	}
	if !strings.Contains(text, "건은 웹 콘솔에서 확인하세요") || !strings.HasSuffix(text, "https://kb.example.com/#documents\n") {
		t.Fatalf("truncated digest lost its omitted count or link:\n%s", text)
	}
	var discord struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(discordBody(big), &discord); err != nil || discord.Content != text {
		t.Fatalf("discord message was cut or changed: %v", err)
	}
}

func TestRecheckDigestWebhookAndDiscord(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if r.Header.Get("X-KB-Event") != eventRecheckDigest {
			t.Errorf("event header = %q", r.Header.Get("X-KB-Event"))
		}
	}))
	defer srv.Close()
	n := &Notifier{HTTP: srv.Client()}
	p := sampleDigestPayload(1, 2)
	if err := n.deliver(context.Background(), outboxJob{ID: "d1", Event: eventRecheckDigest, Kind: "webhook", Target: srv.URL + "/hook", Payload: p}); err != nil {
		t.Fatal(err)
	}
	if err := n.deliver(context.Background(), outboxJob{ID: "d2", Event: eventRecheckDigest, Kind: "slack", Target: "https://example.com/not-slack", Payload: p}); err == nil {
		t.Fatal("slack digest to a non-Slack host was accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	digest, _ := bodies[0]["digest"].(map[string]any)
	if bodies[0]["event"] != eventRecheckDigest || digest == nil || digest["total"] != float64(p.Digest.Total) {
		t.Fatalf("generic webhook body: %v", bodies[0])
	}
}

func TestRecheckDigestNotifications(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "digest-editor@example.com")
	leaver := inviteAs(t, s, admin, "digest-leaver@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "digest", Name: "요약"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: leaver.User.ID, Role: "editor"})

	var mu sync.Mutex
	var hooks []map[string]any
	var hookEvents []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		hooks = append(hooks, body)
		hookEvents = append(hookEvents, r.Header.Get("X-KB-Event"))
		mu.Unlock()
	}))
	defer srv.Close()
	type mail struct{ to, body string }
	var mails []mail
	s.Notifier = &Notifier{HTTP: srv.Client(), SMTP: &SMTPConfig{Host: "smtp.example.com", Port: 587, From: "kb@example.com"},
		Digest: RecheckDigestSchedule{Frequency: "daily", Hour: 9},
		sendMail: func(_ string, _ smtp.Auth, _ string, to []string, msg []byte) error {
			mu.Lock()
			mails = append(mails, mail{to[0], string(msg)})
			mu.Unlock()
			return nil
		}}
	s.PublicURL = "https://kb.example.com"
	drain := func() {
		for {
			n, err := s.ProcessNotificationBatch(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return
			}
		}
	}
	outbox := func(event string) int {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM notification_outbox WHERE project_id=$1 AND event=$2`, p.ID, event).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	hook := mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "digest hook", Target: srv.URL + "/hook", Events: []string{eventRecheckDigest}}).(NotificationChannel)
	mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "email", Name: "digest mail", Events: []string{eventRecheckDigest}})
	// A channel with the default (issue) events never receives digests.
	issuesOnly := mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, Kind: "webhook", Name: "issues", Target: srv.URL + "/issues"}).(NotificationChannel)

	// Seoul 10:00 today: past the send hour. Only the period date is taken from it.
	n := time.Now().In(seoul)
	now := time.Date(n.Year(), n.Month(), n.Day(), 10, 0, 0, 0, seoul)

	// Nothing is due yet, so nothing is queued and the period is not used up.
	if queued, err := s.QueueRecheckDigests(ctx, now); err != nil || queued != 0 {
		t.Fatalf("queued without due documents: %d %v", queued, err)
	}
	var runs int
	if err := s.DB.QueryRow(`SELECT count(*) FROM recheck_digest_runs WHERE project_id=$1`, p.ID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("empty digest claimed a period: %d %v", runs, err)
	}

	docs := map[string]Document{}
	for title, owner := range map[string]Identity{"운영 절차": editor, "배포 체크리스트": editor, "관리자 문서": admin, "떠난 사람 문서": leaver} {
		docs[title] = mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: title, Kind: "reference", Content: "본문비밀 " + title}).(Document)
		mustCall(t, tools, admin, "assign_recheck_owner", RecheckOwnerInput{ProjectID: p.ID, DocumentID: docs[title].ID, OwnerID: owner.User.ID, IntervalDays: 30, Note: "확인메모비밀"})
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE documents SET updated_at=now()-interval '60 days' WHERE project_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: leaver.User.ID, Role: "remove"})

	// Before the send hour nothing happens.
	early := time.Date(n.Year(), n.Month(), n.Day(), 8, 0, 0, 0, seoul)
	if queued, err := s.QueueRecheckDigests(ctx, early); err != nil || queued != 0 {
		t.Fatalf("queued before the send hour: %d %v", queued, err)
	}
	queued, err := s.QueueRecheckDigests(ctx, now)
	if err != nil || queued != 1 {
		t.Fatalf("queued %d projects, want 1: %v", queued, err)
	}
	// The same period is never sent twice.
	if again, err := s.QueueRecheckDigests(ctx, now.Add(time.Hour)); err != nil || again != 0 {
		t.Fatalf("same period queued again: %d %v", again, err)
	}
	// webhook: one team row; email: one row each for the two active owners only.
	if got := outbox(eventRecheckDigest); got != 3 {
		t.Fatalf("outbox has %d digest rows, want 3", got)
	}
	var issuesRows int
	if err := s.DB.QueryRow(`SELECT count(*) FROM notification_outbox WHERE channel_id=$1`, issuesOnly.ID).Scan(&issuesRows); err != nil || issuesRows != 0 {
		t.Fatalf("channel without the digest event received %d rows: %v", issuesRows, err)
	}
	drain()

	mu.Lock()
	if len(hooks) != 1 || hookEvents[0] != eventRecheckDigest {
		t.Fatalf("webhook got %d requests %v", len(hooks), hookEvents)
	}
	raw, _ := json.Marshal(hooks[0])
	text := string(raw)
	for _, secret := range []string{"본문비밀", "확인메모비밀"} {
		if strings.Contains(text, secret) {
			t.Fatalf("digest payload leaked %q: %s", secret, text)
		}
	}
	body := hooks[0]["digest"].(map[string]any)
	if body["total"] != float64(4) || hooks[0]["url"] != "https://kb.example.com/#documents" {
		t.Fatalf("webhook digest: %s", text)
	}
	inactive := 0
	for _, g := range body["groups"].([]any) {
		group := g.(map[string]any)
		if group["inactive"] == true {
			inactive++
			if group["owner_name"] != nil || strings.Contains(text, "digest-leaver") {
				t.Fatalf("removed member is named in the digest: %s", text)
			}
		}
	}
	if inactive != 1 {
		t.Fatalf("want one group for the removed member's document: %s", text)
	}
	if len(mails) != 2 {
		t.Fatalf("sent %d digest mails, want 2 (active owners only): %v", len(mails), mails)
	}
	for _, m := range mails {
		decoded := decodeMailBody(t, m.body)
		switch m.to {
		case "digest-editor@example.com":
			if !strings.Contains(decoded, "운영 절차") || !strings.Contains(decoded, "배포 체크리스트") || strings.Contains(decoded, "관리자 문서") {
				t.Fatalf("editor mail has the wrong documents:\n%s", decoded)
			}
		case "admin@example.com":
			if !strings.Contains(decoded, "관리자 문서") || strings.Contains(decoded, "운영 절차") {
				t.Fatalf("admin mail has the wrong documents:\n%s", decoded)
			}
		default:
			t.Fatalf("digest mailed to %s", m.to)
		}
		if strings.Contains(decoded, "본문비밀") || strings.Contains(decoded, "확인메모비밀") {
			t.Fatalf("mail leaked document text:\n%s", decoded)
		}
	}
	mu.Unlock()

	// Several instances racing for the next period queue it exactly once.
	tomorrow := now.AddDate(0, 0, 1)
	var wg sync.WaitGroup
	results := make(chan int, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, err := s.QueueRecheckDigests(ctx, tomorrow)
			if err != nil {
				t.Error(err)
			}
			results <- q
		}()
	}
	wg.Wait()
	close(results)
	total := 0
	for q := range results {
		total += q
	}
	if total != 1 || outbox(eventRecheckDigest) != 6 {
		t.Fatalf("concurrent queueing: %d projects queued, %d rows (want 1 and 6)", total, outbox(eventRecheckDigest))
	}

	// A disabled channel is skipped, and once every document is confirmed the
	// following period sends nothing.
	off := false
	mustCall(t, tools, admin, "set_notification_channel", NotificationChannelInput{ProjectID: p.ID, ChannelID: hook.ID, Enabled: &off})
	for title, owner := range map[string]Identity{"운영 절차": admin, "배포 체크리스트": admin, "관리자 문서": admin, "떠난 사람 문서": admin} {
		mustCall(t, tools, owner, "confirm_recheck", ConfirmRecheckInput{ProjectID: p.ID, DocumentID: docs[title].ID, Note: "확인함"})
	}
	if q, err := s.QueueRecheckDigests(ctx, now.AddDate(0, 0, 2)); err != nil || q != 0 {
		t.Fatalf("queued with nothing due: %d %v", q, err)
	}
	if outbox(eventRecheckDigest) != 6 {
		t.Fatalf("rows changed with nothing due: %d", outbox(eventRecheckDigest))
	}

	// The scheduler is off when the notifier has no schedule.
	s.Notifier.Digest = RecheckDigestSchedule{}
	if q, err := s.QueueRecheckDigests(ctx, now.AddDate(0, 0, 3)); err != nil || q != 0 {
		t.Fatalf("queued while the schedule is off: %d %v", q, err)
	}
}

// decodeMailBody returns the decoded text part of a message built by sendEmail.
func decodeMailBody(t *testing.T, msg string) string {
	t.Helper()
	_, encoded, ok := strings.Cut(msg, "\r\n\r\n")
	if !ok {
		t.Fatalf("mail without a body: %q", msg)
	}
	return decodeBase64Lines(t, encoded)
}

func decodeBase64Lines(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(encoded), "\r\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
