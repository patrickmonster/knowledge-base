package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"knowledge-base/migrations"
)

type Store struct {
	DB       *sql.DB
	Embedder Embedder
	Objects  ObjectStorage
	// Notifier delivers queued issue notifications; nil disables delivery
	// (events are still queued).
	Notifier *Notifier
	// PublicURL is the server origin used to build absolute upload links.
	PublicURL string
	// Retention purges old audit events and revisions; zero keeps them forever.
	Retention RetentionPolicy
	// Search holds the server default search settings; a global administrator
	// can override them in the web console (search_settings).
	Search SearchPolicy
	// AttachmentQuotaBytes caps a project's stored and reserved files; 0 is unlimited.
	AttachmentQuotaBytes int64
	// Scanner, when set, must report a file clean before it is stored (fail closed).
	Scanner MalwareScanner
	// AllowExecutables disables the built-in executable file block.
	AllowExecutables bool
	// Extractor turns uploaded PDF/DOCX/XLSX files into searchable artifact
	// documents; nil disables extraction.
	Extractor TextExtractor
	// MFAKey seals TOTP secrets (ParseMFAKey). Empty disables MFA setup, and
	// sign-in for users who already enabled MFA fails closed.
	MFAKey []byte
	// GitHub reads repository issues for import_github_issues; nil uses the
	// public API without a token.
	GitHub *GitHubClient
	// Deployment is the repository and commit of the running build, used to
	// tell when a task's pull request is deployed.
	Deployment DeploymentTarget
	// HNSWReview holds the server default HNSW review thresholds; zero fields
	// use DefaultHNSWThresholds. Projects may override them.
	HNSWReview HNSWThresholds
}
type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// migrationScripts are applied in order; version N is migrationScripts[N-1].
var migrationScripts = []string{migrations.Initial, migrations.Auth, migrations.Embeddings, migrations.PermanentKeys, migrations.TaskWorkflow, migrations.Attachments, migrations.AttachmentUploadTokens, migrations.NormalizeTags, migrations.DocumentLinks, migrations.Issues, migrations.TeamRollout, migrations.KnowledgeQuality, migrations.ProjectRules, migrations.RuleEnabled, migrations.SearchMisses, migrations.MFA, migrations.AttachmentChunks, migrations.AttachmentText, migrations.IssueSources, migrations.TaskPullRequests, migrations.GitHubWebhooks, migrations.SearchTimings, migrations.IssueAttachments, migrations.IssueAttachmentParts, migrations.Todos, migrations.TodoTaskLink, migrations.IssueTodoLink, migrations.WebhookPlainSecret, migrations.ProjectRepositories, migrations.SearchThresholds, migrations.TodoImport, migrations.TaskScope, migrations.DocumentNotifications, migrations.RecheckDigestRuns, migrations.Discord, migrations.RetentionSettings, migrations.TodoInReview, migrations.DiscordDropGuildIDs, migrations.DiscordIssueChannels, migrations.BackfillUsedImplements, migrations.IssueChunks, migrations.ProjectUpstreams, migrations.TodoRelations, migrations.TaskPrompts, migrations.SearchSettings, migrations.TodoPendingRelease, migrations.DocumentTrigram}

func (s *Store) Migrate(ctx context.Context) error {
	return transaction(ctx, s.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(51283167)"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY)`); err != nil {
			return err
		}
		for i, script := range migrationScripts {
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", i+1).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err := tx.ExecContext(ctx, script); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES($1)", i+1); err != nil {
				return err
			}
		}
		return nil
	})
}

// snapshotKey marks a context whose transaction must read one consistent
// snapshot across several queries (REPEATABLE READ), e.g. a project export.
type snapshotKey struct{}

func withSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, snapshotKey{}, true)
}

func transaction(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	var opts *sql.TxOptions
	if snapshot, _ := ctx.Value(snapshotKey{}).(bool); snapshot {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead}
	}
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='15s'; SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func one[T any](ctx context.Context, q Querier, query string, args ...any) (out T, err error) {
	var raw []byte
	if err = q.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return
}
func many[T any](ctx context.Context, q Querier, query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return problem("NOT_FOUND", "Resource not found or access is unavailable.")
	}
	return err
}
