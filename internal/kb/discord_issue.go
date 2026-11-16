package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Discord message command "이슈로 등록" (Apps → 이슈로 등록).
//
// A person right-clicks a message, for example the first post of a forum
// thread, and chooses Apps → 이슈로 등록. Discord sends the message with the
// request (no bot, gateway or message-content intent is needed), and the server
// turns it into an issue as the person who linked their Discord account. It uses
// the same signed Interactions Endpoint as the slash commands.
//
// Which project: a global administrator maps a Discord channel (a forum or a
// text channel) to a project in the web console. A message in a forum thread is
// matched by the forum's id, then by the thread's own id. Without a mapping the
// person's only project is used; with several projects the request is refused.
// The person needs editor, admin or owner access to that project. The same
// message is never registered twice.

const (
	discordCommandTypeMessage = 3
	// discordMessageCommandName is the entry shown under Apps (32 characters at most).
	discordMessageCommandName = "이슈로 등록"
	discordIssueTitleLimit    = 200
	discordIssueBodyLimit     = 15000
	discordIssueAttachments   = 10
)

// discordSourceMessage is the message the command was used on, as Discord
// resolves it in the request.
type discordSourceMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
	Author    struct {
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
	} `json:"author"`
	Attachments []struct {
		URL      string `json:"url"`
		Filename string `json:"filename"`
	} `json:"attachments"`
}

