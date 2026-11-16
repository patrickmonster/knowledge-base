package kb

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Registering the /kb slash command (명령 등록).
//
// Discord only shows a slash command after the application has registered it
// with Discord's API, which needs the bot token. The server never stores that
// token: a global administrator types it into the web console, the server
// uses it for this one request to discord.com and drops it. It is not written
// to the database, the audit log, a log line or an error message (it is
// removed from anything Discord echoes back).
//
// The command definition lives here so the registered command always matches
// what the server understands (for example the to-do statuses).

// discordAPIBase is Discord's API. Only tests change it.
var discordAPIBase = "https://discord.com/api/v10"

const (
	discordRegisterTimeout  = 10 * time.Second
	discordRegisterMaxReply = 1 << 20
)

var discordBotToken = regexp.MustCompile(`^[A-Za-z0-9._-]{20,200}$`)

type DiscordRegisterInput struct {
	ApplicationID string `json:"application_id"`
	GuildID       string `json:"guild_id,omitempty"`
	BotToken      string `json:"bot_token"`
}

// DiscordRegisterResult says where the command was registered and which
// commands Discord now has. It never carries the token.
type DiscordRegisterResult struct {
	Scope    string   `json:"scope"` // guild: usable at once; global: Discord may take up to an hour
	Commands []string `json:"commands"`
}

