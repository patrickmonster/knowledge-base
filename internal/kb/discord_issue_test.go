package kb

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDiscordIssueTitleAndBody(t *testing.T) {
	thread := discordInteraction{GuildID: "123456789012345678", Channel: &discordChannelRef{ID: "223456789012345678", Name: "아바타  스트리머\n제외 설정", Type: 11}}
	if got := discordIssueTitle(thread, "본문 첫 줄"); got != "아바타 스트리머 제외 설정" {
		t.Errorf("thread title: %q", got)
	}
	text := discordInteraction{Channel: &discordChannelRef{ID: "223456789012345678", Name: "일반", Type: 0}}
	if got := discordIssueTitle(text, "\n\n  첫   줄  \n둘째"); got != "첫 줄" {
		t.Errorf("first line title: %q", got)
	}
	if got := discordIssueTitle(discordInteraction{}, ""); got != "Discord 메시지" {
		t.Errorf("fallback title: %q", got)
	}
	if got := discordIssueTitle(text, strings.Repeat("가", 500)); len([]rune(got)) != discordIssueTitleLimit {
		t.Errorf("title not cut: %d", len([]rune(got)))
	}

	var msg discordSourceMessage
	raw := `{"id":"323456789012345678","channel_id":"223456789012345678","content":"자동답변이 안 됩니다","author":{"username":"parut","global_name":"파릇"},
		"attachments":[{"url":"https://cdn.discordapp.com/a/b.png?x=1","filename":"스크린 [샷].png"},{"url":"http://insecure.example/c.png","filename":"c.png"}]}`
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	body := discordIssueBody(thread, msg, msg.Content)
	for _, want := range []string{"자동답변이 안 됩니다", "작성자: 파릇", "https://discord.com/channels/123456789012345678/223456789012345678/323456789012345678", "[스크린 (샷).png](https://cdn.discordapp.com/a/b.png?x=1)"} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "insecure.example") {
		t.Errorf("a non-https attachment must be left out:\n%s", body)
	}
	if dm := discordIssueBody(discordInteraction{}, msg, ""); !strings.Contains(dm, "/channels/@me/") {
		t.Errorf("direct message link:\n%s", dm)
	}
}

func TestDiscordMessageCommandParsing(t *testing.T) {
	var ix discordInteraction
	body := `{"id":"1","type":2,"guild_id":"5","channel":{"id":"22","name":"제목","parent_id":"11","type":11},"member":{"user":{"id":"42"}},
		"data":{"name":"이슈로 등록","type":3,"target_id":"33","resolved":{"messages":{"33":{"id":"33","channel_id":"22","content":"hi"}}}}}`
	if err := json.Unmarshal([]byte(body), &ix); err != nil {
		t.Fatal(err)
	}
	m := ix.Data.Resolved.Messages[ix.Data.TargetID]
	if ix.Data.Type != discordCommandTypeMessage || m.Content != "hi" || ix.Channel.ParentID != "11" || !ix.Channel.isThread() {
		t.Fatalf("parsed: %+v %+v", ix.Data, ix.Channel)
	}
}

