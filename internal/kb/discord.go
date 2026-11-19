package kb

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Discord slash commands (Discord 슬래시 명령).
//
// Discord posts every slash command to POST /api/discord/interactions and the
// answer is the HTTP response, so no bot process, gateway connection or bot
// token lives on this server. The request is trusted only when its Ed25519
// signature matches the application public key saved by a global
// administrator. The public key is not a secret.
//
// A Discord account acts as a person only after that person links it: they
// create a single-use code in the web console (내 계정) and enter it with
// /kb link. Commands then run with that person's own project roles. Answers
// are ephemeral (only the caller sees them), cannot mention anyone, and carry
// titles and counts only, never document text.
//
// This first version is read-only: /kb link, unlink, projects and todos.

const (
	discordMaxBody        = 64 << 10
	discordTimestampSkew  = 5 * time.Minute
	discordLinkCodeTTL    = 10 * time.Minute
	discordLinkCodeLength = 8
	discordMessageLimit   = 2000 // Discord's maximum message length in characters
	discordTodoLines      = 15
	discordProjectLines   = 25

	discordInteractionPing    = 1
	discordInteractionCommand = 2
	discordResponsePong       = 1
	discordResponseMessage    = 4
	discordFlagEphemeral      = 64
)

var (
	discordSnowflake   = regexp.MustCompile(`^[0-9]{5,25}$`)
	discordPublicKeyRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	discordSlug        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

// discordCodeAlphabet leaves out 0/O, 1/I and L, which are easy to misread.
const discordCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

var discordStatusLabels = map[string]string{
	"not_started": "시작 전", "in_progress": "진행 중", "in_review": "검토대기", "pending_release": "반영 대기", "completed": "완료", "idea": "아이디어",
}
var discordPriorityLabels = map[string]string{"high": "높음", "medium": "보통", "low": "낮음"}

// DiscordSettings is the server-wide configuration. EndpointURL is the
// address to paste into the Discord developer portal.
type DiscordSettings struct {
	Enabled     bool   `json:"enabled"`
	PublicKey   string `json:"public_key"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	EndpointURL string `json:"endpoint_url,omitempty"`
	// Channels map Discord channels to the project the message command files issues in.
	Channels []DiscordChannel `json:"channels,omitempty"`
}

type DiscordSettingsInput struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
}

type DiscordLinkStatus struct {
	Enabled  bool   `json:"enabled"`
	Linked   bool   `json:"linked"`
	LinkedAt string `json:"linked_at,omitempty"`
}

type DiscordLinkCode struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
}

// DiscordRequest is one signed delivery.
type DiscordRequest struct {
	Signature string
	Timestamp string
	Body      []byte
}

type discordOption struct {
	Name    string          `json:"name"`
	Type    int             `json:"type"`
	Value   json.RawMessage `json:"value"`
	Options []discordOption `json:"options"`
}

type discordInteraction struct {
	ID      string `json:"id"`
	Type    int    `json:"type"`
	GuildID string `json:"guild_id"`
	Member  *struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"member"`
	User *struct {
		ID string `json:"id"`
	} `json:"user"`
	// Channel is where the command was used; for a forum post it is the thread
	// and ParentID is the forum.
	Channel *discordChannelRef `json:"channel"`
	Data    *struct {
		Name    string          `json:"name"`
		Type    int             `json:"type"` // 1 slash command (default), 3 message command
		Options []discordOption `json:"options"`
		// TargetID and Resolved carry the message a message command was used on.
		TargetID string `json:"target_id"`
		Resolved struct {
			Messages map[string]discordSourceMessage `json:"messages"`
		} `json:"resolved"`
	} `json:"data"`
}

type discordChannelRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
	Type     int    `json:"type"`
}

// isThread is true for news, public and private threads (forum posts are public threads).
func (c discordChannelRef) isThread() bool { return c.Type == 10 || c.Type == 11 || c.Type == 12 }

