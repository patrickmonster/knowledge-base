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

// Scheduled recheck digests (재확인 요약 발송).
//
// The notification worker queues the get_recheck_digest result for projects
// that have a channel subscribed to recheck_digest. Rows go through the same
// outbox, retries, Discord formatting and SSRF checks as issue notifications.
// recheck_digest_runs holds one row per project and period: whoever inserts it
// queues the digest in the same transaction, so a period is sent once even
// with several server instances. Nothing is queued when no document is due.

const eventRecheckDigest = "recheck_digest"

const (
	digestItemsPerGroup = 5  // documents listed per owner in the payload
	digestMaxGroups     = 15 // owners listed before "more_groups"
	digestTitleRunes    = 80
	digestTextBudget    = 1800 // keeps the whole message under Discord's 2,000 characters
)

// RecheckDigestSchedule says how often digests are queued. The zero value
// (and an empty Frequency) disables them.
type RecheckDigestSchedule struct {
	Frequency string // "", "daily" or "weekly" (Monday)
	Hour      int    // 0-23, Asia/Seoul: nothing is queued before this hour
}

type digestItem struct {
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	DaysOverdue int    `json:"days_overdue"`
	Citation    string `json:"citation"`
}

// digestGroup is one owner's due documents. Documents whose owner left the
// project or was disabled are grouped without a name (Inactive) and nobody is
// mailed for them.
type digestGroup struct {
	OwnerName string       `json:"owner_name,omitempty"`
	Inactive  bool         `json:"inactive,omitempty"`
	Total     int          `json:"total"`
	Items     []digestItem `json:"items"`
}

type recheckDigestBody struct {
	Total      int           `json:"total"`
	Groups     []digestGroup `json:"groups"`
	MoreGroups int           `json:"more_groups,omitempty"`
}

// periodStart returns the date that identifies the period containing now and
// whether the period's send time has been reached.
func (c RecheckDigestSchedule) periodStart(now time.Time) (string, bool) {
	if c.Frequency != "daily" && c.Frequency != "weekly" {
		return "", false
	}
	n := now.In(seoul)
	start := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, seoul)
	if c.Frequency == "weekly" {
		start = start.AddDate(0, 0, -((int(start.Weekday()) + 6) % 7))
	}
	return start.Format("2006-01-02"), !n.Before(start.Add(time.Duration(c.Hour) * time.Hour))
}

