package kb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// GitHub tokens are set per project repository in the web console; merge
// checks and issue imports use the token of the repository they read.
func TestRepositoryGitHubToken(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	const token = "github_pat_0123456789abcdefghij"
	var mu sync.Mutex
	auth := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		// A private repository: GitHub answers 404 without a token that can read it.
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/repos/o/private/pulls/3":
			json.NewEncoder(w).Encode(map[string]any{"state": "closed", "merged": true, "merge_commit_sha": "3333333", "merged_at": "2026-11-20T01:00:00Z"})
		case "/repos/o/private/issues":
			json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s.GitHub = &GitHubClient{BaseURL: srv.URL}
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "token-editor@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "repo-token", Name: "repo token"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "repo-token-other", Name: "other"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "add_project_repository", ProjectRepositoryInput{ProjectID: p.ID, Repository: "https://github.com/O/private.git"})
	mustCall(t, tools, admin, "add_project_repository", ProjectRepositoryInput{ProjectID: other.ID, Repository: "o/private"})

	run := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "token-1", Title: "토큰", Task: "비공개 저장소 PR"}).(StartTaskResult).Run
	finish := finishFixture(p.ID, run.ID)
	finish.PullRequests = []string{"o/private#3"}
	mustCall(t, tools, admin, "finish_task", finish)

	// No token yet: GitHub hides the private repository.
	r := mustCall(t, tools, admin, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID}).(DeploymentCheckResult)
	if len(r.PullRequests) != 1 || r.PullRequests[0].CheckError == nil || *r.PullRequests[0].CheckError != "NOT_FOUND" {
		t.Fatalf("without token: %+v", r.PullRequests)
	}

	// Only a signed-in owner or admin sets tokens; never an editor or an MCP key.
	in := RepositoryTokenInput{ProjectID: p.ID, Repository: "o/private", Token: token}
	_, err := call(t, tools, editor, "set_project_repository_token", in)
	denied(t, err, "FORBIDDEN")
	keyOut, err := s.CreateKey(ctx, admin, KeyInput{Name: "rw", Access: "read_write", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "set_project_repository_token", in)
	denied(t, err, "FORBIDDEN")
	for _, bad := range []RepositoryTokenInput{
		{ProjectID: p.ID, Repository: "o/private", Token: "short"},
		{ProjectID: p.ID, Repository: "o/private", Token: "github_pat_with space 0123456789"},
		{ProjectID: p.ID, Repository: "o/private", Token: token, Remove: true},
	} {
		_, err = call(t, tools, admin, "set_project_repository_token", bad)
		denied(t, err, "INVALID_INPUT")
	}
	_, err = call(t, tools, admin, "set_project_repository_token", RepositoryTokenInput{ProjectID: p.ID, Repository: "o/unregistered", Token: token})
	denied(t, err, "NOT_FOUND")

	list := mustCall(t, tools, admin, "set_project_repository_token", in).([]ProjectRepository)
	if len(list) != 1 || list[0].Repository != "github.com/o/private" || !list[0].GitHubToken || list[0].TokenSetAt == nil {
		t.Fatalf("after set: %+v", list)
	}
	// Members see whether a token is set, never the token.
	seen := mustCall(t, tools, editor, "list_project_repositories", ProjectInput{ProjectID: p.ID}).([]ProjectRepository)
	raw, _ := json.Marshal(seen)
	if len(seen) != 1 || !seen[0].GitHubToken || strings.Contains(string(raw), token) {
		t.Fatalf("listed: %s", raw)
	}
	var audited int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE details::text LIKE '%'||$1||'%'`, token).Scan(&audited); err != nil || audited != 0 {
		t.Fatalf("token in audit log: %d %v", audited, err)
	}

	// The failed check is retried at once and now reads the merge with the token.
	r = mustCall(t, tools, admin, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID}).(DeploymentCheckResult)
	if len(r.PullRequests) != 1 || r.PullRequests[0].State != "merged" || r.PullRequests[0].CheckError != nil {
		t.Fatalf("with token: %+v", r.PullRequests)
	}
	// Issue imports of the repository use the same token.
	imp := mustCall(t, tools, admin, "import_github_issues", ImportGitHubIssuesInput{ProjectID: p.ID, Repository: "o/private"}).(ImportGitHubIssuesResult)
	if !imp.Authenticated {
		t.Fatalf("import without token: %+v", imp)
	}
	// Another project registering the same repository does not get the token.
	_, err = call(t, tools, admin, "import_github_issues", ImportGitHubIssuesInput{ProjectID: other.ID, Repository: "o/private"})
	denied(t, err, "NOT_FOUND")
	mu.Lock()
	if auth["/repos/o/private/issues"] != "" {
		t.Fatal("token of one project was used for another")
	}
	mu.Unlock()

	// The webhook card shows the repository's token state.
	mustCall(t, tools, admin, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, Repository: "o/private"})
	hooks := mustCall(t, tools, admin, "list_github_webhooks", ProjectInput{ProjectID: p.ID}).(GitHubWebhookList)
	if len(hooks.Webhooks) != 1 || !hooks.Webhooks[0].TokenConfigured {
		t.Fatalf("webhook token state: %+v", hooks.Webhooks)
	}

	list = mustCall(t, tools, admin, "set_project_repository_token", RepositoryTokenInput{ProjectID: p.ID, Repository: "github.com/o/private", Remove: true}).([]ProjectRepository)
	if len(list) != 1 || list[0].GitHubToken || list[0].TokenSetAt != nil {
		t.Fatalf("after remove: %+v", list)
	}
}
