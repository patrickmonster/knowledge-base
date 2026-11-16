package kb

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func discordTestKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(pub), priv
}

func signDiscord(priv ed25519.PrivateKey, ts string, body []byte) string {
	return hex.EncodeToString(ed25519.Sign(priv, append([]byte(ts), body...)))
}

func TestVerifyDiscordSignature(t *testing.T) {
	pub, priv := discordTestKey(t)
	otherPub, _ := discordTestKey(t)
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":1}`)
	sig := signDiscord(priv, ts, body)

	if !verifyDiscordSignature(pub, ts, sig, body, now) {
		t.Fatal("valid signature rejected")
	}
	cases := map[string]bool{
		"other key":       verifyDiscordSignature(otherPub, ts, sig, body, now),
		"changed body":    verifyDiscordSignature(pub, ts, sig, []byte(`{"type":2}`), now),
		"changed time":    verifyDiscordSignature(pub, strconv.FormatInt(now.Unix()+1, 10), sig, body, now),
		"stale":           verifyDiscordSignature(pub, ts, sig, body, now.Add(discordTimestampSkew+time.Minute)),
		"from the future": verifyDiscordSignature(pub, ts, sig, body, now.Add(-discordTimestampSkew-time.Minute)),
		"empty key":       verifyDiscordSignature("", ts, sig, body, now),
		"short key":       verifyDiscordSignature(pub[:60], ts, sig, body, now),
		"upper-case key":  verifyDiscordSignature(strings.ToUpper(pub), ts, sig, body, now),
		"not hex":         verifyDiscordSignature(pub, ts, "zz"+sig[2:], body, now),
		"short sig":       verifyDiscordSignature(pub, ts, sig[:100], body, now),
		"no signature":    verifyDiscordSignature(pub, ts, "", body, now),
		"bad timestamp":   verifyDiscordSignature(pub, "yesterday", sig, body, now),
	}
	for name, ok := range cases {
		if ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDiscordLinkCode(t *testing.T) {
	code, err := newLinkCode()
	if err != nil || !validLinkCode(code) {
		t.Fatalf("generated code %q: %v", code, err)
	}
	shown := displayLinkCode(code)
	if len(shown) != discordLinkCodeLength+1 || normalizeLinkCode(" "+strings.ToLower(shown)+" ") != code {
		t.Fatalf("display %q does not round-trip to %q", shown, code)
	}
	for _, bad := range []string{"", "ABCD", "ABCD-EFG0", "ABCDEFGHI", "abcd-efg!", "ABCD-EFGI"} {
		if validLinkCode(normalizeLinkCode(bad)) {
			t.Errorf("%q accepted", bad)
		}
	}
	if linkCodeHash(code) == code || linkCodeHash(code) != linkCodeHash(code) || linkCodeHash(code) == linkCodeHash("AAAAAAAA") {
		t.Fatal("link code hash is not a stable one-way digest")
	}
}

func TestCleanDiscordSettings(t *testing.T) {
	pub, _ := discordTestKey(t)
	// A key alone is enough to turn it on; no server list is needed.
	ok, err := cleanDiscordSettings(DiscordSettingsInput{Enabled: true, PublicKey: " " + strings.ToUpper(pub) + " "})
	if err != nil || ok.PublicKey != pub || !ok.Enabled {
		t.Fatalf("normalized: %+v %v", ok, err)
	}
	if off, err := cleanDiscordSettings(DiscordSettingsInput{}); err != nil || off.Enabled || off.PublicKey != "" {
		t.Fatalf("empty settings: %+v %v", off, err)
	}
	for name, in := range map[string]DiscordSettingsInput{
		"on without key": {Enabled: true},
		"short key":      {PublicKey: pub[:62]},
		"not hex":        {PublicKey: strings.Repeat("g", 64)},
	} {
		if _, err := cleanDiscordSettings(in); errorCode(err) != "INVALID_INPUT" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDiscordReplyIsEphemeralAndMentionsNobody(t *testing.T) {
	raw, err := json.Marshal(discordReply("@everyone <@123> hi"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Type int `json:"type"`
		Data struct {
			Content         string `json:"content"`
			Flags           int    `json:"flags"`
			AllowedMentions struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Type != 4 || out.Data.Flags != 64 || out.Data.AllowedMentions.Parse == nil || len(out.Data.AllowedMentions.Parse) != 0 {
		t.Fatalf("reply: %s", raw)
	}
	if !strings.Contains(string(raw), `"parse":[]`) {
		t.Fatalf("allowed_mentions.parse must be an empty list: %s", raw)
	}
	long := discordReply(strings.Repeat("가", discordMessageLimit+50)).Data.Content
	if n := len([]rune(long)); n != discordMessageLimit || !strings.HasSuffix(long, "…") {
		t.Fatalf("not cut to the Discord limit: %d runes", n)
	}
}

func TestFormatDiscordTodos(t *testing.T) {
	todos := []Todo{
		{Title: "작업\n  하나", Status: "in_progress", Priority: "high", AssigneeName: "홍길동", DueDate: "2026-12-01"},
		{Title: "끝난 일", Status: "completed", Priority: "low"},
		{Title: "새 상태", Status: "in_review", Priority: "medium"},
	}
	out := formatDiscordTodos("보드\n프로젝트", todos, "", false)
	for _, want := range []string{"할 일 2건 (완료 제외)", "[진행 중 · 높음] 작업 하나 — 홍길동 · 마감 2026-12-01", "[검토대기 · 보통] 새 상태"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "끝난 일") || strings.Contains(out, "보드\n") {
		t.Errorf("finished to-do or raw newline leaked:\n%s", out)
	}
	if done := formatDiscordTodos("p", todos, "completed", true); !strings.Contains(done, "(완료 · 내 담당)") {
		t.Errorf("filter label: %s", done)
	}
	if empty := formatDiscordTodos("p", nil, "", false); !strings.Contains(empty, "표시할 할 일이 없습니다.") {
		t.Errorf("empty: %s", empty)
	}

	var many []Todo
	for i := 0; i < discordTodoLines+5; i++ {
		many = append(many, Todo{Title: "할 일 " + strconv.Itoa(i), Status: "not_started", Priority: "medium"})
	}
	out = formatDiscordTodos("p", many, "", false)
	if strings.Count(out, "•") != discordTodoLines || !strings.Contains(out, "…외 5건") {
		t.Errorf("not cut to %d lines:\n%s", discordTodoLines, out)
	}
	if n := len([]rune(clipDiscord(out))); n > discordMessageLimit {
		t.Errorf("message of %d characters", n)
	}
}

func TestDiscordOptions(t *testing.T) {
	var ix discordInteraction
	raw := `{"id":"1","type":2,"guild_id":"9","member":{"user":{"id":"42"}},
		"data":{"name":"kb","options":[{"name":"todos","type":1,"options":[
			{"name":"project","type":3,"value":" Board "},{"name":"mine","type":5,"value":true},{"name":"status","type":3,"value":5}]}]}}`
	if err := json.Unmarshal([]byte(raw), &ix); err != nil {
		t.Fatal(err)
	}
	sub := ix.Data.Options[0]
	if ix.userID() != "42" || sub.Name != "todos" || discordString(sub.Options, "project") != "Board" || !discordBool(sub.Options, "mine") {
		t.Fatalf("parsed: %+v", ix)
	}
	if discordString(sub.Options, "status") != "" || discordString(sub.Options, "missing") != "" || discordBool(sub.Options, "project") {
		t.Fatal("a value of the wrong type must read as empty")
	}
	dm := discordInteraction{User: &struct {
		ID string `json:"id"`
	}{ID: "7"}}
	if dm.userID() != "7" {
		t.Fatal("direct message user")
	}
}

// discordSend signs and delivers one request the way Discord does.
func discordSend(t *testing.T, s *Store, priv ed25519.PrivateKey, ts string, body string) (discordResponse, error) {
	t.Helper()
	return s.ReceiveDiscordInteraction(context.Background(), DiscordRequest{Signature: signDiscord(priv, ts, []byte(body)), Timestamp: ts, Body: []byte(body)})
}

func TestDiscordCommands(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "discord-editor@example.com")
	outsider := inviteAs(t, s, admin, "discord-outsider@example.com")
	pub, priv := discordTestKey(t)
	const guild, other = "123456789012345678", "223456789012345678"

	// Off until an administrator saves a key and turns it on.
	if _, err := discordSend(t, s, priv, strconv.FormatInt(time.Now().Unix(), 10), `{"type":1}`); errorCode(err) != "NOT_FOUND" {
		t.Fatalf("disabled endpoint: %v", err)
	}
	if _, err := s.SetDiscordSettings(ctx, editor, DiscordSettingsInput{Enabled: true, PublicKey: pub}); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("only a global administrator may save settings: %v", err)
	}
	if _, err := s.SetDiscordSettings(ctx, admin, DiscordSettingsInput{Enabled: true}); errorCode(err) != "INVALID_INPUT" {
		t.Fatalf("enabling without a key: %v", err)
	}
	saved, err := s.SetDiscordSettings(ctx, admin, DiscordSettingsInput{Enabled: true, PublicKey: pub})
	if err != nil || !saved.(DiscordSettings).Enabled || !strings.HasSuffix(saved.(DiscordSettings).EndpointURL, "/api/discord/interactions") {
		t.Fatalf("save: %+v %v", saved, err)
	}

	now := func() string { return strconv.FormatInt(time.Now().Unix(), 10) }
	if r, err := discordSend(t, s, priv, now(), `{"type":1}`); err != nil || r.Type != discordResponsePong {
		t.Fatalf("ping: %+v %v", r, err)
	}
	_, wrong := discordTestKey(t)
	if _, err := discordSend(t, s, wrong, now(), `{"type":1}`); errorCode(err) != "UNAUTHORIZED" {
		t.Fatalf("signature from another key: %v", err)
	}
	if _, err := discordSend(t, s, priv, strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10), `{"type":1}`); errorCode(err) != "UNAUTHORIZED" {
		t.Fatalf("old timestamp: %v", err)
	}

	next := 1000
	cmd := func(guildID, discordUser, sub string, options string) string {
		t.Helper()
		next++
		body := `{"id":"` + strconv.Itoa(900000+next) + `","type":2,"guild_id":"` + guildID + `","member":{"user":{"id":"` + discordUser +
			`"}},"data":{"name":"kb","options":[{"name":"` + sub + `","type":1,"options":[` + options + `]}]}}`
		r, err := discordSend(t, s, priv, now(), body)
		if err != nil {
			t.Fatalf("/kb %s: %v", sub, err)
		}
		if r.Type != discordResponseMessage || r.Data == nil || r.Data.Flags != discordFlagEphemeral || len(r.Data.AllowedMentions.Parse) != 0 {
			t.Fatalf("reply must be ephemeral without mentions: %+v", r)
		}
		return r.Data.Content
	}

	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "discord-board", Name: "디스코드 보드"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "viewer"})
	mustCall(t, tools, admin, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "열린 할 일", Priority: "high", AssigneeID: editor.User.ID})
	mustCall(t, tools, admin, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "끝난 할 일", Status: "completed"})

	const person = "323456789012345678"
	if out := cmd(guild, person, "todos", ""); !strings.Contains(out, "연결된 계정이 없습니다") {
		t.Fatalf("unlinked caller: %s", out)
	}
	// There is no server list: any server the bot is in works, and so does a direct message.
	if out := cmd(other, person, "todos", ""); !strings.Contains(out, "연결된 계정이 없습니다") {
		t.Fatalf("another server: %s", out)
	}
	dm := `{"id":"888888","type":2,"user":{"id":"` + person + `"},"data":{"name":"kb","options":[{"name":"todos","type":1}]}}`
	if r, err := discordSend(t, s, priv, now(), dm); err != nil || !strings.Contains(r.Data.Content, "연결된 계정이 없습니다") {
		t.Fatalf("direct message: %+v %v", r, err)
	}

	// Link: the code works once, only for the person who made it.
	made, err := s.CreateDiscordLinkCode(ctx, editor)
	if err != nil {
		t.Fatal(err)
	}
	code := made.(DiscordLinkCode).Code
	if out := cmd(guild, person, "link", `{"name":"code","type":3,"value":"ZZZZ-ZZZZ"}`); !strings.Contains(out, "코드가 없거나 만료") {
		t.Fatalf("wrong code: %s", out)
	}
	if out := cmd(guild, person, "link", `{"name":"code","type":3,"value":"`+strings.ToLower(code)+`"}`); !strings.Contains(out, "연결했습니다") {
		t.Fatalf("link: %s", out)
	}
	if out := cmd(guild, "423456789012345678", "link", `{"name":"code","type":3,"value":"`+code+`"}`); !strings.Contains(out, "코드가 없거나 만료") {
		t.Fatalf("a used code must not link again: %s", out)
	}
	status, err := s.DiscordLink(ctx, editor)
	if err != nil || !status.(DiscordLinkStatus).Linked {
		t.Fatalf("link status: %+v %v", status, err)
	}

	// Commands run with that person's own project roles.
	if out := cmd(guild, person, "projects", ""); !strings.Contains(out, "`discord-board`") || !strings.Contains(out, "조회자") {
		t.Fatalf("projects: %s", out)
	}
	out := cmd(guild, person, "todos", "")
	if !strings.Contains(out, "열린 할 일") || strings.Contains(out, "끝난 할 일") || !strings.Contains(out, "디스코드 보드") {
		t.Fatalf("todos: %s", out)
	}
	if out := cmd(guild, person, "todos", `{"name":"status","type":3,"value":"completed"}`); !strings.Contains(out, "끝난 할 일") || strings.Contains(out, "열린 할 일") {
		t.Fatalf("todos by status: %s", out)
	}
	if out := cmd(guild, person, "todos", `{"name":"mine","type":5,"value":true}`); !strings.Contains(out, "열린 할 일") {
		t.Fatalf("my todos: %s", out)
	}
	if out := cmd(guild, person, "todos", `{"name":"project","type":3,"value":"not-mine"}`); !strings.Contains(out, "찾지 못했습니다") {
		t.Fatalf("project the person is not in: %s", out)
	}

	// A person with no project sees nothing, and a replay is refused.
	made, _ = s.CreateDiscordLinkCode(ctx, outsider)
	const stranger = "523456789012345678"
	cmd(guild, stranger, "link", `{"name":"code","type":3,"value":"`+made.(DiscordLinkCode).Code+`"}`)
	if out := cmd(guild, stranger, "todos", ""); !strings.Contains(out, "참여 중인 프로젝트가 없습니다") {
		t.Fatalf("outsider: %s", out)
	}
	replay := `{"id":"777777","type":2,"guild_id":"` + guild + `","member":{"user":{"id":"` + person + `"}},"data":{"name":"kb","options":[{"name":"projects","type":1}]}}`
	if _, err := discordSend(t, s, priv, now(), replay); err != nil {
		t.Fatal(err)
	}
	if _, err := discordSend(t, s, priv, now(), replay); errorCode(err) != "CONFLICT" {
		t.Fatalf("replayed interaction: %v", err)
	}

	// A disabled account stops working at once.
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET disabled=true WHERE id=$1`, editor.User.ID); err != nil {
		t.Fatal(err)
	}
	if out := cmd(guild, person, "todos", ""); !strings.Contains(out, "비활성화") {
		t.Fatalf("disabled account: %s", out)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET disabled=false WHERE id=$1`, editor.User.ID); err != nil {
		t.Fatal(err)
	}

	if out := cmd(guild, person, "unlink", ""); !strings.Contains(out, "해제") {
		t.Fatalf("unlink: %s", out)
	}
	if out := cmd(guild, person, "todos", ""); !strings.Contains(out, "연결된 계정이 없습니다") {
		t.Fatalf("after unlink: %s", out)
	}

	// Turning it off closes the endpoint again.
	if _, err := s.SetDiscordSettings(ctx, admin, DiscordSettingsInput{PublicKey: pub}); err != nil {
		t.Fatal(err)
	}
	if _, err := discordSend(t, s, priv, now(), `{"type":1}`); errorCode(err) != "NOT_FOUND" {
		t.Fatalf("turned off: %v", err)
	}
	if _, err := s.CreateDiscordLinkCode(ctx, editor); errorCode(err) != "CONFLICT" {
		t.Fatalf("no link codes while off: %v", err)
	}
}
