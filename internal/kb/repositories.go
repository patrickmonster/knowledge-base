package kb

import (
	"context"
	"database/sql"
	"net/url"
	"regexp"
	"strings"
)

// Project repositories: an agent working in a git checkout passes its remote
// to list_projects and gets the project registered for that repository, instead
// of matching project names.

const maxProjectRepositories = 20

var (
	repoHostPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,252}[a-z0-9])?$`)
	repoSegmentPattern = regexp.MustCompile(`^[a-z0-9._~-]{1,100}$`)
	scpLikePattern     = regexp.MustCompile(`^[^/@:]+@([^/:]+):(.+)$`)
)

// normalizeRepository turns a git remote or repository link into host/path in
// lower case: "git@github.com:Owner/Repo.git", "https://github.com/owner/repo",
// "ssh://git@github.com/owner/repo" and "owner/repo" all become
// "github.com/owner/repo". Credentials and ports in the remote are dropped.
func normalizeRepository(raw string) (string, error) {
	invalid := problem("INVALID_INPUT", "repository must be a git remote such as https://github.com/owner/name, git@github.com:owner/name.git or owner/name.")
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || len(s) > 500 {
		return "", invalid
	}
	var host, path string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return "", invalid
		}
		host, path = u.Hostname(), u.Path
	case scpLikePattern.MatchString(s):
		m := scpLikePattern.FindStringSubmatch(s)
		host, path = m[1], m[2]
	default:
		parts := strings.SplitN(strings.Trim(s, "/"), "/", 2)
		if len(parts) == 2 && strings.Contains(parts[0], ".") {
			host, path = parts[0], parts[1]
		} else {
			host, path = "github.com", s
		}
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if host == "github.com" && len(segments) > 2 {
		// Links such as github.com/owner/name/issues name the same repository.
		segments = segments[:2]
	}
	if !repoHostPattern.MatchString(host) || len(segments) < 2 {
		return "", invalid
	}
	for _, seg := range segments {
		if !repoSegmentPattern.MatchString(seg) || seg == "." || seg == ".." {
			return "", invalid
		}
	}
	out := host + "/" + strings.Join(segments, "/")
	if len(out) > 200 {
		return "", invalid
	}
	return out, nil
}

type ListProjectsInput struct {
	PageInput
	Repository string `json:"repository,omitempty"`
}

type ProjectRepositoryInput struct {
	ProjectID  string `json:"project_id"`
	Repository string `json:"repository"`
}

type ProjectRepositories struct {
	ProjectID    string   `json:"project_id"`
	Repositories []string `json:"repositories"`
	Changed      bool     `json:"changed"`
}

func projectRepositoryList(ctx context.Context, tx *sql.Tx, projectID string) ([]string, error) {
	return many[string](ctx, tx, `SELECT to_jsonb(repository) FROM project_repositories WHERE project_id=$1 ORDER BY repository`, projectID)
}

func manageProjectRepository(ctx context.Context, tx *sql.Tx, a Identity, in ProjectRepositoryInput) (Project, string, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true)
	if err != nil {
		return p, "", err
	}
	if p.Role != "owner" && p.Role != "admin" {
		return p, "", problem("FORBIDDEN", "Owner or admin access is required.")
	}
	repo, err := normalizeRepository(in.Repository)
	return p, repo, err
}

func registerRepositoryTools(t Tools, s *Store) {
	add(t, s, "add_project_repository", "Owner or admin: register a source repository (git remote or owner/name) for the project, so list_projects(repository) finds it. Stored normalized as host/owner/name. Registering the same repository again changes nothing.", true, func(in ProjectRepositoryInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ProjectRepositoryInput) (any, error) {
		p, repo, err := manageProjectRepository(ctx, tx, a, in)
		if err != nil {
			return nil, err
		}
		current, err := projectRepositoryList(ctx, tx, p.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range current {
			if r == repo {
				return ProjectRepositories{ProjectID: p.ID, Repositories: current}, nil
			}
		}
		if len(current) >= maxProjectRepositories {
			return nil, problem("INVALID_INPUT", "A project can have at most 20 repositories.")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_repositories(project_id,repository,created_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, p.ID, repo, a.User.ID); err != nil {
			return nil, err
		}
		list, err := projectRepositoryList(ctx, tx, p.ID)
		return ProjectRepositories{ProjectID: p.ID, Repositories: list, Changed: true}, err
	})
	add(t, s, "remove_project_repository", "Owner or admin: unregister a source repository from the project. Removing one that is not registered changes nothing.", true, func(in ProjectRepositoryInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ProjectRepositoryInput) (any, error) {
		p, repo, err := manageProjectRepository(ctx, tx, a, in)
		if err != nil {
			return nil, err
		}
		r, err := tx.ExecContext(ctx, `DELETE FROM project_repositories WHERE project_id=$1 AND repository=$2`, p.ID, repo)
		if err != nil {
			return nil, err
		}
		n, _ := r.RowsAffected()
		list, err := projectRepositoryList(ctx, tx, p.ID)
		return ProjectRepositories{ProjectID: p.ID, Repositories: list, Changed: n > 0}, err
	})
}
