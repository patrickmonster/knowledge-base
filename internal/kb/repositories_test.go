package kb

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizeRepository(t *testing.T) {
	want := "github.com/patrickmonster/knowledge-base"
	for _, in := range []string{
		"patrickmonster/knowledge-base",
		"PatrickMonster/Knowledge-Base",
		"https://github.com/patrickmonster/knowledge-base",
		"https://github.com/patrickmonster/knowledge-base.git",
		"https://github.com/patrickmonster/knowledge-base/",
		"https://github.com/patrickmonster/knowledge-base/issues/42",
		"http://github.com/patrickmonster/knowledge-base",
		"https://user:token@github.com/patrickmonster/knowledge-base.git",
		"git@github.com:patrickmonster/knowledge-base.git",
		"git@github.com:PatrickMonster/knowledge-base",
		"ssh://git@github.com/patrickmonster/knowledge-base.git",
		"ssh://git@github.com:22/patrickmonster/knowledge-base",
		"github.com/patrickmonster/knowledge-base",
		"  github.com/patrickmonster/knowledge-base.git\n",
	} {
		got, err := normalizeRepository(in)
		if err != nil || got != want {
			t.Errorf("normalizeRepository(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for in, out := range map[string]string{
		"https://gitlab.com/group/sub/project.git": "gitlab.com/group/sub/project",
		"git@gitlab.example.com:team/app.git":      "gitlab.example.com/team/app",
		"gitlab.com/group/project":                 "gitlab.com/group/project",
	} {
		if got, err := normalizeRepository(in); err != nil || got != out {
			t.Errorf("normalizeRepository(%q) = %q, %v; want %q", in, got, err, out)
		}
	}
	for _, in := range []string{"", "knowledge-base", "https://github.com/", "https://github.com/owner", "owner/..", "owner/a b", "file:///tmp/repo", "git@github.com:owner/.."} {
		if got, err := normalizeRepository(in); err == nil {
			t.Errorf("normalizeRepository(%q) = %q, want error", in, got)
		}
	}
}

func TestProjectRepositories(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	admin := inviteAs(t, s, owner, "repo-admin@example.com")
	editor := inviteAs(t, s, owner, "repo-editor@example.com")
	outsider := inviteAs(t, s, owner, "repo-outsider@example.com")

	a := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "repo-a", Name: "A"}).(Project)
	b := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "repo-b", Name: "B"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: a.ID, UserID: admin.User.ID, Role: "admin"})
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: a.ID, UserID: editor.User.ID, Role: "editor"})

	added := mustCall(t, tools, owner, "add_project_repository", ProjectRepositoryInput{ProjectID: a.ID, Repository: "git@github.com:Owner/App.git"}).(ProjectRepositories)
	if !added.Changed || len(added.Repositories) != 1 || added.Repositories[0] != "github.com/owner/app" {
		t.Fatalf("added: %+v", added)
	}
	again := mustCall(t, tools, admin, "add_project_repository", ProjectRepositoryInput{ProjectID: a.ID, Repository: "https://github.com/owner/app"}).(ProjectRepositories)
	if again.Changed || len(again.Repositories) != 1 {
		t.Fatalf("again: %+v", again)
	}
	_, err := call(t, tools, editor, "add_project_repository", ProjectRepositoryInput{ProjectID: a.ID, Repository: "owner/other"})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, outsider, "add_project_repository", ProjectRepositoryInput{ProjectID: a.ID, Repository: "owner/other"})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, owner, "add_project_repository", ProjectRepositoryInput{ProjectID: a.ID, Repository: "not a repo"})
	denied(t, err, "INVALID_INPUT")

	// list_projects carries repositories and filters by the checkout's remote.
	all := mustCall(t, tools, owner, "list_projects", ListProjectsInput{}).([]Project)
	byID := map[string]Project{}
	for _, p := range all {
		byID[p.ID] = p
	}
	if r := byID[a.ID].Repositories; len(r) != 1 || r[0] != "github.com/owner/app" {
		t.Fatalf("project a repositories: %+v", byID[a.ID])
	}
	if r := byID[b.ID].Repositories; len(r) != 0 {
		t.Fatalf("project b repositories: %+v", byID[b.ID])
	}
	match := mustCall(t, tools, editor, "list_projects", ListProjectsInput{Repository: "https://github.com/OWNER/app.git"}).([]Project)
	if len(match) != 1 || match[0].ID != a.ID {
		t.Fatalf("match: %+v", match)
	}
	if none := mustCall(t, tools, owner, "list_projects", ListProjectsInput{Repository: "owner/unknown"}).([]Project); len(none) != 0 {
		t.Fatalf("none: %+v", none)
	}
	_, err = call(t, tools, owner, "list_projects", ListProjectsInput{Repository: "nope"})
	denied(t, err, "INVALID_INPUT")
	// Projects the caller is not a member of never match.
	if out := mustCall(t, tools, outsider, "list_projects", ListProjectsInput{Repository: "owner/app"}).([]Project); len(out) != 0 {
		t.Fatalf("outsider: %+v", out)
	}

	// resolve_project: one match is used; no match tells the agent to leave
	// this server alone (another connected server may own the repository).
	one := mustCall(t, tools, editor, "resolve_project", ResolveProjectInput{Repository: "git@github.com:owner/app.git"}).(ResolvedProject)
	if !one.Matched || one.Project == nil || one.Project.ID != a.ID || one.Project.Role != "editor" || one.Repository != "github.com/owner/app" || one.Guidance != resolveMatch {
		t.Fatalf("resolve one: %+v", one)
	}
	// A match carries the caller's rules, so an agent that goes straight from
	// resolving to committing has seen them; without rules the field is absent.
	if one.Rules != nil {
		t.Fatalf("resolve without rules: %+v", one.Rules)
	}
	mustCall(t, tools, owner, "save_rule", SaveRuleInput{ProjectID: a.ID, Scope: "project", Category: "commit", Title: "branch per task", Body: "b"})
	off := mustCall(t, tools, owner, "save_rule", SaveRuleInput{ProjectID: a.ID, Scope: "project", Title: "switched off", Body: "b"}).(Rule)
	mustCall(t, tools, owner, "set_rule_enabled", SetRuleEnabledInput{ProjectID: a.ID, RuleID: off.ID, Enabled: false})
	mustCall(t, tools, editor, "save_rule", SaveRuleInput{ProjectID: a.ID, Scope: "personal", Title: "mine", Body: "b"})
	mustCall(t, tools, owner, "save_rule", SaveRuleInput{ProjectID: a.ID, Scope: "personal", Title: "the owner's own", Body: "b"})
	ruled := mustCall(t, tools, editor, "resolve_project", ResolveProjectInput{Repository: "owner/app"}).(ResolvedProject)
	if ruled.Rules == nil || len(ruled.Rules.ProjectRules) != 1 || ruled.Rules.ProjectRules[0].Title != "branch per task" ||
		len(ruled.Rules.PersonalRules) != 1 || ruled.Rules.PersonalRules[0].Title != "mine" || ruled.Rules.Guidance != rulesGuidance {
		t.Fatalf("resolve rules: %+v", ruled.Rules)
	}
	for _, who := range []Identity{owner, outsider} {
		repo := "owner/unknown"
		if who.User.ID == outsider.User.ID {
			repo = "owner/app" // registered, but not in a project the caller can see
		}
		none := mustCall(t, tools, who, "resolve_project", ResolveProjectInput{Repository: repo}).(ResolvedProject)
		if none.Matched || none.Project != nil || none.Rules != nil || len(none.Candidates) != 0 || none.Guidance != resolveNoMatch {
			t.Fatalf("resolve none (%s): %+v", repo, none)
		}
	}
	_, err = call(t, tools, owner, "resolve_project", ResolveProjectInput{Repository: "nope"})
	denied(t, err, "INVALID_INPUT")

	// The same repository in two projects returns both; agents must ask.
	mustCall(t, tools, owner, "add_project_repository", ProjectRepositoryInput{ProjectID: b.ID, Repository: "owner/app"})
	if both := mustCall(t, tools, owner, "list_projects", ListProjectsInput{Repository: "owner/app"}).([]Project); len(both) != 2 {
		t.Fatalf("both: %+v", both)
	}
	several := mustCall(t, tools, owner, "resolve_project", ResolveProjectInput{Repository: "owner/app"}).(ResolvedProject)
	if several.Matched || several.Project != nil || several.Rules != nil || len(several.Candidates) != 2 || several.Guidance != resolveSeveral {
		t.Fatalf("resolve several: %+v", several)
	}

	// A project-scoped key sees and manages only its project.
	keyOut, err := s.CreateKey(ctx, owner, KeyInput{Name: "scoped", Access: "read_write", ProjectID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if only := mustCall(t, tools, scoped, "list_projects", ListProjectsInput{Repository: "owner/app"}).([]Project); len(only) != 1 || only[0].ID != a.ID {
		t.Fatalf("scoped: %+v", only)
	}
	// A project-scoped key resolves only within its project, so the repository
	// registered in two projects is not ambiguous for it.
	if r := mustCall(t, tools, scoped, "resolve_project", ResolveProjectInput{Repository: "owner/app"}).(ResolvedProject); !r.Matched || r.Project.ID != a.ID {
		t.Fatalf("scoped resolve: %+v", r)
	}
	_, err = call(t, tools, scoped, "add_project_repository", ProjectRepositoryInput{ProjectID: b.ID, Repository: "owner/x"})
	denied(t, err, "FORBIDDEN")

	removed := mustCall(t, tools, owner, "remove_project_repository", ProjectRepositoryInput{ProjectID: b.ID, Repository: "git@github.com:owner/app.git"}).(ProjectRepositories)
	if !removed.Changed || len(removed.Repositories) != 0 {
		t.Fatalf("removed: %+v", removed)
	}
	if again := mustCall(t, tools, owner, "remove_project_repository", ProjectRepositoryInput{ProjectID: b.ID, Repository: "owner/app"}).(ProjectRepositories); again.Changed {
		t.Fatalf("remove again: %+v", again)
	}
	_, err = call(t, tools, editor, "remove_project_repository", ProjectRepositoryInput{ProjectID: a.ID, Repository: "owner/app"})
	denied(t, err, "FORBIDDEN")

	for i := 1; i < maxProjectRepositories; i++ {
		mustCall(t, tools, owner, "add_project_repository", ProjectRepositoryInput{ProjectID: a.ID, Repository: "owner/r" + string(rune('a'+i))})
	}
	_, err = call(t, tools, owner, "add_project_repository", ProjectRepositoryInput{ProjectID: a.ID, Repository: "owner/one-too-many"})
	denied(t, err, "INVALID_INPUT")
}

// The server instructions carry the whole workflow, so a repository needs no
// AGENTS.md, and they tell an unrelated server among several to stay out.
func TestServerInstructionsPickProjectByRepository(t *testing.T) {
	for _, want := range []string{"resolve_project", "matched=false", "do not ask the user", "without any file in the repository", "Leave no trace of this server in the repository", "follow the rules it returns", "only commits, pushes, opens a pull request"} {
		if !strings.Contains(serverInstructions, want) {
			t.Errorf("server instructions miss %q", want)
		}
	}
}
