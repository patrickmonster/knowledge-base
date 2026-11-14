package kb

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue notifications (이슈 담당자 알림).
//
// Issue changes write outbox rows in the same transaction, so a notification
// exists exactly when the change committed. A background worker delivers them
// to project channels (webhook, Slack, email) with retries. Payloads carry
// identifiers, titles and names only — never issue bodies or answers.

const (
	eventIssueAssigned  = "issue_assigned"
	eventIssueAnswered  = "issue_answered"
	eventIssueCommented = "issue_commented"
	eventIssueStatus    = "issue_status"
	eventTest           = "test"
)

var notificationEvents = []string{eventIssueAssigned, eventIssueAnswered, eventIssueCommented, eventIssueStatus}

// maxNotificationAttempts bounds retries; backoff doubles from one minute.
const maxNotificationAttempts = 6

type NotificationChannel struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"project_id"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Target      string   `json:"target,omitempty"`
	TargetHint  string   `json:"target_hint"`
	Events      []string `json:"events"`
	Enabled     bool     `json:"enabled"`
	CreatedBy   string   `json:"created_by"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	LastStatus  *string  `json:"last_status"`
	LastError   *string  `json:"last_error"`
	LastEventAt *string  `json:"last_event_at"`
	Pending     int      `json:"pending"`
}

type NotificationChannelInput struct {
	ProjectID string   `json:"project_id"`
	ChannelID string   `json:"channel_id,omitempty" jsonschema:"Existing channel to update or remove; omit to create"`
	Kind      string   `json:"kind,omitempty" jsonschema:"webhook, slack, or email"`
	Name      string   `json:"name,omitempty"`
	Target    string   `json:"target,omitempty" jsonschema:"HTTPS URL for webhook/slack; empty keeps the current URL when updating; unused for email"`
	Events    []string `json:"events,omitempty" jsonschema:"issue_assigned, issue_answered, issue_commented, issue_status; empty means all"`
	Enabled   *bool    `json:"enabled,omitempty"`
	Remove    bool     `json:"remove,omitempty"`
}

type ChannelRef struct {
	ProjectID string `json:"project_id"`
	ChannelID string `json:"channel_id"`
}

type notificationRecipient struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// notificationPayload is stored in the outbox and rendered at delivery time.
type notificationPayload struct {
	Event       string                  `json:"event"`
	ProjectID   string                  `json:"project_id"`
	ProjectName string                  `json:"project_name"`
	IssueID     string                  `json:"issue_id,omitempty"`
	IssueTitle  string                  `json:"issue_title,omitempty"`
	IssueKind   string                  `json:"issue_kind,omitempty"`
	IssueStatus string                  `json:"issue_status,omitempty"`
	Blocking    bool                    `json:"blocking,omitempty"`
	AreaName    string                  `json:"area_name,omitempty"`
	ActorName   string                  `json:"actor_name"`
	Recipients  []notificationRecipient `json:"recipients"`
	CreatedAt   string                  `json:"created_at"`
	URL         string                  `json:"url,omitempty"`
}

func registerNotificationTools(t Tools, s *Store) {
	add(t, s, "list_notification_channels", "Web console only: list a project's notification channels (targets masked) with recent delivery status.", false, func(in ProjectInput) string { return in.ProjectID }, listNotificationChannels)
	add(t, s, "set_notification_channel", "Web console only, project owner: create, update or remove a webhook, Slack or email notification channel for issue events.", true, func(in NotificationChannelInput) string { return in.ProjectID }, setNotificationChannel)
	add(t, s, "test_notification_channel", "Web console only, project owner: queue a test notification on a channel.", true, func(in ChannelRef) string { return in.ProjectID }, testNotificationChannel)
	// Outbound targets are configured by people, never by an AI client's key.
	for _, name := range []string{"list_notification_channels", "set_notification_channel", "test_notification_channel"} {
		tool := t[name]
		tool.Register = func(*mcp.Server, Identity) {}
		t[name] = tool
	}
}

const channelSelect = `SELECT (to_jsonb(c)-'target')||jsonb_build_object('target_hint',c.target,
	'last_status',(SELECT o.status FROM notification_outbox o WHERE o.channel_id=c.id ORDER BY o.created_at DESC LIMIT 1),
	'last_error',(SELECT CASE WHEN o.last_error LIKE 'lease:%' THEN NULL ELSE o.last_error END FROM notification_outbox o WHERE o.channel_id=c.id ORDER BY o.created_at DESC LIMIT 1),
	'last_event_at',(SELECT o.created_at FROM notification_outbox o WHERE o.channel_id=c.id ORDER BY o.created_at DESC LIMIT 1),
	'pending',(SELECT count(*) FROM notification_outbox o WHERE o.channel_id=c.id AND o.status IN ('pending','processing')))
	FROM notification_channels c`

func webOnly(a Identity) error {
	if a.KeyID != "" {
		return problem("FORBIDDEN", "Notification settings require a signed-in person in the web console.")
	}
	return nil
}

func listNotificationChannels(ctx context.Context, tx *sql.Tx, a Identity, in ProjectInput) (any, error) {
	if err := webOnly(a); err != nil {
		return nil, err
	}
	if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false); err != nil {
		return nil, err
	}
	items, err := many[NotificationChannel](ctx, tx, channelSelect+` WHERE c.project_id=$1 ORDER BY c.created_at,c.id LIMIT 50`, in.ProjectID)
	for i := range items {
		items[i].TargetHint = maskTarget(items[i].TargetHint)
	}
	return items, err
}

// maskTarget keeps the scheme and host of a URL so people can recognise it;
// webhook paths often carry the secret.
func maskTarget(target string) string {
	if target == "" {
		return ""
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return "***"
	}
	tail := ""
	if len(u.Path) > 4 {
		tail = u.Path[len(u.Path)-4:]
	}
	return u.Scheme + "://" + u.Host + "/…" + tail
}

func validateChannelTarget(kind, target string) error {
	if kind == "email" {
		if target != "" {
			return problem("INVALID_INPUT", "Email channels send to each recipient's account address; leave target empty.")
		}
		return nil
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || len(target) > 2000 {
		return problem("INVALID_INPUT", "target must be an HTTPS URL without credentials, up to 2000 bytes.")
	}
	if kind == "slack" && u.Hostname() != "hooks.slack.com" {
		return problem("INVALID_INPUT", "Slack channels need an incoming webhook URL on hooks.slack.com.")
	}
	return nil
}

func normalizeEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		return append([]string{}, notificationEvents...), nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, e := range events {
		valid := false
		for _, known := range notificationEvents {
			valid = valid || e == known
		}
		if !valid {
			return nil, problem("INVALID_INPUT", "events must be issue_assigned, issue_answered, issue_commented, or issue_status.")
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out, nil
}

func requireProjectOwner(ctx context.Context, tx *sql.Tx, a Identity, projectID string) error {
	p, err := projectAccess(ctx, tx, a.User.ID, projectID, true)
	if err != nil {
		return err
	}
	if p.OwnerID != a.User.ID {
		return problem("FORBIDDEN", "Only the project owner can manage notification channels.")
	}
	return nil
}

func setNotificationChannel(ctx context.Context, tx *sql.Tx, a Identity, in NotificationChannelInput) (any, error) {
	if err := webOnly(a); err != nil {
		return nil, err
	}
	if err := requireProjectOwner(ctx, tx, a, in.ProjectID); err != nil {
		return nil, err
	}
	in.Target = strings.TrimSpace(in.Target)
	in.Name = strings.TrimSpace(in.Name)
	if in.ChannelID != "" {
		if err := validID(in.ChannelID); err != nil {
			return nil, err
		}
		current, err := one[NotificationChannel](ctx, tx, `SELECT to_jsonb(c) FROM notification_channels c WHERE id=$1 AND project_id=$2 FOR UPDATE`, in.ChannelID, in.ProjectID)
		if err != nil {
			return nil, notFound(err)
		}
		if in.Remove {
			if _, err := tx.ExecContext(ctx, `DELETE FROM notification_channels WHERE id=$1`, in.ChannelID); err != nil {
				return nil, err
			}
			return map[string]any{"id": in.ChannelID, "removed": true}, nil
		}
		if in.Kind != "" && in.Kind != current.Kind {
			return nil, problem("INVALID_INPUT", "A channel's kind cannot change; remove it and create a new one.")
		}
		if in.Name == "" {
			in.Name = current.Name
		}
		if in.Target == "" {
			in.Target = current.Target
		}
		if in.Events == nil {
			in.Events = current.Events
		}
		if in.Enabled == nil {
			in.Enabled = &current.Enabled
		}
		in.Kind = current.Kind
	} else {
		if in.Remove {
			return nil, problem("INVALID_INPUT", "channel_id is required to remove a channel.")
		}
		if in.Kind != "webhook" && in.Kind != "slack" && in.Kind != "email" {
			return nil, problem("INVALID_INPUT", "kind must be webhook, slack, or email.")
		}
		if in.Enabled == nil {
			enabled := true
			in.Enabled = &enabled
		}
	}
	if err := bounded("name", in.Name, 1, 100); err != nil {
		return nil, err
	}
	if err := validateChannelTarget(in.Kind, in.Target); err != nil {
		return nil, err
	}
	events, err := normalizeEvents(in.Events)
	if err != nil {
		return nil, err
	}
	id := in.ChannelID
	if id == "" {
		id = uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO notification_channels(id,project_id,kind,name,target,events,enabled,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, in.ProjectID, in.Kind, in.Name, in.Target, events, *in.Enabled, a.User.ID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE notification_channels SET name=$2,target=$3,events=$4,enabled=$5,updated_at=clock_timestamp() WHERE id=$1`,
			id, in.Name, in.Target, events, *in.Enabled)
		if err == nil && !*in.Enabled {
			// Turning a channel off also drops what it had waiting; rows already
			// being sent are stopped by the worker's lease renewal check.
			_, err = tx.ExecContext(ctx, `UPDATE notification_outbox SET status='failed',last_error='CHANNEL_DISABLED',next_attempt_at=now()
				WHERE channel_id=$1 AND status='pending'`, id)
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := one[NotificationChannel](ctx, tx, channelSelect+` WHERE c.id=$1`, id)
	out.TargetHint = maskTarget(out.TargetHint)
	return out, err
}