// DiscordChannel maps one Discord channel to the project its messages go to.
type DiscordChannel struct {
	ChannelID   string `json:"channel_id"`
	ProjectSlug string `json:"project_slug"`
	ProjectName string `json:"project_name"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// DiscordChannelInput saves a mapping; an empty project_slug removes it.
type DiscordChannelInput struct {
	ChannelID   string `json:"channel_id"`
	ProjectSlug string `json:"project_slug"`
}

func (s *Store) discordChannels(ctx context.Context, q Querier) ([]DiscordChannel, error) {
	return many[DiscordChannel](ctx, q, `SELECT jsonb_build_object('channel_id',c.channel_id,'project_slug',p.slug,
		'project_name',p.name,'updated_at',c.updated_at) FROM discord_channel_projects c JOIN projects p ON p.id=c.project_id
		ORDER BY p.name,c.channel_id LIMIT 200`)
}

// SetDiscordChannel saves or removes one channel → project mapping. Web
// session, global administrator only (Perform rejects MCP keys).
func (s *Store) SetDiscordChannel(ctx context.Context, actor Identity, in DiscordChannelInput) (any, error) {
	in.ChannelID = strings.TrimSpace(in.ChannelID)
	in.ProjectSlug = strings.ToLower(strings.TrimSpace(in.ProjectSlug))
	if !discordSnowflake.MatchString(in.ChannelID) {
		return nil, problem("INVALID_INPUT", "채널 ID는 숫자입니다. Discord 개발자 모드를 켜고 포럼 채널을 우클릭해 'ID 복사'를 누르세요.")
	}
	if in.ProjectSlug != "" && !discordSlug.MatchString(in.ProjectSlug) {
		return nil, problem("INVALID_INPUT", "프로젝트는 slug로 입력하세요. 매핑을 지우려면 비워 두세요.")
	}
	ctx = auditInput(ctx, map[string]any{"channel_id": in.ChannelID, "project_slug": in.ProjectSlug})
	return s.Perform(ctx, actor, "update_discord_channel", "", false, true, func(tx *sql.Tx) (any, error) {
		if in.ProjectSlug == "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM discord_channel_projects WHERE channel_id=$1`, in.ChannelID); err != nil {
				return nil, err
			}
			return s.discordChannels(ctx, tx)
		}
		var project string
		if err := tx.QueryRowContext(ctx, `SELECT id::text FROM projects WHERE slug=$1`, in.ProjectSlug).Scan(&project); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, problem("NOT_FOUND", "그 slug의 프로젝트를 찾지 못했습니다.")
			}
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO discord_channel_projects(channel_id,project_id,updated_by,updated_at)
			VALUES($1,$2,$3,clock_timestamp())
			ON CONFLICT(channel_id) DO UPDATE SET project_id=$2,updated_by=$3,updated_at=clock_timestamp()`,
			in.ChannelID, project, actor.User.ID); err != nil {
			return nil, err
		}
		return s.discordChannels(ctx, tx)
	})
}

// discordIssueTitle prefers the forum thread's title, then the first line of
// the message.
func discordIssueTitle(ix discordInteraction, content string) string {
	title := ""
	if ix.Channel != nil && ix.Channel.Name != "" && ix.Channel.isThread() {
		title = oneLine(ix.Channel.Name)
	}
	if title == "" {
		for _, line := range strings.Split(content, "\n") {
			if line = oneLine(line); line != "" {
				title = line
				break
			}
		}
	}
	if title == "" {
		title = "Discord 메시지"
	}
	return clipRunes(title, discordIssueTitleLimit)
}

// markdownLabel keeps a file name from breaking out of a Markdown link.
func markdownLabel(name string) string {
	name = oneLine(name)
	return strings.NewReplacer("[", "(", "]", ")").Replace(name)
}

func discordMessageLink(guild, channel, message string) string {
	if !discordSnowflake.MatchString(guild) {
		guild = "@me"
	}
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guild, channel, message)
}

// discordIssueBody is the message text followed by where it came from.
func discordIssueBody(ix discordInteraction, msg discordSourceMessage, content string) string {
	var b strings.Builder
	if content != "" {
		b.WriteString(clipRunes(content, discordIssueBodyLimit))
		b.WriteString("\n\n---\n")
	}
	channel := msg.ChannelID
	if channel == "" && ix.Channel != nil {
		channel = ix.Channel.ID
	}
	author := oneLine(msg.Author.GlobalName)
	if author == "" {
		author = oneLine(msg.Author.Username)
	}
	if author != "" {
		fmt.Fprintf(&b, "- 작성자: %s\n", author)
	}
	if discordSnowflake.MatchString(channel) {
		fmt.Fprintf(&b, "- 출처: Discord 메시지 %s\n", discordMessageLink(ix.GuildID, channel, msg.ID))
	}
	shown := 0
	for _, a := range msg.Attachments {
		if !strings.HasPrefix(a.URL, "https://") || shown == discordIssueAttachments {
			continue
		}
		if shown == 0 {
			b.WriteString("- 첨부(Discord 링크, 시간이 지나면 열리지 않을 수 있습니다):\n")
		}
		name := markdownLabel(a.Filename)
		if name == "" {
			name = "첨부 파일"
		}
		fmt.Fprintf(&b, "  - [%s](%s)\n", name, strings.NewReplacer(" ", "%20", ")", "%29", "(", "%28").Replace(a.URL))
		shown++
	}
	return strings.TrimSpace(b.String())
}

// discordIssueProject finds the project for this message: a mapped channel
// first (the forum, then the thread), else the caller's only project.
func discordIssueProject(ctx context.Context, tx *sql.Tx, userID string, channels []string) (string, error) {
	for _, ch := range channels {
		if !discordSnowflake.MatchString(ch) {
			continue
		}
		var project string
		err := tx.QueryRowContext(ctx, `SELECT project_id::text FROM discord_channel_projects WHERE channel_id=$1`, ch).Scan(&project)
		if err == nil {
			return project, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	projects, err := many[discordProjectRow](ctx, tx, discordProjectSelect+` ORDER BY p.name LIMIT 2`, userID)
	if err != nil {
		return "", err
	}
	switch len(projects) {
	case 0:
		return "", problem("NOT_FOUND", "참여 중인 프로젝트가 없습니다.")
	case 1:
		return projects[0].ID, nil
	}
	return "", problem("INVALID_INPUT", "이 채널에 연결된 프로젝트가 없습니다. 관리자가 웹 '운영 현황 → Discord 연동'에서 채널을 프로젝트에 연결해야 합니다.")
}

// discordIssueCommand registers the message as an issue.
func (s *Store) discordIssueCommand(ctx context.Context, ix discordInteraction, discordUser string) (string, error) {
	msg, ok := ix.Data.Resolved.Messages[ix.Data.TargetID]
	if !ok || !discordSnowflake.MatchString(msg.ID) {
		return "", problem("INVALID_INPUT", "대상 메시지를 확인하지 못했습니다.")
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" && len(msg.Attachments) == 0 {
		return "", problem("INVALID_INPUT", "내용이 없는 메시지는 이슈로 등록할 수 없습니다.")
	}
	channels := []string{msg.ChannelID}
	if ix.Channel != nil {
		channels = []string{ix.Channel.ParentID, ix.Channel.ID, msg.ChannelID}
	}
	var out string
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		u, err := discordPerson(ctx, tx, discordUser)
		if err != nil {
			return err
		}
		// Two clicks on the same message at once must not make two issues.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "discord-issue:"+msg.ID); err != nil {
			return err
		}
		var title string
		err = tx.QueryRowContext(ctx, `SELECT i.title FROM discord_issue_messages m JOIN issues i ON i.id=m.issue_id
			WHERE m.message_id=$1`, msg.ID).Scan(&title)
		if err == nil {
			out = "이미 이슈로 등록된 메시지입니다: **" + oneLine(title) + "**"
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		projectID, err := discordIssueProject(ctx, tx, u.ID, channels)
		if err != nil {
			return err
		}
		// createIssue checks the person's own role in the project (not a viewer).
		res, err := createIssue(ctx, tx, Identity{User: u}, CreateIssueInput{
			ProjectID: projectID, Kind: "issue", Title: discordIssueTitle(ix, content),
			Body: discordIssueBody(ix, msg, content), Tags: []string{"discord"},
		})
		if err != nil {
			return err
		}
		issue, _ := res.(Issue)
		if issue.ID == "" {
			return fmt.Errorf("issue was not created")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO discord_issue_messages(message_id,issue_id) VALUES($1,$2)`, msg.ID, issue.ID); err != nil {
			return err
		}
		project, err := projectAccess(ctx, tx, u.ID, projectID, false)
		if err != nil {
			return err
		}
		out = fmt.Sprintf("이슈로 등록했습니다: **%s** (프로젝트 %s)", oneLine(issue.Title), oneLine(project.Name))
		return audit(ctx, tx, Event{ActorID: u.ID, Action: "discord_issue", ProjectID: projectID, TargetID: issue.ID, Outcome: "success",
			RequestID: uuid.NewString(), Details: map[string]any{"message_id": msg.ID}})
	})
	return out, err
}
