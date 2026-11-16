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

type ResolveProjectInput struct {
	Repository string `json:"repository" jsonschema:"The checkout's git remote, e.g. the output of git remote get-url origin"`
}

// ResolvedProject answers which project on this server, if any, a checkout
// belongs to. Several knowledge servers may be connected to one client and the
// agent may ask all of them; a server without the repository answers
// matched=false so the agent leaves it alone instead of stopping to ask.
//
// A match carries the project's rules. Rules are otherwise pulled by start_task
// or list_rules, and an agent that resolves the project and then goes straight
// to editing and committing never saw them; this is the one call every agent
// makes, so the rules ride along with it.
type ResolvedProject struct {
	Repository string    `json:"repository"`
	Matched    bool      `json:"matched"`
	Project    *Project  `json:"project,omitempty"`
	Rules      *RuleSet  `json:"rules,omitempty"`
	Candidates []Project `json:"candidates,omitempty"`
	Guidance   string    `json:"guidance"`
}

const (
	resolveNoMatch = "This server has no project for this repository. Do not use this server for this task: do not call start_task, list_rules or finish_task here, do not ask the user about it, and continue the work. Another connected knowledge server may match."
	resolveMatch   = "Use this project for this task on this server. rules are this project's work rules as of now: follow them from the first step (branch, commit, pull request, completion record), also when you have not called start_task yet; no rules field means none are set. Before each change task call start_task(project_id), which returns the rules again in context.rules (they can change), and finish_task when done. With role viewer, only read (search, documents, rules); skip start_task and finish_task."
	resolveSeveral = "Several projects on this server are registered for this repository. Ask the user which one to use (or none); do not guess from names."
)

func resolveProject(ctx context.Context, tx *sql.Tx, a Identity, in ResolveProjectInput) (ResolvedProject, error) {
	repo, err := normalizeRepository(in.Repository)
	if err != nil {
		return ResolvedProject{}, err
	}
	projects, err := many[Project](ctx, tx, `SELECT to_jsonb(p)||jsonb_build_object('role',m.role) FROM projects p
		JOIN project_members m ON m.project_id=p.id
		JOIN project_repositories r ON r.project_id=p.id AND r.repository=$2
		WHERE m.user_id=$1 AND ($3='' OR p.id=NULLIF($3,'')::uuid)
		ORDER BY p.created_at,p.id LIMIT 21`, a.User.ID, repo, a.ProjectID)
	if err != nil {
		return ResolvedProject{}, err
	}
	out := ResolvedProject{Repository: repo}
	switch len(projects) {
	case 0:
		out.Guidance = resolveNoMatch
	case 1:
		out.Matched, out.Project, out.Guidance = true, &projects[0], resolveMatch
		if out.Rules, err = taskRules(ctx, tx, a, projects[0]); err != nil {
			return ResolvedProject{}, err
		}
	default:
		out.Candidates, out.Guidance = projects, resolveSeveral
	}
	return out, nil
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
	add(t, s, "resolve_project", "Find the project on this server registered for a git checkout. Call it first, with the checkout's git remote, on every connected knowledge server. matched=true: use project.id for this task and follow the returned rules (the project's work rules for branches, commits and pull requests) right away. matched=false: this server is unrelated to the repository; leave it alone without asking the user. candidates: ask the user which project. Read-only and safe to call on any server.", false, nil, func(ctx context.Context, tx *sql.Tx, a Identity, in ResolveProjectInput) (any, error) {
		return resolveProject(ctx, tx, a, in)
	})
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
