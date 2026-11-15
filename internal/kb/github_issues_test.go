package kb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestParseGitHubRepository(t *testing.T) {
	for in, want := range map[string]string{
		"octocat/Hello-World":                          "octocat/Hello-World",
		" https://github.com/octocat/hello.world.git ": "octocat/hello.world",
		"github.com/octocat/repo/issues":               "octocat/repo",
		"HTTPS://GitHub.com/a/b/":                      "a/b",
	} {
		got, err := parseGitHubRepository(in)
		if err != nil || got != want {
			t.Fatalf("parseGitHubRepository(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "octocat", "octocat/", "-bad/repo", "a/..", "a/b c", "https://evil.example/a/b", "a/b?x=1", "../a"} {
		if _, err := parseGitHubRepository(in); err == nil {
			t.Fatalf("parseGitHubRepository(%q) accepted", in)
		}
	}
}

func TestNewGitHubClient(t *testing.T) {
	if c, err := NewGitHubClient("", ""); err != nil || c.BaseURL != "" || c.Token != "" {
		t.Fatalf("empty config: %+v %v", c, err)
	}
	if c, err := NewGitHubClient("https://ghe.example/api/v3", " tok "); err != nil || c.Token != "tok" {
		t.Fatalf("enterprise config: %+v %v", c, err)
	}
	for _, bad := range []string{"http://api.github.com", "https://u:p@api.github.com", "https://api.github.com?x=1", "ftp://x"} {
		if _, err := NewGitHubClient(bad, ""); err == nil {
			t.Fatalf("accepted base URL %q", bad)
		}
	}
	if _, err := NewGitHubClient("", "a b"); err == nil {
		t.Fatal("accepted token with whitespace")
	}
}

func ghItem(n int, state string, labels ...string) map[string]any {
	ls := []map[string]string{}
	for _, l := range labels {
		ls = append(ls, map[string]string{"name": l})
	}
	return map[string]any{"number": n, "title": fmt.Sprintf("Issue %d", n), "body": fmt.Sprintf("body %d", n), "state": state,
		"html_url": fmt.Sprintf("https://github.com/o/r/issues/%d", n), "user": map[string]string{"login": "dev"}, "labels": ls,
		"created_at": "2026-11-01T00:00:00Z", "updated_at": "2026-11-02T00:00:00Z"}
}

func TestGitHubClientListIssues(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		if r.URL.Path != "/repos/o/r/issues" || r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		page := r.URL.Query().Get("page")
		items := []map[string]any{}
		if page == "1" {
			for i := 1; i <= 100; i++ {
				item := ghItem(i, "open")
				if i%10 == 0 {
					item["pull_request"] = map[string]string{"url": "x"}
				}
				items = append(items, item)
			}
		} else if page == "2" {
			for i := 101; i <= 120; i++ {
				items = append(items, ghItem(i, "open"))
			}
		}
		json.NewEncoder(w).Encode(items)
	}))
	defer srv.Close()
	c := &GitHubClient{BaseURL: srv.URL, Token: "secret"}
	got, skipped, err := c.ListIssues(t.Context(), "o/r", "all", []string{"bug", "ui"}, "2026-11-01T00:00:00Z", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 110 || skipped != 10 {
		t.Fatalf("got %d issues, %d skipped", len(got), skipped)
	}
	if len(queries) != 2 || !strings.Contains(queries[0], "labels=bug%2Cui") || !strings.Contains(queries[0], "state=all") || !strings.Contains(queries[0], "since=2026-11-01") {
		t.Fatalf("queries: %v", queries)
	}
	// limit stops paging early.
	queries = nil
	got, _, err = c.ListIssues(t.Context(), "o/r", "open", nil, "", 5)
	if err != nil || len(got) != 5 || len(queries) != 1 {
		t.Fatalf("limit: %d %v %v", len(got), err, queries)
	}
}

func TestGitHubClientErrors(t *testing.T) {
	cases := []struct {
		status int
		header map[string]string
		token  string
		code   string
		hint   string
	}{
		{404, nil, "", "NOT_FOUND", "GITHUB_TOKEN"},
		{404, nil, "ghp_secretvalue", "NOT_FOUND", "token cannot read"},
		{401, nil, "ghp_secretvalue", "GITHUB_UNAUTHORIZED", ""},
		{403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1790000000"}, "", "GITHUB_RATE_LIMITED", "GITHUB_TOKEN"},
		{429, nil, "ghp_secretvalue", "GITHUB_RATE_LIMITED", ""},
		{403, nil, "ghp_secretvalue", "GITHUB_UNAUTHORIZED", ""},
		{410, nil, "", "NOT_FOUND", "disabled"},
		{422, nil, "", "INVALID_INPUT", ""},
		{502, nil, "", "GITHUB_UNAVAILABLE", ""},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range tc.header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(tc.status)
		}))
		_, _, err := (&GitHubClient{BaseURL: srv.URL, Token: tc.token}).ListIssues(t.Context(), "o/r", "open", nil, "", 10)
		srv.Close()
		if errorCode(err) != tc.code || !strings.Contains(PublicError(err), tc.hint) {
			t.Fatalf("status %d: got %v", tc.status, err)
		}
		if tc.token != "" && strings.Contains(PublicError(err), tc.token) {
			t.Fatal("token leaked into error")
		}
	}
}