func (i discordInteraction) userID() string {
	if i.Member != nil && i.Member.User.ID != "" {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

type discordMessage struct {
	Content         string `json:"content"`
	Flags           int    `json:"flags"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

type discordResponse struct {
	Type int             `json:"type"`
	Data *discordMessage `json:"data,omitempty"`
}

// discordReply is an ephemeral message nobody can be mentioned in.
func discordReply(text string) discordResponse {
	m := &discordMessage{Content: clipDiscord(text), Flags: discordFlagEphemeral}
	m.AllowedMentions.Parse = []string{}
	return discordResponse{Type: discordResponseMessage, Data: m}
}

func clipDiscord(text string) string {
	if utf8.RuneCountInString(text) <= discordMessageLimit {
		return text
	}
	return string([]rune(text)[:discordMessageLimit-1]) + "…"
}

// verifyDiscordSignature checks X-Signature-Ed25519 over timestamp+body.
func verifyDiscordSignature(publicKeyHex, timestamp, signatureHex string, body []byte, now time.Time) bool {
	if !discordPublicKeyRe.MatchString(publicKeyHex) {
		return false
	}
	key, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(signatureHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(seconds, 0)); d > discordTimestampSkew || d < -discordTimestampSkew {
		return false
	}
	msg := make([]byte, 0, len(timestamp)+len(body))
	msg = append(append(msg, timestamp...), body...)
	return ed25519.Verify(ed25519.PublicKey(key), msg, sig)
}

func normalizeLinkCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func validLinkCode(code string) bool {
	if len(code) != discordLinkCodeLength {
		return false
	}
	for _, r := range code {
		if !strings.ContainsRune(discordCodeAlphabet, r) {
			return false
		}
	}
	return true
}

func linkCodeHash(code string) string {
	sum := sha256.Sum256([]byte("discord-link:" + code))
	return hex.EncodeToString(sum[:])
}

func newLinkCode() (string, error) {
	var raw [discordLinkCodeLength]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	out := make([]byte, discordLinkCodeLength)
	for i, b := range raw {
		// 256 is not a multiple of 31; the bias is far below what a
		// single-use ten-minute code needs.
		out[i] = discordCodeAlphabet[int(b)%len(discordCodeAlphabet)]
	}
	return string(out), nil
}

func displayLinkCode(code string) string { return code[:4] + "-" + code[4:] }

func discordOptionValue(opts []discordOption, name string) (json.RawMessage, bool) {
	for _, o := range opts {
		if o.Name == name {
			return o.Value, true
		}
	}
	return nil, false
}

func discordString(opts []discordOption, name string) string {
	raw, ok := discordOptionValue(opts, name)
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func discordBool(opts []discordOption, name string) bool {
	raw, ok := discordOptionValue(opts, name)
	if !ok {
		return false
	}
	var b bool
	return json.Unmarshal(raw, &b) == nil && b
}

// ---- settings ----

func (s *Store) discordSettings(ctx context.Context, q Querier) (DiscordSettings, error) {
	out, err := one[DiscordSettings](ctx, q, `SELECT jsonb_build_object('enabled',enabled,'public_key',public_key,
		'updated_at',updated_at) FROM discord_settings`)
	if errors.Is(err, sql.ErrNoRows) {
		return DiscordSettings{}, nil
	}
	return out, err
}

func (s *Store) discordEndpointURL() string { return s.PublicURL + "/api/discord/interactions" }

// DiscordSettingsStatus is the administrator's view of the configuration.
func (s *Store) DiscordSettingsStatus(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "view_discord_settings", "", false, true, func(tx *sql.Tx) (any, error) {
		out, err := s.discordSettings(ctx, tx)
		if err != nil {
			return out, err
		}
		out.EndpointURL = s.discordEndpointURL()
		out.Channels, err = s.discordChannels(ctx, tx)
		return out, err
	})
}

func cleanDiscordSettings(in DiscordSettingsInput) (DiscordSettingsInput, error) {
	in.PublicKey = strings.ToLower(strings.TrimSpace(in.PublicKey))
	if in.PublicKey != "" && !discordPublicKeyRe.MatchString(in.PublicKey) {
		return in, problem("INVALID_INPUT", "The public key is 64 hexadecimal characters (Discord developer portal → General Information → Public Key).")
	}
	if in.Enabled && in.PublicKey == "" {
		return in, problem("INVALID_INPUT", "Save the public key before turning Discord on.")
	}
	return in, nil
}

// SetDiscordSettings stores the configuration. Web session, global
// administrator only (Perform rejects MCP keys for admin operations).
func (s *Store) SetDiscordSettings(ctx context.Context, actor Identity, in DiscordSettingsInput) (any, error) {
	in, err := cleanDiscordSettings(in)
	if err != nil {
		return nil, err
	}
	ctx = auditInput(ctx, map[string]any{"enabled": in.Enabled})
	return s.Perform(ctx, actor, "update_discord_settings", "", false, true, func(tx *sql.Tx) (any, error) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO discord_settings(singleton,enabled,public_key,updated_by,updated_at)
			VALUES(true,$1,$2,$3,clock_timestamp())
			ON CONFLICT(singleton) DO UPDATE SET enabled=$1,public_key=$2,updated_by=$3,updated_at=clock_timestamp()`,
			in.Enabled, in.PublicKey, actor.User.ID); err != nil {
			return nil, err
		}
		out, err := s.discordSettings(ctx, tx)
		out.EndpointURL = s.discordEndpointURL()
		return out, err
	})
}

