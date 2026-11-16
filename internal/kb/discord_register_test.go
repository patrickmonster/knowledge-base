package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// A made-up value shaped like a bot token. It is assembled at run time so the
// source holds no string that secret scanners mistake for a real token.
var registerTestToken = strings.Join([]string{"fake-test-application", "partTwo", "test-only_value-1234567890"}, ".")

func TestDiscordCommandDefinition(t *testing.T) {
	raw, err := json.Marshal(discordCommandDefinition())
	if err != nil {
		t.Fatal(err)
	}
	var cmds []discordCommand
	if err := json.Unmarshal(raw, &cmds); err != nil || len(cmds) != 2 || cmds[0].Name != "kb" {
		t.Fatalf("definition: %s %v", raw, err)
	}
	name := regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	var walk func(opts []discordCommandOption)
	walk = func(opts []discordCommandOption) {
		for _, o := range opts {
			if !name.MatchString(o.Name) || o.Description == "" || utf8.RuneCountInString(o.Description) > 100 {
				t.Errorf("option %q does not meet Discord's limits", o.Name)
			}
			walk(o.Options)
		}
	}
	walk(cmds[0].Options)
	// The message command (Apps → 이슈로 등록) has no description or options, as Discord requires.
	if m := cmds[1]; m.Type != discordCommandTypeMessage || m.Name != discordMessageCommandName || m.Description != "" || len(m.Options) != 0 ||
		utf8.RuneCountInString(m.Name) > 32 || strings.Contains(string(raw), `"type":3,"description":"","options"`) {
		t.Errorf("message command: %+v", m)
	}
	subs := map[string]bool{}
	var statusChoices []string
	for _, o := range cmds[0].Options {
		subs[o.Name] = o.Type == 1
		for _, sub := range o.Options {
			if sub.Name == "status" {
				for _, c := range sub.Choices {
					statusChoices = append(statusChoices, c.Value)
				}
			}
		}
	}
	for _, want := range []string{"link", "unlink", "projects", "todos"} {
		if !subs[want] {
			t.Errorf("missing subcommand %s", want)
		}
	}
	// The registered choices are the statuses the board really has.
	if strings.Join(statusChoices, ",") != strings.Join(todoStatuses, ",") {
		t.Errorf("status choices %v, board statuses %v", statusChoices, todoStatuses)
	}
}

