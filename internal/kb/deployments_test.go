package kb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestParsePullRequest(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Octo/Repo/pull/12":            "https://github.com/octo/repo/pull/12",
		" https://github.com/o/r/pull/3/files ":           "https://github.com/o/r/pull/3",
		"https://github.com/o/r/pull/3#issuecomment-1":    "https://github.com/o/r/pull/3",
		"https://github.com/o/r.js/pull/9?notification=1": "https://github.com/o/r.js/pull/9",
		"o/r#44": "https://github.com/o/r/pull/44",
	} {
		got, err := parsePullRequest(in)
		if err != nil || got.URL != want {
			t.Fatalf("parsePullRequest(%q) = %+v, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"", "#12", "o/r", "https://github.com/o/r/issues/3", "https://evil.example/o/r/pull/3", "o/r#0", "o/r#-1", "http://github.com/o/r/pull/3", "o/r/x#3", "-o/r#3"} {
		if _, err := parsePullRequest(in); err == nil {
			t.Fatalf("parsePullRequest(%q) accepted", in)
		}
	}
	refs, err := parsePullRequests([]string{"o/r#1", "https://github.com/O/R/pull/1", "o/r#2"})
	if err != nil || len(refs) != 2 {
		t.Fatalf("duplicates must collapse: %+v %v", refs, err)
	}
	if _, err := parsePullRequests(make([]string, 11)); err == nil {
		t.Fatal("more than 10 pull requests accepted")
	}
}

func TestDetectDeploymentTarget(t *testing.T) {
	if _, err := DetectDeploymentTarget("not a repo", "", ""); err == nil {
		t.Fatal("invalid SERVER_REPOSITORY accepted")
	}
	d, err := DetectDeploymentTarget("", "Octo", "Repo")
	if err != nil || d.Repository != "octo/repo" {
		t.Fatalf("railway fallback: %+v %v", d, err)
	}
	d, err = DetectDeploymentTarget(" A/B ", "x", "y")
	if err != nil || d.Repository != "a/b" {
		t.Fatalf("explicit repository wins: %+v %v", d, err)
	}
	// Invalid Railway values are ignored rather than stopping the server.
	if d, err = DetectDeploymentTarget("", "-bad", "r"); err != nil || d.Repository != "" {
		t.Fatalf("invalid railway values: %+v %v", d, err)
	}
}

func TestInsertStatusBlock(t *testing.T) {
	block := "> **상태 갱신**\n> - x\n"
	got := insertStatusBlock("# 제목\n\n## 요청\n본문\n", block)
	if got != "# 제목\n\n> **상태 갱신**\n> - x\n\n## 요청\n본문\n" {
		t.Fatalf("below title: %q", got)
	}
	// A newer block goes above an older one; the body is untouched.
	again := insertStatusBlock(got, "> **새 블록**\n")
	if !strings.HasPrefix(again, "# 제목\n\n> **새 블록**\n\n> **상태 갱신**") || !strings.HasSuffix(again, "## 요청\n본문\n") {
		t.Fatalf("second block: %q", again)
	}
	if got := insertStatusBlock("본문", block); got != block+"\n본문" {
		t.Fatalf("no title: %q", got)
	}
}

func TestDescribePullRequest(t *testing.T) {
	s := &Store{Deployment: DeploymentTarget{Repository: "o/r", Commit: "1234567890abcdef"}}
	sha, at, dep := "abcdef1234567", "2026-11-15T01:23:05Z", "1234567890abcdef"
	cases := map[string]TrackedPullRequest{
		"운영 배포 확인":   {Repository: "o/r", State: "deployed", MergeCommit: &sha, MergedAt: &at, DeployedCommit: &dep},
		"아직 포함되지 않음": {Repository: "o/r", State: "merged", MergeCommit: &sha},
		"확인하지 않는다":   {Repository: "o/other", State: "merged", MergeCommit: &sha},
		"닫힘":         {Repository: "o/r", State: "closed"},
		"열려 있음":      {Repository: "o/r", State: "open"},
	}
	for want, p := range cases {
		if got := s.describePullRequest(p); !strings.Contains(got, want) {
			t.Fatalf("%s: %q", want, got)
		}
	}
	if got := s.describePullRequest(cases["운영 배포 확인"]); !strings.Contains(got, "2026-11-15 10:23") || !strings.Contains(got, "abcdef1") || !strings.Contains(got, "1234567") {
		t.Fatalf("times and short commits: %q", got)
	}
	if !s.finalState(cases["확인하지 않는다"]) || s.finalState(cases["아직 포함되지 않음"]) || s.finalState(cases["열려 있음"]) {
		t.Fatal("final states")
	}
}

// fakeGitHub serves pull requests and compare results for tests.
type fakeGitHub struct {
	mu      sync.Mutex
	pulls   map[string]map[string]any
	compare map[string]string
	calls   int
}

func (f *fakeGitHub) set(path string, pull map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulls[path] = pull
}

func (f *fakeGitHub) setCompare(path, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compare[path] = status
}

func (f *fakeGitHub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	f := &fakeGitHub{pulls: map[string]map[string]any{}, compare: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		if pull, ok := f.pulls[r.URL.Path]; ok {
			json.NewEncoder(w).Encode(pull)
			return
		}
		if status, ok := f.compare[r.URL.Path]; ok {
			json.NewEncoder(w).Encode(map[string]string{"status": status})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func TestGitHubPullRequestAndContains(t *testing.T) {
	f, srv := newFakeGitHub(t)
	c := &GitHubClient{BaseURL: srv.URL}
	f.set("/repos/o/r/pulls/5", map[string]any{"state": "closed", "merged": true, "merge_commit_sha": "abc1234", "merged_at": "2026-11-15T01:00:00Z"})
	pr, err := c.PullRequest(t.Context(), "o/r", 5)
	if err != nil || !pr.Merged || *pr.MergeCommitSHA != "abc1234" {
		t.Fatalf("pull: %+v %v", pr, err)
	}
	for status, want := range map[string]bool{"behind": true, "identical": true, "ahead": false, "diverged": false} {
		f.setCompare("/repos/o/r/compare/head123...abc1234", status)
		got, err := c.Contains(t.Context(), "o/r", "head123", "abc1234")
		if err != nil || got != want {
			t.Fatalf("compare %s: %v %v", status, got, err)
		}
	}
	if _, err := c.PullRequest(t.Context(), "o/r", 6); errorCode(err) != "NOT_FOUND" {
		t.Fatalf("missing pull request: %v", err)
	}
}

func TestTaskDeploymentTracking(t *testing.T) {
	s := testStore(t)
	f, srv := newFakeGitHub(t)
	s.GitHub = &GitHubClient{BaseURL: srv.URL}
	head := "1111111111111111111111111111111111111111"
	s.Deployment = DeploymentTarget{Repository: "o/r", Commit: head}
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	viewer := inviteAs(t, s, admin, "deploy-viewer@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "deploy-track", Name: "deploy"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})

	run := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "deploy-1", Title: "배포 추적", Task: "배포 추적 기능"}).(StartTaskResult).Run
	bad := finishFixture(p.ID, run.ID)
	bad.PullRequests = []string{"https://example.com/o/r/pull/1"}
	_, err := call(t, tools, admin, "finish_task", bad)
	denied(t, err, "INVALID_INPUT")
	finish := finishFixture(p.ID, run.ID)
	finish.PullRequests = []string{"https://github.com/O/R/pull/5", "o/other#7", "o/r#5"}
	completion := mustCall(t, tools, admin, "finish_task", finish).(TaskCompletion)
	if strings.Join(completion.PullRequests, " ") != "https://github.com/o/r/pull/5 https://github.com/o/other/pull/7" {
		t.Fatalf("tracked pull requests: %+v", completion.PullRequests)
	}
	if again := mustCall(t, tools, admin, "finish_task", finish).(TaskCompletion); len(again.PullRequests) != 2 {
		t.Fatalf("retry must return the same completion: %+v", again)
	}
	historyID := completion.Documents[0].ID
	original := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: historyID}).(Document)

	// The next task's context lists the unsettled pull requests.
	next := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "deploy-2", Title: "다음 작업", Task: "다음 작업"}).(StartTaskResult).Run
	if next.Context.Deployments == nil || len(next.Context.Deployments.Pending) != 2 || next.Context.Deployments.Pending[0].TaskTitle != "배포 추적" {
		t.Fatalf("pending deployments in context: %+v", next.Context.Deployments)
	}

	// Viewers are rejected before GitHub is called.
	_, err = call(t, tools, viewer, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID})
	denied(t, err, "FORBIDDEN")
	if f.count() != 0 {
		t.Fatal("GitHub was called for an unauthorized check")
	}

	// PR 5 is open; o/other#7 is merged in a repository this server does not
	// deploy, which is final: its history gets a block.
	f.set("/repos/o/r/pulls/5", map[string]any{"state": "open", "merged": false})
	f.set("/repos/o/other/pulls/7", map[string]any{"state": "closed", "merged": true, "merge_commit_sha": "7777777", "merged_at": "2026-11-15T01:00:00Z"})
	r := mustCall(t, tools, admin, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID, TaskID: run.ID}).(DeploymentCheckResult)
	if r.Checked != 2 || r.RecordedHistories != 1 || len(r.PullRequests) != 2 {
		t.Fatalf("first check: %+v", r)
	}
	doc := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: historyID}).(Document)
	body := strings.SplitN(original.Content, "\n", 2)[1]
	if doc.Revision != 2 || !strings.Contains(doc.Content, "> **상태 갱신") || !strings.Contains(doc.Content, "o/other/pull/7: 병합") ||
		!strings.Contains(doc.Content, "o/r/pull/5: 아직 열려 있음") || !strings.HasSuffix(doc.Content, strings.TrimLeft(body, "\n")) {
		t.Fatalf("first block: rev %d\n%s", doc.Revision, doc.Content)
	}

	// Merged but not in the running build yet: nothing new to record.
	f.set("/repos/o/r/pulls/5", map[string]any{"state": "closed", "merged": true, "merge_commit_sha": "5555555", "merged_at": "2026-11-15T02:00:00Z"})
	f.setCompare("/repos/o/r/compare/"+head+"...5555555", "ahead")
	r = mustCall(t, tools, admin, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID}).(DeploymentCheckResult)
	if r.Checked != 1 || r.RecordedHistories != 0 {
		t.Fatalf("merged, not deployed: %+v", r)
	}
	// The running build contains the merge commit: deployed and recorded.
	f.setCompare("/repos/o/r/compare/"+head+"...5555555", "behind")
	r = mustCall(t, tools, admin, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID}).(DeploymentCheckResult)
	if r.Checked != 1 || r.RecordedHistories != 1 {
		t.Fatalf("deployed: %+v", r)
	}
	doc = mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: historyID}).(Document)
	if doc.Revision != 3 || strings.Count(doc.Content, "> **상태 갱신") != 2 || !strings.Contains(doc.Content, "운영 배포 확인") ||
		strings.Index(doc.Content, "운영 배포 확인") > strings.Index(doc.Content, "o/r/pull/5: 아직 열려 있음") {
		t.Fatalf("second block must sit above the first: rev %d\n%s", doc.Revision, doc.Content)
	}

	// Everything is settled: no more GitHub calls, nothing in the context.
	calls := f.count()
	r = mustCall(t, tools, admin, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID}).(DeploymentCheckResult)
	if r.Checked != 0 || r.RecordedHistories != 0 || f.count() != calls {
		t.Fatalf("settled: %+v", r)
	}
	later := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "deploy-3", Title: "그 다음", Task: "그 다음"}).(StartTaskResult).Run
	if later.Context.Deployments != nil {
		t.Fatalf("settled pull requests must leave the context: %+v", later.Context.Deployments)
	}

	// The background pass uses the same path; a closed pull request is final.
	run2 := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "deploy-4", Title: "닫힌 PR", Task: "닫힌 PR"}).(StartTaskResult).Run
	finish2 := finishFixture(p.ID, run2.ID)
	finish2.PullRequests = []string{"o/r#8"}
	c2 := mustCall(t, tools, admin, "finish_task", finish2).(TaskCompletion)
	f.set("/repos/o/r/pulls/8", map[string]any{"state": "closed", "merged": false})
	if n, err := s.checkDeployments(t.Context(), deploymentScope{Limit: deploymentCheckBatch}); err != nil || n != 1 {
		t.Fatalf("background check: %d %v", n, err)
	}
	if n, err := s.recordDeployments(t.Context(), "", ""); err != nil || n != 1 {
		t.Fatalf("background record: %d %v", n, err)
	}
	doc = mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: c2.Documents[0].ID}).(Document)
	if !strings.Contains(doc.Content, "병합되지 않고 닫힘") {
		t.Fatalf("closed block: %s", doc.Content)
	}
	// A recent check is not repeated by the background pass.
	if n, err := s.checkDeployments(t.Context(), deploymentScope{Limit: deploymentCheckBatch}); err != nil || n != 0 {
		t.Fatalf("recheck too soon: %d %v", n, err)
	}
}

