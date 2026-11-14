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
func (s *Store) Migrate(ctx context.Context) error {
	return transaction(ctx, s.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(51283167)"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY)`); err != nil {
			return err
		}
		for i, script := range []string{migrations.Initial, migrations.Auth, migrations.Embeddings, migrations.PermanentKeys, migrations.TaskWorkflow, migrations.Attachments} {
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
func transaction(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
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