// ---- linking (web side) ----

func (s *Store) DiscordLink(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "view_discord_link", "", false, false, func(tx *sql.Tx) (any, error) {
		settings, err := s.discordSettings(ctx, tx)
		if err != nil {
			return nil, err
		}
		out := DiscordLinkStatus{Enabled: settings.Enabled}
		var linked time.Time
		err = tx.QueryRowContext(ctx, `SELECT created_at FROM discord_links WHERE user_id=$1`, actor.User.ID).Scan(&linked)
		if err == nil {
			out.Linked, out.LinkedAt = true, linked.UTC().Format(time.RFC3339)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return out, nil
	})
}

// CreateDiscordLinkCode makes a single-use code for the signed-in person and
// replaces any earlier unused one. The code is shown once and stored hashed.
func (s *Store) CreateDiscordLinkCode(ctx context.Context, actor Identity) (any, error) {
	if actor.KeyID != "" {
		return nil, problem("FORBIDDEN", "Link codes can only be created in the web console.")
	}
	if err := s.RateLimit(ctx, "discord-code:"+actor.User.ID, 5, 600); err != nil {
		return nil, err
	}
	return s.Perform(ctx, actor, "discord_link_code", "", false, false, func(tx *sql.Tx) (any, error) {
		settings, err := s.discordSettings(ctx, tx)
		if err != nil {
			return nil, err
		}
		if !settings.Enabled {
			return nil, problem("CONFLICT", "Discord is not turned on for this server.")
		}
		code, err := newLinkCode()
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM discord_link_codes WHERE user_id=$1 OR expires_at<now()`, actor.User.ID); err != nil {
			return nil, err
		}
		var expires time.Time
		if err := tx.QueryRowContext(ctx, `INSERT INTO discord_link_codes(code_hash,user_id,expires_at) VALUES($1,$2,clock_timestamp()+make_interval(secs=>$3))
			RETURNING expires_at`, linkCodeHash(code), actor.User.ID, int(discordLinkCodeTTL.Seconds())).Scan(&expires); err != nil {
			return nil, err
		}
		return DiscordLinkCode{Code: displayLinkCode(code), ExpiresAt: expires.UTC().Format(time.RFC3339)}, nil
	})
}

func (s *Store) UnlinkDiscord(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "discord_unlink", "", false, false, func(tx *sql.Tx) (any, error) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM discord_link_codes WHERE user_id=$1`, actor.User.ID); err != nil {
			return nil, err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM discord_links WHERE user_id=$1`, actor.User.ID)
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		return map[string]any{"unlinked": n > 0}, nil
	})
}

// ---- interactions (Discord side) ----

// ReceiveDiscordInteraction verifies one signed request and answers it. An
// error means the request was not accepted (the HTTP status says why); every
// accepted command, including one that fails, is answered with a message.
func (s *Store) ReceiveDiscordInteraction(ctx context.Context, req DiscordRequest) (discordResponse, error) {
	settings, err := s.discordSettings(ctx, s.DB)
	if err != nil {
		return discordResponse{}, err
	}
	if !settings.Enabled || settings.PublicKey == "" {
		return discordResponse{}, problem("NOT_FOUND", "Discord is not turned on.")
	}
	// Nothing in the body is read before the signature checks out.
	if !verifyDiscordSignature(settings.PublicKey, req.Timestamp, req.Signature, req.Body, time.Now()) {
		return discordResponse{}, problem("UNAUTHORIZED", "Invalid request signature.")
	}
	var ix discordInteraction
	if err := json.Unmarshal(req.Body, &ix); err != nil {
		return discordResponse{}, problem("INVALID_INPUT", "The request is not valid JSON.")
	}
	if ix.Type == discordInteractionPing {
		return discordResponse{Type: discordResponsePong}, nil
	}
	if ix.Type != discordInteractionCommand || ix.Data == nil {
		return discordReply("지원하지 않는 요청입니다. `/kb` 명령을 사용하세요."), nil
	}
	if !discordSnowflake.MatchString(ix.ID) {
		return discordResponse{}, problem("INVALID_INPUT", "Missing interaction id.")
	}
	var fresh bool
	if err := s.DB.QueryRowContext(ctx, `WITH gc AS (DELETE FROM discord_interactions WHERE received_at<now()-interval '1 day'),
		ins AS (INSERT INTO discord_interactions(interaction_id) VALUES($1) ON CONFLICT DO NOTHING RETURNING 1)
		SELECT EXISTS(SELECT 1 FROM ins)`, ix.ID).Scan(&fresh); err != nil {
		return discordResponse{}, err
	}
	if !fresh {
		return discordResponse{}, problem("CONFLICT", "This interaction was already handled.")
	}
	user := ix.userID()
	if !discordSnowflake.MatchString(user) {
		return discordReply("요청한 사용자를 확인하지 못했습니다."), nil
	}
	if err := s.RateLimit(ctx, "discord-cmd:"+user, 30, 60); err != nil {
		return discordReply("요청이 너무 많습니다. 잠시 후 다시 시도하세요."), nil
	}
	text, err := s.runDiscordCommand(ctx, ix, user)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code != "INTERNAL_ERROR" {
			return discordReply(PublicError(err)), nil
		}
		slog.Error("discord command failed", "code", errorCode(err))
		return discordReply("처리하지 못했습니다. 잠시 후 다시 시도하세요."), nil
	}
	return discordReply(text), nil
}

func (s *Store) runDiscordCommand(ctx context.Context, ix discordInteraction, discordUser string) (string, error) {
	if ix.Data.Type == discordCommandTypeMessage {
		if ix.Data.Name != discordMessageCommandName {
			return "알 수 없는 메시지 명령입니다.", nil
		}
		return s.discordIssueCommand(ctx, ix, discordUser)
	}
	if ix.Data.Name != "kb" || len(ix.Data.Options) == 0 {
		return "`/kb link`, `/kb unlink`, `/kb projects`, `/kb todos` 중에서 고르세요.", nil
	}
	sub := ix.Data.Options[0]
	switch sub.Name {
	case "link":
		return s.discordLinkCommand(ctx, discordUser, discordString(sub.Options, "code"), ix.GuildID)
	case "unlink":
		return s.discordUnlinkCommand(ctx, discordUser)
	case "projects":
		return s.discordProjectsCommand(ctx, discordUser)
	case "todos":
		return s.discordTodosCommand(ctx, discordUser, sub.Options)
	}
	return "알 수 없는 명령입니다. `/kb link`, `unlink`, `projects`, `todos`를 사용하세요.", nil
}

func (s *Store) discordLinkCommand(ctx context.Context, discordUser, raw, guild string) (string, error) {
	code := normalizeLinkCode(raw)
	if !validLinkCode(code) {
		return "", problem("INVALID_INPUT", "연결 코드 형식이 맞지 않습니다. 웹 '내 계정'에서 만든 코드(예: ABCD-EFGH)를 입력하세요.")
	}
	if err := s.RateLimit(ctx, "discord-link:"+discordUser, 5, 600); err != nil {
		return "", problem("RATE_LIMITED", "연결 시도가 너무 많습니다. 10분 뒤 다시 시도하세요.")
	}
	if err := s.RateLimit(ctx, "discord-link-all", 120, 600); err != nil {
		return "", problem("RATE_LIMITED", "연결 시도가 너무 많습니다. 잠시 후 다시 시도하세요.")
	}
	var name string
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		var userID string
		err := tx.QueryRowContext(ctx, `DELETE FROM discord_link_codes WHERE code_hash=$1 AND expires_at>now() RETURNING user_id`, linkCodeHash(code)).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return problem("INVALID_INPUT", "코드가 없거나 만료되었습니다. 웹 '내 계정'에서 새 코드를 만드세요.")
		}
		if err != nil {
			return err
		}
		var disabled bool
		if err := tx.QueryRowContext(ctx, `SELECT disabled,name FROM users WHERE id=$1 FOR SHARE`, userID).Scan(&disabled, &name); err != nil {
			return err
		}
		if disabled {
			return problem("UNAUTHORIZED", "비활성화된 계정입니다.")
		}
		var other string
		err = tx.QueryRowContext(ctx, `SELECT user_id::text FROM discord_links WHERE discord_user_id=$1 FOR UPDATE`, discordUser).Scan(&other)
		if err == nil && other != userID {
			return problem("CONFLICT", "이 Discord 계정은 이미 다른 사용자에 연결돼 있습니다. `/kb unlink`로 해제한 뒤 다시 연결하세요.")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT discord_user_id FROM discord_links WHERE user_id=$1 FOR UPDATE`, userID).Scan(&existing)
		if err == nil && existing != discordUser {
			return problem("CONFLICT", "이 계정은 이미 다른 Discord 계정과 연결돼 있습니다. 웹 '내 계정'에서 먼저 해제하세요.")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO discord_links(discord_user_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, discordUser, userID); err != nil {
			return err
		}
		return audit(ctx, tx, Event{ActorID: userID, Action: "discord_link", TargetID: discordUser, Outcome: "success", RequestID: uuid.NewString(),
			Details: map[string]any{"guild_id": guild}})
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("**%s** 계정과 연결했습니다. `/kb projects`, `/kb todos`를 사용해 보세요.", name), nil
}

func (s *Store) discordUnlinkCommand(ctx context.Context, discordUser string) (string, error) {
	var userID string
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `DELETE FROM discord_links WHERE discord_user_id=$1 RETURNING user_id::text`, discordUser).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, Event{ActorID: userID, Action: "discord_unlink", TargetID: discordUser, Outcome: "success", RequestID: uuid.NewString()})
	})
	if err != nil {
		return "", err
	}
	if userID == "" {
		return "연결된 계정이 없습니다.", nil
	}
	return "연결을 해제했습니다.", nil
}

