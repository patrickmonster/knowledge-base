package kb

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"runtime/debug"
	"time"
)

// BuildCommit is set at build time with
// -ldflags "-X knowledge-base/internal/kb.BuildCommit=<sha>".
var BuildCommit string

var processStarted = time.Now().UTC()

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// ServerVersion identifies the running build and the database schema it sees.
// It contains no secrets: commit hash, schema numbers and process start time.
type ServerVersion struct {
	Commit         string `json:"commit"`
	CommitSource   string `json:"commit_source"`
	SchemaVersion  int    `json:"schema_version"`
	SchemaExpected int    `json:"schema_expected"`
	SchemaCurrent  bool   `json:"schema_current"`
	StartedAt      string `json:"started_at"`
}

// buildCommit resolves the deployed commit: build flag first, then the
// Railway runtime variable, then Go VCS stamping (local builds).
func buildCommit() (string, string) {
	if c := BuildCommit; commitPattern.MatchString(c) {
		return c, "build"
	}
	if c := os.Getenv("RAILWAY_GIT_COMMIT_SHA"); commitPattern.MatchString(c) {
		return c, "railway"
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		rev, dirty := "", false
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if commitPattern.MatchString(rev) {
			if dirty {
				return rev, "vcs-modified"
			}
			return rev, "vcs"
		}
	}
	return "unknown", "unknown"
}

func serverVersion(ctx context.Context, q Querier) (ServerVersion, error) {
	commit, source := buildCommit()
	out := ServerVersion{Commit: commit, CommitSource: source, SchemaExpected: len(migrationScripts), StartedAt: processStarted.Format(time.RFC3339)}
	var v sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v); err != nil {
		return ServerVersion{}, err
	}
	out.SchemaVersion = int(v.Int64)
	out.SchemaCurrent = out.SchemaVersion == out.SchemaExpected
	return out, nil
}