func TestCleanDiscordRegister(t *testing.T) {
	app, guild, token, err := cleanDiscordRegister(DiscordRegisterInput{ApplicationID: " 123456789012345678 ", BotToken: "Bot " + registerTestToken + " "})
	if err != nil || app != "123456789012345678" || guild != "" || token != registerTestToken {
		t.Fatalf("clean: %q %q %q %v", app, guild, token, err)
	}
	if _, g, _, err := cleanDiscordRegister(DiscordRegisterInput{ApplicationID: "123456789012345678", GuildID: "223456789012345678", BotToken: registerTestToken}); err != nil || g != "223456789012345678" {
		t.Fatalf("with server: %q %v", g, err)
	}
	for name, in := range map[string]DiscordRegisterInput{
		"no app id":       {BotToken: registerTestToken},
		"app id letters":  {ApplicationID: "abc", BotToken: registerTestToken},
		"server id bad":   {ApplicationID: "123456789012345678", GuildID: "../x", BotToken: registerTestToken},
		"no token":        {ApplicationID: "123456789012345678"},
		"short token":     {ApplicationID: "123456789012345678", BotToken: "short"},
		"token with line": {ApplicationID: "123456789012345678", BotToken: registerTestToken + "\nX-Evil: 1"},
		"token with url":  {ApplicationID: "123456789012345678", BotToken: "https://evil.example/" + registerTestToken},
	} {
		if _, _, _, err := cleanDiscordRegister(in); errorCode(err) != "INVALID_INPUT" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// fakeDiscord answers like Discord's command endpoint and records the request.
type fakeDiscord struct {
	mu     sync.Mutex
	method string
	path   string
	auth   string
	body   []byte
	calls  int
}

func (f *fakeDiscord) handler(status int, reply string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		f.method, f.path, f.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		f.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}
}

func TestCallDiscordRegister(t *testing.T) {
	ctx := context.Background()
	const app, guild = "123456789012345678", "223456789012345678"

	fake := &fakeDiscord{}
	srv := httptest.NewServer(fake.handler(200, `[{"id":"1","name":"kb"}]`))
	defer srv.Close()
	names, err := callDiscordRegister(ctx, srv.URL, app, guild, registerTestToken)
	if err != nil || len(names) != 1 || names[0] != "kb" {
		t.Fatalf("server registration: %v %v", names, err)
	}
	if fake.method != "PUT" || fake.path != "/applications/"+app+"/guilds/"+guild+"/commands" || fake.auth != "Bot "+registerTestToken {
		t.Fatalf("request: %s %s auth=%q", fake.method, fake.path, fake.auth)
	}
	var sent []discordCommand
	if err := json.Unmarshal(fake.body, &sent); err != nil || len(sent) != 2 || sent[0].Name != "kb" || sent[1].Type != discordCommandTypeMessage {
		t.Fatalf("body: %s %v", fake.body, err)
	}
	if _, err := callDiscordRegister(ctx, srv.URL, app, "", registerTestToken); err != nil || fake.path != "/applications/"+app+"/commands" {
		t.Fatalf("global registration: %v path=%s", err, fake.path)
	}

	for name, c := range map[string]struct {
		status int
		reply  string
		code   string
		want   string
	}{
		"bad token":       {401, `{"message":"401: Unauthorized","code":0}`, "INVALID_INPUT", "토큰을 거부"},
		"bot not in it":   {403, `{"message":"Missing Access","code":50001}`, "INVALID_INPUT", "applications.commands"},
		"missing access":  {400, `{"message":"Missing Access","code":50001}`, "INVALID_INPUT", "applications.commands"},
		"unknown app":     {404, `{"message":"Unknown Application","code":10002}`, "INVALID_INPUT", "찾지 못했습니다"},
		"rate limited":    {429, `{"message":"You are being rate limited.","retry_after":4.2}`, "RATE_LIMITED", "5초"},
		"invalid command": {400, `{"message":"Invalid Form Body","code":50035}`, "INVALID_INPUT", "50035"},
		"discord down":    {500, `not json`, "INVALID_INPUT", "500"},
	} {
		f := &fakeDiscord{}
		s := httptest.NewServer(f.handler(c.status, c.reply))
		_, err := callDiscordRegister(ctx, s.URL, app, guild, registerTestToken)
		s.Close()
		if errorCode(err) != c.code || !strings.Contains(PublicError(err), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Whatever Discord echoes is cleaned of the token.
	echo := &fakeDiscord{}
	s := httptest.NewServer(echo.handler(400, `{"message":"bad token `+registerTestToken+` here","code":50035}`))
	_, err = callDiscordRegister(ctx, s.URL, app, guild, registerTestToken)
	s.Close()
	if err == nil || strings.Contains(PublicError(err), registerTestToken) || !strings.Contains(PublicError(err), "[token]") {
		t.Fatalf("token leaked into the error: %v", err)
	}

	// An unreachable Discord is reported without the address.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	if _, err := callDiscordRegister(ctx, url, app, guild, registerTestToken); errorCode(err) != "DISCORD_UNAVAILABLE" || strings.Contains(PublicError(err), "127.0.0.1") {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestRegisterDiscordCommandsAdminOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "register-editor@example.com")

	fake := &fakeDiscord{}
	srv := httptest.NewServer(fake.handler(200, `[{"id":"1","name":"kb"}]`))
	defer srv.Close()
	old := discordAPIBase
	discordAPIBase = srv.URL
	defer func() { discordAPIBase = old }()
	in := DiscordRegisterInput{ApplicationID: "123456789012345678", GuildID: "223456789012345678", BotToken: registerTestToken}

	// Only a global administrator's web session may do it, and Discord is not called otherwise.
	if _, err := s.RegisterDiscordCommands(ctx, editor, in); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("editor: %v", err)
	}
	keyed := admin
	keyed.KeyID = "00000000-0000-4000-8000-000000000000"
	if _, err := s.RegisterDiscordCommands(ctx, keyed, in); err == nil {
		t.Fatal("an MCP key must not register commands")
	}
	if fake.calls != 0 {
		t.Fatalf("Discord was called %d times before the caller was authorized", fake.calls)
	}
	if _, err := s.RegisterDiscordCommands(ctx, admin, DiscordRegisterInput{ApplicationID: "x", BotToken: registerTestToken}); errorCode(err) != "INVALID_INPUT" {
		t.Fatalf("bad input: %v", err)
	}

	out, err := s.RegisterDiscordCommands(ctx, admin, in)
	res, ok := out.(DiscordRegisterResult)
	if err != nil || !ok || res.Scope != "guild" || len(res.Commands) != 1 || res.Commands[0] != "kb" || fake.calls != 1 {
		t.Fatalf("admin: %+v %v calls=%d", out, err, fake.calls)
	}

	// The audit trail says it happened, and neither it nor the database holds the token.
	var details string
	if err := s.DB.QueryRowContext(ctx, `SELECT coalesce(string_agg(action||' '||outcome||' '||details::text,' | '),'') FROM audit_events WHERE action='register_discord_commands'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, "register_discord_commands success") || strings.Contains(details, registerTestToken) || strings.Contains(details, "123456789012345678") {
		t.Fatalf("audit: %s", details)
	}
	var found bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE details::text LIKE '%'||$1||'%')`, registerTestToken).Scan(&found); err != nil || found {
		t.Fatalf("the token reached the audit log: %v %v", found, err)
	}
}

// A global registration says where the commands run, from the install types
// the application has turned on; a server registration leaves that out.
func TestDiscordRegisterPlaces(t *testing.T) {
	ctx := context.Background()
	const app, guild = "123456789012345678", "223456789012345678"
	register := func(appInfoStatus int, appInfo, guildID string) (sent []discordCommand, appInfoCalls int) {
		t.Helper()
		var put []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet && r.URL.Path == "/applications/@me" {
				appInfoCalls++
				if r.Header.Get("Authorization") != "Bot "+registerTestToken {
					t.Errorf("app info auth %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(appInfoStatus)
				io.WriteString(w, appInfo)
				return
			}
			put, _ = io.ReadAll(r.Body)
			io.WriteString(w, `[{"id":"1","name":"kb"}]`)
		}))
		defer srv.Close()
		if _, err := callDiscordRegister(ctx, srv.URL, app, guildID, registerTestToken); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(put, &sent); err != nil || len(sent) != 2 {
			t.Fatalf("body %s %v", put, err)
		}
		return sent, appInfoCalls
	}
	check := func(name string, cmds []discordCommand, types, contexts string) {
		t.Helper()
		for _, c := range cmds {
			if got := fmt.Sprint(c.IntegrationTypes); got != types {
				t.Errorf("%s %s integration_types %s, want %s", name, c.Name, got, types)
			}
			if got := fmt.Sprint(c.Contexts); got != contexts {
				t.Errorf("%s %s contexts %s, want %s", name, c.Name, got, contexts)
			}
		}
	}

	both, calls := register(200, `{"id":"1","integration_types_config":{"0":{},"1":{"oauth2_install_params":null}}}`, "")
	if calls != 1 {
		t.Fatalf("app info calls %d", calls)
	}
	check("server and user install", both, "[0 1]", "[0 1 2]")

	userOnly, _ := register(200, `{"integration_types_config":{"1":{}}}`, "")
	check("user install only", userOnly, "[1]", "[0 1 2]")

	serverOnly, _ := register(200, `{"integration_types_config":{"0":{}}}`, "")
	check("server install only", serverOnly, "[0]", "[0 1]")

	// When Discord does not say, a server install (which every app has) is assumed.
	failed, _ := register(500, `nope`, "")
	check("app info failed", failed, "[0]", "[0 1]")
	empty, _ := register(200, `{}`, "")
	check("no config", empty, "[0]", "[0 1]")

	// A server registration lives only in that server: no lookup, no fields.
	inGuild, calls := register(200, `{"integration_types_config":{"1":{}}}`, guild)
	if calls != 0 {
		t.Errorf("server registration looked up the app %d times", calls)
	}
	check("server registration", inGuild, "[]", "[]")
	raw, _ := json.Marshal(inGuild)
	if strings.Contains(string(raw), "integration_types") || strings.Contains(string(raw), "contexts") {
		t.Errorf("server registration sent places: %s", raw)
	}
}