// discordPerson is the linked account, refused when it was disabled.
func discordPerson(ctx context.Context, q Querier, discordUser string) (User, error) {
	u, err := one[User](ctx, q, `SELECT jsonb_build_object('id',u.id,'name',u.name,'email',u.email,'global_role',u.global_role,'disabled',u.disabled)
		FROM discord_links l JOIN users u ON u.id=l.user_id WHERE l.discord_user_id=$1`, discordUser)
	if errors.Is(err, sql.ErrNoRows) {
		return u, problem("UNAUTHORIZED", "연결된 계정이 없습니다. 웹 '내 계정'에서 연결 코드를 만들고 `/kb link`로 입력하세요.")
	}
	if err != nil {
		return u, err
	}
	if u.Disabled {
		return u, problem("UNAUTHORIZED", "비활성화된 계정입니다.")
	}
	return u, nil
}

type discordProjectRow struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

const discordProjectSelect = `SELECT jsonb_build_object('id',p.id,'slug',p.slug,'name',p.name,'role',m.role)
	FROM projects p JOIN project_members m ON m.project_id=p.id WHERE m.user_id=$1`

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func (s *Store) discordProjectsCommand(ctx context.Context, discordUser string) (string, error) {
	var out string
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		u, err := discordPerson(ctx, tx, discordUser)
		if err != nil {
			return err
		}
		rows, err := many[discordProjectRow](ctx, tx, discordProjectSelect+` ORDER BY p.name LIMIT $2`, u.ID, discordProjectLines+1)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			out = "참여 중인 프로젝트가 없습니다."
		} else {
			var b strings.Builder
			b.WriteString("**참여 중인 프로젝트**\n")
			for i, r := range rows {
				if i == discordProjectLines {
					b.WriteString("…더 있습니다. 웹에서 확인하세요.")
					break
				}
				fmt.Fprintf(&b, "• `%s` %s (%s)\n", r.Slug, oneLine(r.Name), roleLabel(r.Role))
			}
			out = b.String()
		}
		return audit(ctx, tx, Event{ActorID: u.ID, Action: "discord_projects", Outcome: "success", RequestID: uuid.NewString()})
	})
	return out, err
}