func TestGitHubIssueMapping(t *testing.T) {
	notPlanned, completed := "not_planned", "completed"
	if s, _ := githubStatus(githubIssue{State: "open"}); s != "open" {
		t.Fatal(s)
	}
	if s, note := githubStatus(githubIssue{State: "closed", StateReason: &completed}); s != "resolved" || !strings.Contains(note, "completed") {
		t.Fatal(s, note)
	}
	if s, _ := githubStatus(githubIssue{State: "closed", StateReason: &notPlanned}); s != "closed" {
		t.Fatal(s)
	}
	if s, _ := githubStatus(githubIssue{State: "closed"}); s != "resolved" {
		t.Fatal(s)
	}
	if githubSourceState(githubIssue{State: "closed", StateReason: &notPlanned}) != "closed:not_planned" {
		t.Fatal("source state")
	}
	item := githubIssue{Number: 7, HTMLURL: "https://github.com/o/r/issues/7", CreatedAt: "2026-11-01T00:00:00Z", Comments: 2}
	item.Labels = append(item.Labels, struct {
		Name string `json:"name"`
	}{"Question"})
	if githubKind("auto", item) != "question" || githubKind("issue", item) != "issue" || githubKind("auto", githubIssue{}) != "issue" {
		t.Fatal("kind")
	}
	body := renderGitHubIssueBody("o/r", item)
	if !strings.Contains(body, "[o/r#7](https://github.com/o/r/issues/7)") || !strings.Contains(body, "GitHub 본문 없음") || !strings.Contains(body, "댓글 2개") || !strings.Contains(body, "@unknown") {
		t.Fatalf("body: %s", body)
	}
	long := strings.Repeat("가", 30000)
	item.Body = &long
	if n := len([]rune(renderGitHubIssueBody("o/r", item))); n != 20000 {
		t.Fatalf("body not clipped: %d", n)
	}
	tags := githubIssueTags([]string{"github"}, githubIssue{Labels: []struct {
		Name string `json:"name"`
	}{{"bug"}, {"GitHub"}, {strings.Repeat("x", 60)}}})
	if strings.Join(tags, ",") != "github,bug" {
		t.Fatalf("tags: %v", tags)
	}
	if _, err := validateGitHubImport(ImportGitHubIssuesInput{Repository: "o/r", Limit: 501}); errorCode(err) != "INVALID_INPUT" {
		t.Fatal("limit not checked")
	}
	if _, err := validateGitHubImport(ImportGitHubIssuesInput{Repository: "o/r", Since: "yesterday"}); errorCode(err) != "INVALID_INPUT" {
		t.Fatal("since not checked")
	}
	plan, err := validateGitHubImport(ImportGitHubIssuesInput{Repository: "https://github.com/o/r", Since: "2026-11-01T09:00:00+09:00"})
	if err != nil || plan.in.State != "open" || plan.in.Kind != "issue" || plan.in.Limit != 100 || plan.in.Since != "2026-11-01T00:00:00Z" {
		t.Fatalf("defaults: %+v %v", plan.in, err)
	}
}