func TestDiscordMessageCommandCreatesIssue(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "discord-issue-editor@example.com")
	pub, priv := discordTestKey(t)
	if _, err := s.SetDiscordSettings(ctx, admin, DiscordSettingsInput{Enabled: true, PublicKey: pub}); err != nil {
		t.Fatal(err)
	}
	now := func() string { return strconv.FormatInt(time.Now().Unix(), 10) }
	const guild, forum, thread, person = "123456789012345678", "223456789012345678", "323456789012345678", "423456789012345678"

	next := 5000
	use := func(user, messageID, channelParent, content string) string {
		t.Helper()
		next++
		body := `{"id":"` + strconv.Itoa(800000+next) + `","type":2,"guild_id":"` + guild + `","channel":{"id":"` + thread + `","name":"아바타 스트리머 제외 설정","parent_id":"` + channelParent +
			`","type":11},"member":{"user":{"id":"` + user + `"}},"data":{"name":"` + discordMessageCommandName + `","type":3,"target_id":"` + messageID +
			`","resolved":{"messages":{"` + messageID + `":{"id":"` + messageID + `","channel_id":"` + thread + `","content":` + strconv.Quote(content) +
			`,"author":{"username":"parut","global_name":"파릇파릇지"}}}}}}`
		r, err := discordSend(t, s, priv, now(), body)
		if err != nil {
			t.Fatal(err)
		}
		if r.Type != discordResponseMessage || r.Data == nil || r.Data.Flags != discordFlagEphemeral || len(r.Data.AllowedMentions.Parse) != 0 {
			t.Fatalf("reply must be ephemeral without mentions: %+v", r)
		}
		return r.Data.Content
	}
	issueCount := func(project string) (n int) {
		t.Helper()
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM issues WHERE project_id=$1`, project).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	a := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "feature-requests", Name: "추가 기능 요청"}).(Project)
	b := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "read-only-board", Name: "읽기 전용"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: a.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: b.ID, UserID: editor.User.ID, Role: "viewer"})

	// Not linked yet.
	if out := use(person, "500000000000000001", forum, "아바타 기능"); !strings.Contains(out, "연결된 계정이 없습니다") {
		t.Fatalf("unlinked caller: %s", out)
	}
	made, err := s.CreateDiscordLinkCode(ctx, editor)
	if err != nil {
		t.Fatal(err)
	}
	link := `{"id":"810001","type":2,"guild_id":"` + guild + `","member":{"user":{"id":"` + person + `"}},"data":{"name":"kb","options":[{"name":"link","type":1,"options":[{"name":"code","type":3,"value":"` + made.(DiscordLinkCode).Code + `"}]}]}}`
	if r, err := discordSend(t, s, priv, now(), link); err != nil || !strings.Contains(r.Data.Content, "연결했습니다") {
		t.Fatalf("link: %+v %v", r, err)
	}

	// Two projects and no mapping: refused with a hint, nothing created.
	if out := use(person, "500000000000000001", forum, "아바타 기능"); !strings.Contains(out, "연결된 프로젝트가 없습니다") {
		t.Fatalf("no mapping: %s", out)
	}
	if issueCount(a.ID)+issueCount(b.ID) != 0 {
		t.Fatal("nothing may be created without a project")
	}

	// Only a global administrator maps channels, and the input is checked.
	if _, err := s.SetDiscordChannel(ctx, editor, DiscordChannelInput{ChannelID: forum, ProjectSlug: a.Slug}); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("only a global administrator may map: %v", err)
	}
	if _, err := s.SetDiscordChannel(ctx, admin, DiscordChannelInput{ChannelID: "not-a-number", ProjectSlug: a.Slug}); errorCode(err) != "INVALID_INPUT" {
		t.Fatalf("bad channel: %v", err)
	}
	if _, err := s.SetDiscordChannel(ctx, admin, DiscordChannelInput{ChannelID: forum, ProjectSlug: "no-such-project"}); errorCode(err) != "NOT_FOUND" {
		t.Fatalf("unknown project: %v", err)
	}
	if _, err := s.SetDiscordChannel(ctx, admin, DiscordChannelInput{ChannelID: forum, ProjectSlug: " Feature-Requests "}); err != nil {
		t.Fatal(err)
	}
	status, err := s.DiscordSettingsStatus(ctx, admin)
	if err != nil || len(status.(DiscordSettings).Channels) != 1 || status.(DiscordSettings).Channels[0].ProjectSlug != "feature-requests" {
		t.Fatalf("channels in settings: %+v %v", status, err)
	}

	// The forum post becomes an issue, titled after the thread.
	out := use(person, "500000000000000001", forum, "아바타 기능에 스트리머는 채팅 쳤을때 표시되지 않게 해주세요.")
	if !strings.Contains(out, "이슈로 등록했습니다") || !strings.Contains(out, "아바타 스트리머 제외 설정") || !strings.Contains(out, "추가 기능 요청") {
		t.Fatalf("create: %s", out)
	}
	var title, body, kind, creator string
	var tags []string
	row := s.DB.QueryRowContext(ctx, `SELECT title,body,kind,created_by::text,tags FROM issues WHERE project_id=$1`, a.ID)
	var rawTags []byte
	if err := row.Scan(&title, &body, &kind, &creator, &rawTags); err != nil {
		t.Fatal(err)
	}
	_ = tags
	if title != "아바타 스트리머 제외 설정" || kind != "issue" || creator != editor.User.ID || !strings.Contains(body, "표시되지 않게") ||
		!strings.Contains(body, "/channels/"+guild+"/"+thread+"/500000000000000001") || !strings.Contains(string(rawTags), "discord") {
		t.Fatalf("issue: %q %q %q %q %s", title, body, kind, creator, rawTags)
	}

	// The same message is never registered twice.
	if out := use(person, "500000000000000001", forum, "아바타 기능에 스트리머는 채팅 쳤을때 표시되지 않게 해주세요."); !strings.Contains(out, "이미 이슈로 등록된 메시지") {
		t.Fatalf("duplicate: %s", out)
	}
	if n := issueCount(a.ID); n != 1 {
		t.Fatalf("issues after duplicate: %d", n)
	}
	// Another message makes another issue.
	if out := use(person, "500000000000000002", forum, "다른 요청"); !strings.Contains(out, "이슈로 등록했습니다") {
		t.Fatalf("second message: %s", out)
	}

	// A project where the person is only a viewer refuses, creating nothing.
	if _, err := s.SetDiscordChannel(ctx, admin, DiscordChannelInput{ChannelID: forum, ProjectSlug: b.Slug}); err != nil {
		t.Fatal(err)
	}
	if out := use(person, "500000000000000003", forum, "읽기 전용 프로젝트"); strings.Contains(out, "이슈로 등록했습니다") || issueCount(b.ID) != 0 {
		t.Fatalf("viewer must not create: %s", out)
	}

	// Removing the mapping returns to the "no project" hint.
	if _, err := s.SetDiscordChannel(ctx, admin, DiscordChannelInput{ChannelID: forum}); err != nil {
		t.Fatal(err)
	}
	if out := use(person, "500000000000000004", forum, "매핑 없음"); !strings.Contains(out, "연결된 프로젝트가 없습니다") {
		t.Fatalf("after unmapping: %s", out)
	}

	// A message without text or files is refused; an unknown message command is ignored.
	if out := use(person, "500000000000000005", forum, "   "); !strings.Contains(out, "내용이 없는 메시지") {
		t.Fatalf("empty message: %s", out)
	}
	unknown := `{"id":"820001","type":2,"guild_id":"` + guild + `","member":{"user":{"id":"` + person + `"}},"data":{"name":"다른 명령","type":3,"target_id":"1"}}`
	if r, err := discordSend(t, s, priv, now(), unknown); err != nil || !strings.Contains(r.Data.Content, "알 수 없는 메시지 명령") {
		t.Fatalf("unknown message command: %+v %v", r, err)
	}
}