func roleLabel(role string) string {
	switch role {
	case "owner":
		return "소유자"
	case "admin":
		return "관리자"
	case "editor":
		return "편집자"
	case "viewer":
		return "조회자"
	}
	return role
}

func (s *Store) discordTodosCommand(ctx context.Context, discordUser string, opts []discordOption) (string, error) {
	slug := strings.ToLower(discordString(opts, "project"))
	status := discordString(opts, "status")
	mine := discordBool(opts, "mine")
	if slug != "" && !discordSlug.MatchString(slug) {
		return "", problem("INVALID_INPUT", "프로젝트는 slug로 입력하세요. `/kb projects`에서 확인할 수 있습니다.")
	}
	var out string
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		u, err := discordPerson(ctx, tx, discordUser)
		if err != nil {
			return err
		}
		projects, err := many[discordProjectRow](ctx, tx, discordProjectSelect+` ORDER BY p.name LIMIT 50`, u.ID)
		if err != nil {
			return err
		}
		var project *discordProjectRow
		switch {
		case slug != "":
			for i := range projects {
				if projects[i].Slug == slug {
					project = &projects[i]
				}
			}
			if project == nil {
				return problem("NOT_FOUND", "참여 중인 프로젝트에서 찾지 못했습니다. `/kb projects`로 slug를 확인하세요.")
			}
		case len(projects) == 1:
			project = &projects[0]
		case len(projects) == 0:
			return problem("NOT_FOUND", "참여 중인 프로젝트가 없습니다.")
		default:
			return problem("INVALID_INPUT", "참여 중인 프로젝트가 여러 개입니다. `project` 옵션에 slug를 입력하세요. `/kb projects`에서 확인할 수 있습니다.")
		}
		res, err := listTodos(ctx, tx, Identity{User: u}, ListTodosInput{ProjectID: project.ID, Status: status, Mine: mine})
		if err != nil {
			return err
		}
		todos, _ := res.([]Todo)
		out = formatDiscordTodos(project.Name, todos, status, mine)
		return audit(ctx, tx, Event{ActorID: u.ID, Action: "discord_todos", ProjectID: project.ID, Outcome: "success", RequestID: uuid.NewString(),
			Details: map[string]any{"status": status, "mine": mine, "count": len(todos)}})
	})
	return out, err
}