// A pull request GitHub never reported (merged before the repository's
// webhook existed, private repository) is confirmed by hand in the web console.
func TestConfirmPullRequest(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	f, srv := newFakeGitHub(t)
	s.GitHub = &GitHubClient{BaseURL: srv.URL}
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "confirm-editor@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "confirm-pr", Name: "confirm"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	run := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "confirm-1", Title: "비공개 PR", Task: "비공개 저장소 작업"}).(StartTaskResult).Run
	finish := finishFixture(p.ID, run.ID)
	finish.PullRequests = []string{"o/private#4", "o/private#5"}
	completion := mustCall(t, tools, admin, "finish_task", finish).(TaskCompletion)
	historyID := completion.Documents[0].ID

	// The private repository is invisible without credentials.
	r := mustCall(t, tools, admin, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID}).(DeploymentCheckResult)
	if len(r.PullRequests) != 2 || r.PullRequests[0].CheckError == nil || *r.PullRequests[0].CheckError != "NOT_FOUND" {
		t.Fatalf("private repository: %+v", r.PullRequests)
	}

	in := PullRequestConfirmInput{ProjectID: p.ID, TaskID: run.ID, Repository: "O/private", Number: 4, State: "merged"}
	_, err := call(t, tools, editor, "confirm_pull_request", in)
	denied(t, err, "FORBIDDEN")
	keyOut, err := s.CreateKey(ctx, admin, KeyInput{Name: "rw", Access: "read_write", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "confirm_pull_request", in)
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, admin, "confirm_pull_request", PullRequestConfirmInput{ProjectID: p.ID, TaskID: run.ID, Repository: "o/private", Number: 4, State: "deployed"})
	denied(t, err, "INVALID_INPUT")
	_, err = call(t, tools, admin, "confirm_pull_request", PullRequestConfirmInput{ProjectID: p.ID, TaskID: run.ID, Repository: "o/private", Number: 99, State: "merged"})
	denied(t, err, "NOT_FOUND")

	got := mustCall(t, tools, admin, "confirm_pull_request", in).(TrackedPullRequest)
	if got.State != "merged" || got.ConfirmedAt == nil || got.ConfirmedByName == nil || got.CheckError != nil {
		t.Fatalf("confirmed: %+v", got)
	}
	// Settled pull requests cannot be confirmed again.
	_, err = call(t, tools, admin, "confirm_pull_request", in)
	denied(t, err, "CONFLICT")
	mustCall(t, tools, admin, "confirm_pull_request", PullRequestConfirmInput{ProjectID: p.ID, TaskID: run.ID, Repository: "o/private", Number: 5, State: "closed"})

	doc := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: historyID}).(Document)
	if !strings.Contains(doc.Content, "o/private/pull/4: 병합 — ") || !strings.Contains(doc.Content, "o/private/pull/5: 병합되지 않고 닫힘 — ") ||
		!strings.Contains(doc.Content, "수동 확인") || !strings.Contains(doc.Content, "Asia/Seoul, 웹 수동 확인)") {
		t.Fatalf("history after confirmation:\n%s", doc.Content)
	}
	// Nothing is left to check on GitHub.
	calls := f.count()
	mustCall(t, tools, admin, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID})
	if f.count() != calls {
		t.Fatal("confirmed pull requests were checked on GitHub again")
	}
}