func testNotificationChannel(ctx context.Context, tx *sql.Tx, a Identity, in ChannelRef) (any, error) {
	if err := webOnly(a); err != nil {
		return nil, err
	}
	if err := requireProjectOwner(ctx, tx, a, in.ProjectID); err != nil {
		return nil, err
	}
	if err := validID(in.ChannelID); err != nil {
		return nil, err
	}
	var projectName string
	if err := tx.QueryRowContext(ctx, `SELECT p.name FROM notification_channels c JOIN projects p ON p.id=c.project_id WHERE c.id=$1 AND c.project_id=$2`, in.ChannelID, in.ProjectID).Scan(&projectName); err != nil {
		return nil, notFound(err)
	}
	payload := notificationPayload{Event: eventTest, ProjectID: in.ProjectID, ProjectName: projectName, ActorName: a.User.Name,
		Recipients: []notificationRecipient{{ID: a.User.ID, Name: a.User.Name}}, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO notification_outbox(id,project_id,channel_id,event,recipient_id,payload) VALUES($1,$2,$3,$4,$5,$6)`,
		id, in.ProjectID, in.ChannelID, eventTest, a.User.ID, raw)
	return map[string]any{"id": id, "queued": true}, err
}

// notifyIssue queues event for every enabled project channel subscribed to it.
// Recipients are user IDs; the acting user and empty IDs are skipped, and no
// row is queued when nobody else is affected. Email channels get one row per
// recipient; webhook and Slack channels get one row naming all recipients.
func notifyIssue(ctx context.Context, tx *sql.Tx, actor Identity, issueID, event string, recipients ...*string) error {
	ids := []string{}
	seen := map[string]bool{actor.User.ID: true}
	for _, r := range recipients {
		if r != nil && *r != "" && !seen[*r] {
			seen[*r] = true
			ids = append(ids, *r)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	type channel struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	channels, err := many[channel](ctx, tx, `SELECT jsonb_build_object('id',c.id,'kind',c.kind) FROM notification_channels c
		JOIN issues i ON i.project_id=c.project_id WHERE i.id=$1 AND c.enabled AND $2=ANY(c.events) ORDER BY c.id`, issueID, event)
	if err != nil || len(channels) == 0 {
		return err
	}
	payload, err := one[notificationPayload](ctx, tx, `SELECT jsonb_build_object('project_id',i.project_id,'project_name',p.name,'issue_id',i.id,
		'issue_title',i.title,'issue_kind',i.kind,'issue_status',i.status,'blocking',i.blocking,'area_name',coalesce(a.name,''))
		FROM issues i JOIN projects p ON p.id=i.project_id LEFT JOIN work_areas a ON a.id=i.area_id WHERE i.id=$1`, issueID)
	if err != nil {
		return err
	}
	// Only current project members are notified: someone removed from the
	// project may still be an issue's asker or assignee.
	payload.Recipients, err = many[notificationRecipient](ctx, tx, `SELECT jsonb_build_object('id',u.id,'name',u.name) FROM users u
		WHERE u.id::text=ANY($1::text[]) AND NOT u.disabled
		AND EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=$2::uuid AND m.user_id=u.id) ORDER BY u.name,u.id`, ids, payload.ProjectID)
	if err != nil || len(payload.Recipients) == 0 {
		return err
	}
	payload.Event = event
	payload.ActorName = actor.User.Name
	payload.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	for _, c := range channels {
		targets := []any{nil}
		if c.Kind == "email" {
			targets = targets[:0]
			for _, r := range payload.Recipients {
				targets = append(targets, r.ID)
			}
		}
		for _, recipient := range targets {
			if _, err := tx.ExecContext(ctx, `INSERT INTO notification_outbox(id,project_id,channel_id,event,issue_id,recipient_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7)`,
				uuid.NewString(), payload.ProjectID, c.ID, event, issueID, recipient, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- delivery -----------------------------------------------------------------

// SMTPConfig enables email channels. Without it email rows fail with
// SMTP_NOT_CONFIGURED and other channels still deliver.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Notifier delivers outbox rows. sendMail is replaced in tests.
type Notifier struct {
	HTTP     *http.Client
	SMTP     *SMTPConfig
	sendMail func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

func NewNotifier(smtpConfig *SMTPConfig) *Notifier {
	return &Notifier{HTTP: safeHTTPClient(), SMTP: smtpConfig, sendMail: sendMailTimeout}
}

// smtpTimeout bounds one whole SMTP conversation; tests shorten it.
var smtpTimeout = 30 * time.Second

// sendMailTimeout is smtp.SendMail with a dial timeout and a deadline on the
// whole conversation, so a stalled SMTP server cannot block the worker or
// shutdown. STARTTLS is used when offered, like smtp.SendMail.
func sendMailTimeout(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(smtpTimeout)); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server does not support AUTH")
		}
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

var errBlockedAddress = errors.New("blocked address")

// publicAddress refuses loopback, private, link-local, CGNAT and other
// non-public destinations so channel URLs cannot reach internal services.
func publicAddress(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1]&0xc0 == 64 { // 100.64.0.0/10
			return false
		}
		if v4[0] == 0 || v4[0] >= 240 {
			return false
		}
	}
	return true
}

// safeHTTPClient checks each resolved address at dial time (covering DNS
// rebinding), ignores proxies and never follows redirects.
func safeHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !publicAddress(net.ParseIP(host)) {
			return errBlockedAddress
		}
		return nil
	}}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 16 << 10, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type outboxJob struct {
	ID             string              `json:"id"`
	Event          string              `json:"event"`
	Attempts       int                 `json:"attempts"`
	Kind           string              `json:"kind"`
	Target         string              `json:"target"`
	Enabled        bool                `json:"enabled"`
	RecipientEmail string              `json:"recipient_email"`
	Payload        notificationPayload `json:"payload"`
}

// deliveryError carries a short public code; details such as URLs or SMTP
// replies are never stored because they may contain secrets.
type deliveryError struct {
	Code      string
	Permanent bool
}

func (e *deliveryError) Error() string { return e.Code }

// notificationLease is how long a claimed row stays with one worker. Each
// send renews it first and is bounded well below it (HTTP 15s, SMTP 30s), so
// a slow batch never lets another worker take and resend the same row.
const notificationLease = "2 minutes"

// ProcessNotificationBatch claims due outbox rows, delivers them one by one
// outside the claim transaction and records each result. It returns how many
// rows it claimed.
func (s *Store) ProcessNotificationBatch(ctx context.Context) (int, error) {
	if s.Notifier == nil {
		return 0, nil
	}
	lease := "lease:" + uuid.NewString()
	var jobs []outboxJob
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		var err error
		jobs, err = many[outboxJob](ctx, tx, `WITH candidates AS (
			SELECT id FROM notification_outbox WHERE (status='pending' OR (status='processing' AND lease_until<now())) AND next_attempt_at<=now()
			ORDER BY next_attempt_at,created_at,id LIMIT 10 FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE notification_outbox o SET status='processing',attempts=o.attempts+1,lease_until=now()+$2::interval,last_error=$1
			FROM candidates WHERE o.id=candidates.id RETURNING o.*
		) SELECT jsonb_build_object('id',o.id,'event',o.event,'attempts',o.attempts,'kind',c.kind,'target',c.target,'enabled',c.enabled,
			'recipient_email',coalesce(u.email,''),'payload',o.payload)
		FROM claimed o JOIN notification_channels c ON c.id=o.channel_id
		LEFT JOIN users u ON u.id=o.recipient_id AND NOT u.disabled
			AND EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=o.project_id AND m.user_id=u.id)
		ORDER BY o.next_attempt_at,o.created_at,o.id`, lease, notificationLease)
		return err
	})
	if err != nil || len(jobs) == 0 {
		return 0, err
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			// Unsent rows keep their lease and are retried after it expires.
			return len(jobs), ctx.Err()
		}
		// Renew the lease just before sending and re-read the channel switch.
		// No row means another worker took it over after our lease expired.
		var enabled bool
		err := s.DB.QueryRowContext(ctx, `UPDATE notification_outbox o SET lease_until=now()+$3::interval FROM notification_channels c
			WHERE o.id=$1 AND o.status='processing' AND o.last_error=$2 AND c.id=o.channel_id RETURNING c.enabled`, job.ID, lease, notificationLease).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return len(jobs), err
		}
		var sendErr error
		if !enabled {
			sendErr = &deliveryError{Code: "CHANNEL_DISABLED", Permanent: true}
		} else {
			job.Payload.URL = s.PublicURL + "/#issues"
			sendErr = s.Notifier.deliver(ctx, job)
		}
		status, code, next := "sent", "", time.Duration(0)
		if sendErr != nil {
			var de *deliveryError
			code = "DELIVERY_FAILED"
			permanent := false
			if errors.As(sendErr, &de) {
				code, permanent = de.Code, de.Permanent
			}
			status = "pending"
			next = time.Minute << min(job.Attempts-1, 6)
			if permanent || job.Attempts >= maxNotificationAttempts {
				status = "failed"
			}
		}
		// Record the outcome even if the batch was cancelled mid-send, so a
		// delivered message is not sent again.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, err = s.DB.ExecContext(saveCtx, `UPDATE notification_outbox SET status=$2,last_error=NULLIF($3,''),lease_until=NULL,
			next_attempt_at=now()+make_interval(secs=>$4),sent_at=CASE WHEN $2='sent' THEN now() ELSE sent_at END
			WHERE id=$1 AND last_error=$5`, job.ID, status, code, next.Seconds(), lease)
		cancel()
		if err != nil {
			return len(jobs), err
		}
	}
	return len(jobs), nil
}

func (s *Store) RunNotificationWorker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var cleaned time.Time
	for {
		for {
			batchCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			n, err := s.ProcessNotificationBatch(batchCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					slog.Error("notification batch failed")
				}
				break
			}
			if n == 0 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Since(cleaned) < time.Hour {
			continue
		}
		cleaned = time.Now()
		cleanCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		// Keep a month of delivery history for diagnosis.
		if _, err := s.DB.ExecContext(cleanCtx, `DELETE FROM notification_outbox WHERE status IN ('sent','failed') AND created_at<now()-interval '30 days'`); err != nil && ctx.Err() == nil {
			slog.Error("notification cleanup failed")
		}
		cancel()
	}
}

func (n *Notifier) deliver(ctx context.Context, job outboxJob) error {
	switch job.Kind {
	case "email":
		return n.sendEmail(job)
	case "slack":
		body, _ := json.Marshal(map[string]string{"text": notificationText(job.Payload)})
		return n.post(ctx, job, body)
	case "webhook":
		body, _ := json.Marshal(job.Payload)
		return n.post(ctx, job, body)
	}
	return &deliveryError{Code: "UNKNOWN_CHANNEL", Permanent: true}
}

func (n *Notifier) post(ctx context.Context, job outboxJob, body []byte) error {
	if n.HTTP == nil {
		return &deliveryError{Code: "HTTP_NOT_CONFIGURED", Permanent: true}
	}
	if err := validateChannelTarget(job.Kind, job.Target); err != nil {
		return &deliveryError{Code: "INVALID_TARGET", Permanent: true}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, job.Target, bytes.NewReader(body))
	if err != nil {
		return &deliveryError{Code: "INVALID_TARGET", Permanent: true}
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "knowledge-base-notifier/1")
	req.Header.Set("X-KB-Event", job.Event)
	req.Header.Set("X-KB-Delivery", job.ID)
	resp, err := n.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, errBlockedAddress) {
			return &deliveryError{Code: "BLOCKED_ADDRESS", Permanent: true}
		}
		return &deliveryError{Code: "CONNECTION_FAILED"}
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		return &deliveryError{Code: "HTTP_" + strconv.Itoa(resp.StatusCode)}
	default:
		// 3xx (redirects are not followed) and other 4xx will not fix themselves.
		return &deliveryError{Code: "HTTP_" + strconv.Itoa(resp.StatusCode), Permanent: true}
	}
}

// headerSafe strips characters that could end a mail header line.
func headerSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return ' '
		}
		return r
	}, s)
}

func (n *Notifier) sendEmail(job outboxJob) error {
	if n.SMTP == nil || n.SMTP.Host == "" {
		return &deliveryError{Code: "SMTP_NOT_CONFIGURED", Permanent: true}
	}
	if job.RecipientEmail == "" {
		return &deliveryError{Code: "NO_RECIPIENT_EMAIL", Permanent: true}
	}
	subject := "[" + headerSafe(job.Payload.ProjectName) + "] " + headerSafe(notificationSubject(job.Payload))
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\nMessage-ID: <%s@knowledge-base>\r\n\r\n",
		headerSafe(n.SMTP.From), headerSafe(job.RecipientEmail), mime.BEncoding.Encode("utf-8", subject), job.ID)
	encoded := base64.StdEncoding.EncodeToString([]byte(notificationText(job.Payload)))
	for len(encoded) > 76 {
		msg.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	msg.WriteString(encoded + "\r\n")
	var auth smtp.Auth
	if n.SMTP.Username != "" {
		auth = smtp.PlainAuth("", n.SMTP.Username, n.SMTP.Password, n.SMTP.Host)
	}
	send := n.sendMail
	if send == nil {
		send = sendMailTimeout
	}
	if err := send(net.JoinHostPort(n.SMTP.Host, strconv.Itoa(n.SMTP.Port)), auth, n.SMTP.From, []string{job.RecipientEmail}, msg.Bytes()); err != nil {
		return &deliveryError{Code: "SMTP_FAILED"}
	}
	return nil
}

func notificationSubject(p notificationPayload) string {
	kind := map[string]string{"question": "질의", "issue": "이슈"}[p.IssueKind]
	switch p.Event {
	case eventTest:
		return "알림 채널 테스트"
	case eventIssueAssigned:
		return fmt.Sprintf("%s 담당 배정: %s", kind, p.IssueTitle)
	case eventIssueAnswered:
		return fmt.Sprintf("%s 답변 등록: %s", kind, p.IssueTitle)
	case eventIssueCommented:
		return fmt.Sprintf("%s 댓글: %s", kind, p.IssueTitle)
	case eventIssueStatus:
		return fmt.Sprintf("%s 상태 %s: %s", kind, issueStatusLabel(p.IssueStatus), p.IssueTitle)
	}
	return p.IssueTitle
}

func issueStatusLabel(status string) string {
	if label, ok := map[string]string{"open": "답변 대기", "answered": "답변됨", "resolved": "해결", "closed": "닫힘"}[status]; ok {
		return label
	}
	return status
}

// notificationText is the plain message used by Slack and email.
func notificationText(p notificationPayload) string {
	names := make([]string, len(p.Recipients))
	for i, r := range p.Recipients {
		names[i] = r.Name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n", p.ProjectName, notificationSubject(p))
	if p.Event == eventTest {
		fmt.Fprintf(&b, "%s 님이 알림 채널을 테스트했습니다.\n", p.ActorName)
	} else {
		fmt.Fprintf(&b, "받는 사람: %s\n작업자: %s\n상태: %s", strings.Join(names, ", "), p.ActorName, issueStatusLabel(p.IssueStatus))
		if p.Blocking && p.IssueStatus == "open" {
			b.WriteString(" · 작업 보류")
		}
		if p.AreaName != "" {
			fmt.Fprintf(&b, " · 영역 %s", p.AreaName)
		}
		b.WriteString("\n")
	}
	if p.URL != "" {
		fmt.Fprintf(&b, "%s\n", p.URL)
	}
	if p.IssueID != "" {
		fmt.Fprintf(&b, "issue://%s/%s\n", p.ProjectID, p.IssueID)
	}
	return b.String()
}
