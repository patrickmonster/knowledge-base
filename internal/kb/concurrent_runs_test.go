package kb

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestNormalizeScope(t *testing.T) {
	got, err := normalizeScope(" refs/heads/feat/x ", " /work//tree/ ", []string{"./internal/kb/", "internal\\kb", "/web/app.js", ".", "internal/kb"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "feat/x" || got.Worktree != "/work/tree" || strings.Join(got.Paths, ",") != "internal/kb,web/app.js,." {
		t.Fatalf("normalized: %+v", got)
	}
	if empty, err := normalizeScope("", "  ", nil); err != nil || !empty.empty() {
		t.Fatalf("empty scope: %+v %v", empty, err)
	}
	many := make([]string, maxScopePaths+1)
	for i := range many {
		many[i] = "a"
	}
	for name, c := range map[string]struct {
		branch, worktree string
		paths            []string
	}{
		"branch spaces":   {branch: "a b"},
		"branch control":  {branch: "a\x00b"},
		"branch long":     {branch: strings.Repeat("a", maxScopeBranch+1)},
		"worktree long":   {worktree: strings.Repeat("a", maxScopeWorktree+1)},
		"worktree ctrl":   {worktree: "a\nb"},
		"too many paths":  {paths: many},
		"empty path":      {paths: []string{"  "}},
		"parent path":     {paths: []string{"a/../b"}},
		"long path":       {paths: []string{strings.Repeat("a", maxScopePath+1)}},
		"control in path": {paths: []string{"a\tb"}},
	} {
		if _, err := normalizeScope(c.branch, c.worktree, c.paths); err == nil {
			t.Errorf("%s: want INVALID_INPUT", name)
		} else {
			denied(t, err, "INVALID_INPUT")
		}
	}
}

func TestScopeOverlaps(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"internal/kb", "internal/kb", true},
		{"internal/kb", "internal/kb/a.go", true},
		{"internal/kb/a.go", "internal/kb", true},
		{"internal/kb", "internal/kbx", false},
		{"internal/kb", "web", false},
		{".", "web/app.js", true},
	} {
		if pathsOverlap(c.a, c.b) != c.want {
			t.Errorf("pathsOverlap(%q,%q) != %v", c.a, c.b, c.want)
		}
	}
	mine := TaskScope{Branch: "b", Worktree: "/w", Paths: []string{"a/b"}}
	if o := scopeOverlaps(mine, TaskScope{Branch: "c", Worktree: "/x", Paths: []string{"a/c"}}); len(o) != 0 {
		t.Fatalf("no overlap expected: %+v", o)
	}
	o := scopeOverlaps(mine, TaskScope{Branch: "b", Worktree: "/w", Paths: []string{"a"}})
	if len(o) != 3 || o[0].Kind != "branch" || o[1].Kind != "worktree" || o[2].Kind != "path" || o[2].Value != "a/b" || o[2].Theirs != "a" {
		t.Fatalf("overlaps: %+v", o)
	}
	// An empty scope never matches another empty scope.
	if o := scopeOverlaps(TaskScope{}, TaskScope{}); len(o) != 0 {
		t.Fatalf("empty: %+v", o)
	}
}