// QueueRecheckDigests queues this period's digest for every project that
// subscribed a channel to it and has not been sent yet. It returns how many
// projects were queued.
func (s *Store) QueueRecheckDigests(ctx context.Context, now time.Time) (int, error) {
	if s.Notifier == nil {
		return 0, nil
	}
	period, due := s.Notifier.Digest.periodStart(now)
	if !due {
		return 0, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT c.project_id FROM notification_channels c
		WHERE c.enabled AND $1=ANY(c.events) AND NOT EXISTS(SELECT 1 FROM recheck_digest_runs r WHERE r.project_id=c.project_id AND r.period=$2::date)`, eventRecheckDigest, period)
	if err != nil {
		return 0, err
	}
	var projects []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		projects = append(projects, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()
	queued := 0
	for _, id := range projects {
		var did bool
		if err := transaction(ctx, s.DB, func(tx *sql.Tx) (err error) {
			did, err = queueRecheckDigest(ctx, tx, id, period, now)
			return err
		}); err != nil {
			return queued, err
		}
		if did {
			queued++
		}
	}
	return queued, nil
}

func queueRecheckDigest(ctx context.Context, tx *sql.Tx, projectID, period string, now time.Time) (bool, error) {
	type channel struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	channels, err := many[channel](ctx, tx, `SELECT jsonb_build_object('id',c.id,'kind',c.kind) FROM notification_channels c
		WHERE c.project_id=$1 AND c.enabled AND $2=ANY(c.events) ORDER BY c.id`, projectID, eventRecheckDigest)
	if err != nil || len(channels) == 0 {
		return false, err
	}
	digest, err := buildRecheckDigest(ctx, tx, RecheckDigestInput{ProjectID: projectID, Limit: digestItemsPerGroup}, now)
	if err != nil {
		return false, err
	}
	total := 0
	for _, g := range digest.ByOwner {
		total += g.Total
	}
	if total == 0 {
		return false, nil
	}
	// The claim: a concurrent instance blocks here until we commit, then
	// finds the row and skips.
	claimed, err := tx.ExecContext(ctx, `INSERT INTO recheck_digest_runs(project_id,period,document_count) VALUES($1,$2::date,$3) ON CONFLICT DO NOTHING`, projectID, period, total)
	if err != nil {
		return false, err
	}
	if n, err := claimed.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	var projectName string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM projects WHERE id=$1`, projectID).Scan(&projectName); err != nil {
		return false, err
	}
	ownerIDs := make([]string, len(digest.ByOwner))
	for i, g := range digest.ByOwner {
		ownerIDs[i] = g.OwnerID
	}
	// Like issue notifications, only current, enabled project members are
	// addressed; documents of anyone else are listed without a name.
	active, err := many[notificationRecipient](ctx, tx, `SELECT jsonb_build_object('id',u.id,'name',u.name) FROM users u
		WHERE u.id::text=ANY($1::text[]) AND NOT u.disabled
		AND EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=$2::uuid AND m.user_id=u.id) ORDER BY u.name,u.id`, ownerIDs, projectID)
	if err != nil {
		return false, err
	}
	isActive := map[string]bool{}
	for _, r := range active {
		isActive[r.ID] = true
	}
	base := notificationPayload{Event: eventRecheckDigest, ProjectID: projectID, ProjectName: projectName, ActorName: "knowledge-base",
		CreatedAt: now.UTC().Format(time.RFC3339)}
	team := base
	team.Recipients = active
	team.Digest = &recheckDigestBody{Total: total}
	inactive := digestGroup{Inactive: true}
	byOwner := map[string]digestGroup{}
	for _, g := range digest.ByOwner {
		group := digestGroup{OwnerName: g.OwnerName, Total: g.Total, Items: digestItems(g.Due)}
		if !isActive[g.OwnerID] {
			inactive.Total += g.Total
			inactive.Items = append(inactive.Items, group.Items...)
			continue
		}
		byOwner[g.OwnerID] = group
		if len(team.Digest.Groups) < digestMaxGroups {
			team.Digest.Groups = append(team.Digest.Groups, group)
		} else {
			team.Digest.MoreGroups++
		}
	}
	if inactive.Total > 0 {
		if len(inactive.Items) > digestItemsPerGroup {
			inactive.Items = inactive.Items[:digestItemsPerGroup]
		}
		team.Digest.Groups = append(team.Digest.Groups, inactive)
	}
	teamRaw, err := json.Marshal(team)
	if err != nil {
		return false, err
	}
	insert := func(channelID string, recipient any, raw []byte) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO notification_outbox(id,project_id,channel_id,event,recipient_id,payload) VALUES($1,$2,$3,$4,$5,$6)`,
			uuid.NewString(), projectID, channelID, eventRecheckDigest, recipient, raw)
		return err
	}
	for _, c := range channels {
		if c.Kind != "email" {
			if err := insert(c.ID, nil, teamRaw); err != nil {
				return false, err
			}
			continue
		}
		// Email: each active owner gets only their own documents.
		for _, r := range active {
			own := base
			own.Recipients = []notificationRecipient{r}
			own.Digest = &recheckDigestBody{Total: byOwner[r.ID].Total, Groups: []digestGroup{byOwner[r.ID]}}
			raw, err := json.Marshal(own)
			if err != nil {
				return false, err
			}
			if err := insert(c.ID, r.ID, raw); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func digestItems(due []RecheckDue) []digestItem {
	items := make([]digestItem, 0, len(due))
	for _, d := range due {
		items = append(items, digestItem{Title: digestTitle(d.Title), Kind: d.Kind, DaysOverdue: d.DaysOverdue, Citation: d.Citation})
	}
	return items
}

// digestTitle keeps a title on one line and neutralizes the angle brackets
// Slack uses for @channel-style mentions. Mentions are already disabled for
// Discord.
func digestTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	title = strings.NewReplacer("<", "‹", ">", "›").Replace(title)
	if r := []rune(title); len(r) > digestTitleRunes {
		title = string(r[:digestTitleRunes-1]) + "…"
	}
	return title
}

// recheckDigestText is the plain message for Slack, Discord and email: titles,
// owners, days overdue and a console link, kept under digestTextBudget runes.
// Document bodies and recheck notes are never included.
func recheckDigestText(p notificationPayload) string {
	d := p.Digest
	if d == nil {
		d = &recheckDigestBody{}
	}
	date := p.CreatedAt
	if t, err := time.Parse(time.RFC3339, p.CreatedAt); err == nil {
		date = t.In(seoul).Format("2006-01-02")
	}
	head := fmt.Sprintf("[%s] %s (%s)\n", p.ProjectName, notificationSubject(p), date)
	foot := ""
	if p.URL != "" {
		foot = "\n확인하기: " + p.URL + "\n"
	}
	budget := digestTextBudget - len([]rune(head)) - len([]rune(foot)) - 60 // room for the omitted-count line
	var b strings.Builder
	used, shown := 0, 0
	add := func(line string) bool {
		n := len([]rune(line))
		if used+n > budget {
			return false
		}
		b.WriteString(line)
		used += n
		return true
	}
	full := true
	for _, g := range d.Groups {
		if !full {
			break
		}
		title := g.OwnerName
		if g.Inactive {
			title = "담당자 확인 필요 (프로젝트에서 제외된 사용자)"
		}
		if !add(fmt.Sprintf("\n%s · %d건\n", title, g.Total)) {
			full = false
			break
		}
		for _, it := range g.Items {
			if !add(fmt.Sprintf("- %s (%s) · 기한 %d일 지남\n", it.Title, it.Kind, it.DaysOverdue)) {
				full = false
				break
			}
			shown++
		}
	}
	text := head + b.String()
	if rest := d.Total - shown; rest > 0 {
		text += fmt.Sprintf("\n외 %d건은 웹 콘솔에서 확인하세요.\n", rest)
	}
	return text + foot
}

func (s *Store) runRecheckDigestSchedule(ctx context.Context) {
	if s.Notifier == nil || s.Notifier.Digest.Frequency == "" {
		return
	}
	queueCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if n, err := s.QueueRecheckDigests(queueCtx, time.Now()); err != nil {
		if ctx.Err() == nil {
			slog.Error("recheck digest queue failed")
		}
	} else if n > 0 {
		slog.Info("recheck digests queued", "projects", n)
	}
}
