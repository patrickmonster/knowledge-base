package kb

import (
	"context"
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

	// The same repository in two projects returns both; agents must ask.
	mustCall(t, tools, owner, "add_project_repository", ProjectRepositoryInput{ProjectID: b.ID, Repository: "owner/app"})
	if both := mustCall(t, tools, owner, "list_projects", ListProjectsInput{Repository: "owner/app"}).([]Project); len(both) != 2 {
		t.Fatalf("both: %+v", both)
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