func TestStartTaskConcurrentRuns(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, owner, "concurrent-editor@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "concurrent", Name: "concurrent"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	other := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "concurrent-other", Name: "other"}).(Project)

	start := func(id Identity, key string, in StartTaskInput) TaskRun {
		in.ProjectID, in.RequestKey, in.Title, in.Task = p.ID, key, "작업 "+key, "동시 작업 경고 확인 "+key
		return mustCall(t, tools, id, "start_task", in).(StartTaskResult).Run
	}

	// Backward compatible: without scope there is no warning.
	first := start(owner, "first", StartTaskInput{})
	if first.Context.ConcurrentRuns != nil || first.Branch != "" || len(first.Paths) != 0 {
		t.Fatalf("no scope: %+v", first)
	}
	a := start(owner, "a", StartTaskInput{Branch: "feat/x", Worktree: "/work/a", Paths: []string{"internal/kb"}})
	if a.Branch != "feat/x" || a.Worktree != "/work/a" || len(a.Paths) != 1 || a.Context.ConcurrentRuns != nil {
		t.Fatalf("first scoped task: %+v", a)
	}
	// Tasks of another project never match.
	mustCall(t, tools, owner, "start_task", StartTaskInput{ProjectID: other.ID, RequestKey: "o1", Title: "다른 프로젝트", Task: "다른 프로젝트 작업", Branch: "feat/x"})

	// Same branch, same worktree and a path inside a.Paths: all reported, nothing blocked.
	b := start(editor, "b", StartTaskInput{Branch: "refs/heads/feat/x", Worktree: "/work/a/", Paths: []string{"internal/kb/workflow.go", "web"}})
	runs := b.Context.ConcurrentRuns
	if len(runs) != 1 || runs[0].TaskID != a.ID || runs[0].Stale || runs[0].CreatedBy != owner.User.ID || runs[0].CreatedByName == "" || runs[0].StartedAt == "" || runs[0].Title != a.Title {
		t.Fatalf("warning for b: %+v", runs)
	}
	kinds := map[string]bool{}
	for _, o := range runs[0].Overlaps {
		kinds[o.Kind] = true
		if o.Kind == "path" && (o.Value != "internal/kb/workflow.go" || o.Theirs != "internal/kb") {
			t.Fatalf("path overlap: %+v", o)
		}
	}
	if !kinds["branch"] || !kinds["worktree"] || !kinds["path"] || len(runs[0].Overlaps) != 3 {
		t.Fatalf("overlaps: %+v", runs[0].Overlaps)
	}
	// get_task_run returns the stored warning.
	got := mustCall(t, tools, editor, "get_task_run", TaskRef{ProjectID: p.ID, TaskID: b.ID}).(TaskRun)
	if len(got.Context.ConcurrentRuns) != 1 || got.Branch != "feat/x" {
		t.Fatalf("stored: %+v", got)
	}

	// A retry returns the same run: no self warning, and the snapshot stays.
	retry := mustCall(t, tools, editor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "b", Title: "작업 b", Task: "동시 작업 경고 확인 b", Branch: "feat/x", Worktree: "/work/a", Paths: []string{"internal/kb/workflow.go", "web"}}).(StartTaskResult).Run
	if retry.ID != b.ID || len(retry.Context.ConcurrentRuns) != 1 || retry.Context.ConcurrentRuns[0].TaskID != a.ID {
		t.Fatalf("retry: %+v", retry.Context.ConcurrentRuns)
	}
	// A retry with a different scope conflicts like any other changed input.
	_, err := call(t, tools, editor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "b", Title: "작업 b", Task: "동시 작업 경고 확인 b", Branch: "feat/other"})
	denied(t, err, "CONFLICT")

	// Prefix paths match only at segment boundaries; a sibling directory does not.
	c := start(owner, "c", StartTaskInput{Paths: []string{"internal/kbx", "docs"}})
	if len(c.Context.ConcurrentRuns) != 0 {
		t.Fatalf("sibling path must not match: %+v", c.Context.ConcurrentRuns)
	}
	d := start(owner, "d", StartTaskInput{Paths: []string{"internal"}})
	if len(d.Context.ConcurrentRuns) != 3 {
		t.Fatalf("enclosing path matches a, b and c: %+v", d.Context.ConcurrentRuns)
	}

	// Old tasks are shown but marked stale; closed and finished ones are not shown.
	if _, err := s.DB.ExecContext(context.Background(), `UPDATE task_runs SET started_at=now()-interval '10 days' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	mustCall(t, tools, owner, "close_task_run", CloseTaskInput{ProjectID: p.ID, TaskID: c.ID, Reason: "다른 곳에서 처리"})
	e := start(editor, "e", StartTaskInput{Branch: "feat/x", Paths: []string{"internal/kbx"}})
	if len(e.Context.ConcurrentRuns) != 3 {
		t.Fatalf("a, b and d (closed c is not listed): %+v", e.Context.ConcurrentRuns)
	}
	byID := map[string]ConcurrentRun{}
	for _, r := range e.Context.ConcurrentRuns {
		byID[r.TaskID] = r
	}
	if !byID[a.ID].Stale || byID[a.ID].AgeDays < 10 || byID[b.ID].Stale {
		t.Fatalf("stale flags: %+v", e.Context.ConcurrentRuns)
	}
	mustCall(t, tools, owner, "finish_task", finishFixture(p.ID, a.ID))
	f := start(owner, "f", StartTaskInput{Branch: "feat/x"})
	for _, r := range f.Context.ConcurrentRuns {
		if r.TaskID == a.ID {
			t.Fatalf("finished task must not be listed: %+v", f.Context.ConcurrentRuns)
		}
	}
	if len(f.Context.ConcurrentRuns) != 2 {
		t.Fatalf("b and e share the branch (d has no branch): %+v", f.Context.ConcurrentRuns)
	}

	// Input validation goes through the tool.
	_, err = call(t, tools, owner, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "bad", Title: "x", Task: "x", Paths: []string{"../etc"}})
	denied(t, err, "INVALID_INPUT")

	// The next steps and server instructions mention the warning.
	steps := strings.Join(mustCall(t, tools, owner, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "g", Title: "작업 g", Task: "동시 작업 경고 확인 g"}).(StartTaskResult).NextSteps, "\n")
	if !strings.Contains(steps, "concurrent_runs") || !strings.Contains(serverInstructions, "concurrent_runs") {
		t.Fatal("guidance must mention concurrent_runs")
	}
}

// AGENTS.md and the get_agents_template output carry the same concurrent_runs line.
func TestAgentsGuidanceMentionsConcurrentRuns(t *testing.T) {
	const want = "`run.context.concurrent_runs`"
	raw, err := templateFiles.ReadFile("templates/agents.md")
	if err != nil || !strings.Contains(string(raw), want) {
		t.Fatalf("template: %v", err)
	}
	repo, err := os.ReadFile("../../AGENTS.md")
	if err != nil || !strings.Contains(string(repo), want) {
		t.Fatalf("AGENTS.md: %v", err)
	}
	var line string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.Contains(l, want) {
			line = l
		}
	}
	if !strings.Contains(string(repo), line) {
		t.Fatal("AGENTS.md and the template differ on the start_task line")
	}
}