// formatDiscordTodos lists titles, status, priority, assignee and due date.
// Without a status filter, finished to-dos are left out.
func formatDiscordTodos(project string, todos []Todo, status string, mine bool) string {
	shown := make([]Todo, 0, len(todos))
	for _, t := range todos {
		if status == "" && t.Status == "completed" {
			continue
		}
		shown = append(shown, t)
	}
	scope := "완료 제외"
	if status != "" {
		scope = discordStatusLabel(status)
	}
	if mine {
		scope += " · 내 담당"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** 할 일 %d건 (%s)\n", oneLine(project), len(shown), scope)
	if len(shown) == 0 {
		b.WriteString("표시할 할 일이 없습니다.")
		return b.String()
	}
	for i, t := range shown {
		if i == discordTodoLines {
			fmt.Fprintf(&b, "…외 %d건. 웹 할 일 보드에서 확인하세요.", len(shown)-discordTodoLines)
			break
		}
		fmt.Fprintf(&b, "• [%s · %s] %s", discordStatusLabel(t.Status), discordPriorityLabel(t.Priority), oneLine(t.Title))
		if t.AssigneeName != "" {
			fmt.Fprintf(&b, " — %s", oneLine(t.AssigneeName))
		}
		if t.DueDate != "" {
			fmt.Fprintf(&b, " · 마감 %s", t.DueDate)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func discordStatusLabel(s string) string {
	if v, ok := discordStatusLabels[s]; ok {
		return v
	}
	return s
}
func discordPriorityLabel(s string) string {
	if v, ok := discordPriorityLabels[s]; ok {
		return v
	}
	return s
}

// ---- HTTP ----

// discordInteractions is public: the request is trusted only after its
// Ed25519 signature matches the saved application public key. The response is
// Discord's own JSON, not the console's {"data": …} envelope.
func (a *App) discordInteractions(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.RateLimit(r.Context(), "discord-ip:"+a.clientIP(r), 120, 60); err != nil {
		a.fail(w, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, discordMaxBody))
	if err != nil {
		a.fail(w, problem("INVALID_INPUT", "The request body is too large."))
		return
	}
	out, err := a.Store.ReceiveDiscordInteraction(r.Context(), DiscordRequest{
		Signature: r.Header.Get("X-Signature-Ed25519"), Timestamp: r.Header.Get("X-Signature-Timestamp"), Body: body})
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(out)
}