type discordChoice struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type discordCommandOption struct {
	Type        int                    `json:"type"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Required    bool                   `json:"required,omitempty"`
	Choices     []discordChoice        `json:"choices,omitempty"`
	Options     []discordCommandOption `json:"options,omitempty"`
}

type discordCommand struct {
	Name string `json:"name"`
	// Type is omitted for a slash command (1); 3 is a message command, which
	// Discord requires to have no description and no options.
	Type        int                    `json:"type,omitempty"`
	Description string                 `json:"description"`
	Options     []discordCommandOption `json:"options,omitempty"`
}

// discordCommandDefinition is the /kb command with its subcommands
// (option type 1 = subcommand, 3 = string, 5 = boolean).
func discordCommandDefinition() []discordCommand {
	statuses := make([]discordChoice, 0, len(todoStatuses))
	for _, s := range todoStatuses {
		statuses = append(statuses, discordChoice{Name: discordStatusLabel(s), Value: s})
	}
	return []discordCommand{{
		Name: "kb", Description: "지식베이스",
		Options: []discordCommandOption{
			{Type: 1, Name: "link", Description: "내 계정과 연결", Options: []discordCommandOption{
				{Type: 3, Name: "code", Description: "웹 내 계정에서 만든 코드", Required: true}}},
			{Type: 1, Name: "unlink", Description: "연결 해제"},
			{Type: 1, Name: "projects", Description: "내 프로젝트"},
			{Type: 1, Name: "todos", Description: "할 일 목록", Options: []discordCommandOption{
				{Type: 3, Name: "project", Description: "프로젝트 slug"},
				{Type: 3, Name: "status", Description: "상태", Choices: statuses},
				{Type: 5, Name: "mine", Description: "내 담당만"}}},
		},
	}, {
		// Apps → 이슈로 등록 on a message (message commands may use spaces and Korean).
		Name: discordMessageCommandName, Type: discordCommandTypeMessage,
	}}
}

// cleanDiscordRegister validates the input. A pasted "Bot " prefix is dropped.
func cleanDiscordRegister(in DiscordRegisterInput) (app, guild, token string, err error) {
	app = strings.TrimSpace(in.ApplicationID)
	guild = strings.TrimSpace(in.GuildID)
	token = strings.TrimSpace(in.BotToken)
	token = strings.TrimSpace(strings.TrimPrefix(token, "Bot "))
	if !discordSnowflake.MatchString(app) {
		return "", "", "", problem("INVALID_INPUT", "애플리케이션 ID는 숫자입니다(개발자 포털 → General Information → Application ID).")
	}
	if guild != "" && !discordSnowflake.MatchString(guild) {
		return "", "", "", problem("INVALID_INPUT", "서버 ID는 숫자입니다. 모든 서버에 등록하려면 비워 두세요.")
	}
	if !discordBotToken.MatchString(token) {
		return "", "", "", problem("INVALID_INPUT", "봇 토큰 형식이 맞지 않습니다(개발자 포털 → Bot → Reset Token으로 받은 값).")
	}
	return app, guild, token, nil
}

// discordRejected turns Discord's error answer into a hint for the operator.
// Anything Discord echoes is cleaned of the token first.
func discordRejected(status int, body []byte, token string) error {
	var reply struct {
		Code       int     `json:"code"`
		Message    string  `json:"message"`
		RetryAfter float64 `json:"retry_after"`
	}
	_ = json.Unmarshal(body, &reply)
	detail := strings.ReplaceAll(reply.Message, token, "[token]")
	if len(detail) > 200 {
		detail = detail[:200]
	}
	switch {
	case status == http.StatusUnauthorized:
		return problem("INVALID_INPUT", "Discord가 봇 토큰을 거부했습니다(401). Bot 탭에서 Reset Token으로 새로 받아 입력하세요.")
	case status == http.StatusForbidden || reply.Code == 50001:
		return problem("INVALID_INPUT", "봇이 그 서버에 없거나 권한이 없습니다("+fmt.Sprint(status)+"). applications.commands 범위로 봇을 서버에 다시 초대하세요.")
	case status == http.StatusNotFound:
		return problem("INVALID_INPUT", "애플리케이션 ID 또는 서버 ID를 Discord에서 찾지 못했습니다(404).")
	case status == http.StatusTooManyRequests:
		return problem("RATE_LIMITED", fmt.Sprintf("Discord가 요청을 제한했습니다. %.0f초 뒤 다시 시도하세요.", reply.RetryAfter+1))
	}
	return problem("INVALID_INPUT", fmt.Sprintf("Discord가 명령 등록을 거부했습니다(%d, 코드 %d): %s", status, reply.Code, detail))
}

// callDiscordRegister replaces the application's commands with ours (a bulk
// overwrite, so running it again is safe) and returns their names.
func callDiscordRegister(ctx context.Context, base, app, guild, token string) ([]string, error) {
	url := base + "/applications/" + app + "/commands"
	if guild != "" {
		url = base + "/applications/" + app + "/guilds/" + guild + "/commands"
	}
	payload, err := json.Marshal(discordCommandDefinition())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, discordRegisterTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return nil, problem("INVALID_INPUT", "요청을 만들지 못했습니다.")
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DiscordBot (knowledge-base, 1.0)")
	client := &http.Client{Timeout: discordRegisterTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		// The error text can carry the URL; say only what the operator can act on.
		return nil, problem("DISCORD_UNAVAILABLE", "Discord API에 연결하지 못했습니다. 잠시 후 다시 시도하세요.")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, discordRegisterMaxReply))
	if err != nil {
		return nil, problem("DISCORD_UNAVAILABLE", "Discord API 응답을 읽지 못했습니다. 잠시 후 다시 시도하세요.")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, discordRejected(resp.StatusCode, body, token)
	}
	var registered []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &registered); err != nil {
		return nil, problem("DISCORD_UNAVAILABLE", "Discord API 응답을 해석하지 못했습니다.")
	}
	names := make([]string, 0, len(registered))
	for _, c := range registered {
		names = append(names, c.Name)
	}
	return names, nil
}

// RegisterDiscordCommands registers /kb with Discord. Web session, global
// administrator only (Perform rejects MCP keys and non-administrators before
// anything runs). The audit entry records only whether it was a server or a
// global registration.
func (s *Store) RegisterDiscordCommands(ctx context.Context, actor Identity, in DiscordRegisterInput) (any, error) {
	scope := "global"
	if strings.TrimSpace(in.GuildID) != "" {
		scope = "guild"
	}
	ctx = auditInput(ctx, map[string]any{"scope": scope})
	return s.Perform(ctx, actor, "register_discord_commands", "", false, true, func(tx *sql.Tx) (any, error) {
		app, guild, token, err := cleanDiscordRegister(in)
		if err != nil {
			return nil, err
		}
		if err := s.RateLimit(ctx, "discord-register:"+actor.User.ID, 10, 600); err != nil {
			return nil, err
		}
		names, err := callDiscordRegister(ctx, discordAPIBase, app, guild, token)
		if err != nil {
			return nil, err
		}
		return DiscordRegisterResult{Scope: scope, Commands: names}, nil
	})
}