func TestImportGitHubIssues(t *testing.T) {
	s := testStore(t)
	var mu sync.Mutex
	items := []map[string]any{ghItem(1, "open", "bug"), ghItem(2, "open", "question"), ghItem(3, "closed")}
	items[2]["state_reason"] = "not_planned"
	pr := ghItem(4, "open")
	pr["pull_request"] = map[string]string{"url": "x"}
	items = append(items, pr)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		json.NewEncoder(w).Encode(items)
	}))
	defer srv.Close()
	s.GitHub = &GitHubClient{BaseURL: srv.URL}
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "gh-editor@example.com")
	viewer := inviteAs(t, s, admin, "gh-viewer@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "gh-import", Name: "gh"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	area := mustCall(t, tools, admin, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "배포", OwnerID: editor.User.ID}).(WorkArea)

	// Viewers are rejected before GitHub is called.
	_, err := call(t, tools, viewer, "import_github_issues", ImportGitHubIssuesInput{ProjectID: p.ID, Repository: "o/r"})
	denied(t, err, "FORBIDDEN")
	if calls != 0 {
		t.Fatal("GitHub was called for an unauthorized import")
	}

	in := ImportGitHubIssuesInput{ProjectID: p.ID, Repository: "https://github.com/O/R", State: "all", Kind: "auto", AreaID: area.ID, Tags: []string{"외부"}}
	r := mustCall(t, tools, admin, "import_github_issues", in).(ImportGitHubIssuesResult)
	if r.Repository != "O/R" || r.Fetched != 3 || r.Created != 3 || r.SkippedPullRequests != 1 || r.Authenticated {
		t.Fatalf("first import: %+v", r)
	}
	list := mustCall(t, tools, admin, "list_issues", IssueListInput{ProjectID: p.ID, Status: "all"}).([]Issue)
	if len(list) != 3 {
		t.Fatalf("issues: %d", len(list))
	}
	byKey := map[string]Issue{}
	for _, i := range list {
		if i.Source == nil || *i.Source != "github" || i.SourceKey == nil || i.SourceURL == nil {
			t.Fatalf("source not set: %+v", i)
		}
		byKey[*i.SourceKey] = i
	}
	one, two, three := byKey["o/r#1"], byKey["o/r#2"], byKey["o/r#3"]
	if one.Kind != "issue" || two.Kind != "question" || one.Status != "open" || three.Status != "closed" || !strings.Contains(three.Resolution, "not_planned") {
		t.Fatalf("mapping: %+v %+v %+v", one, two, three)
	}
	if one.AssigneeID == nil || *one.AssigneeID != editor.User.ID || one.AreaID == nil || strings.Join(one.Tags, ",") != "github,외부,bug" {
		t.Fatalf("assignment and tags: %+v", one)
	}

	// Same data again: nothing changes.
	r = mustCall(t, tools, admin, "import_github_issues", in).(ImportGitHubIssuesResult)
	if r.Created != 0 || r.Updated != 0 || r.Unchanged != 3 {
		t.Fatalf("idempotent import: %+v", r)
	}

	// Work done here (answer, tag) survives; GitHub edits and state changes are applied.
	mustCall(t, tools, editor, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: one.ID, Answer: "배포 스크립트에서 처리합니다."})
	mustCall(t, tools, admin, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: two.ID, Tags: &[]string{"github", "확인함"}})
	mu.Lock()
	items[0]["title"], items[0]["updated_at"] = "Issue 1 (edited)", "2026-11-03T00:00:00Z"
	items[1]["state"], items[1]["state_reason"], items[1]["updated_at"] = "closed", "completed", "2026-11-03T00:00:00Z"
	mu.Unlock()
	r = mustCall(t, tools, editor, "import_github_issues", in).(ImportGitHubIssuesResult)
	if r.Created != 0 || r.Updated != 2 || r.Unchanged != 1 {
		t.Fatalf("update import: %+v", r)
	}
	got := mustCall(t, tools, admin, "get_issue", IssueRef{ProjectID: p.ID, IssueID: one.ID}).(Issue)
	if got.Title != "Issue 1 (edited)" || got.Status != "answered" || got.Answer == "" {
		t.Fatalf("answered issue after re-import: %+v", got)
	}
	got = mustCall(t, tools, admin, "get_issue", IssueRef{ProjectID: p.ID, IssueID: two.ID}).(Issue)
	if got.Status != "resolved" || !strings.Contains(got.Resolution, "completed") || !strings.Contains(strings.Join(got.Tags, ","), "확인함") {
		t.Fatalf("closed on GitHub: %+v", got)
	}

	// Reopened on GitHub → reopened here.
	mu.Lock()
	items[1]["state"], items[1]["state_reason"], items[1]["updated_at"] = "open", nil, "2026-11-04T00:00:00Z"
	mu.Unlock()
	mustCall(t, tools, admin, "import_github_issues", in)
	got = mustCall(t, tools, admin, "get_issue", IssueRef{ProjectID: p.ID, IssueID: two.ID}).(Issue)
	if got.Status != "open" {
		t.Fatalf("reopened: %+v", got)
	}

	// Another project imports independently.
	p2 := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "gh-import-2", Name: "gh2"}).(Project)
	r = mustCall(t, tools, admin, "import_github_issues", ImportGitHubIssuesInput{ProjectID: p2.ID, Repository: "o/r", State: "all"}).(ImportGitHubIssuesResult)
	if r.Created != 3 {
		t.Fatalf("second project: %+v", r)
	}
}
