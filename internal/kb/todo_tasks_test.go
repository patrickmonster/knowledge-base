package kb

import "testing"

func TestTodoTaskLink(t *testing.T) {
	s := testStore(t)
	f, srv := newFakeGitHub(t)
	s.GitHub = &GitHubClient{BaseURL: srv.URL}
	head := "2222222222222222222222222222222222222222"
	s.Deployment = DeploymentTarget{Repository: "o/r", Commit: head}
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, owner, "link-editor@example.com")
	other := inviteAs(t, s, owner, "link-other@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "todo-link", Name: "todo link"}).(Project)
	for _, u := range []Identity{editor, other} {
		mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: u.User.ID, Role: "editor"})
	}
	half := 40
	board := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "환불 화면 개선", Category: "Work", Progress: &half}).(Todo)
	idea := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "결제 알림 정리", Status: "idea", Priority: "low"}).(Todo)
	done := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "환불 정책 문서", Status: "completed"}).(Todo)

	// start_task with todo_id links it and moves it to in_progress.
	start := StartTaskInput{ProjectID: p.ID, RequestKey: "link-1", Title: "환불 화면", Task: "환불 화면 개선 작업", TodoID: board.ID}
	run := mustCall(t, tools, editor, "start_task", start).(StartTaskResult).Run
	if run.TodoID != board.ID || run.Context.Todos == nil || run.Context.Todos.Linked == nil || run.Context.Todos.Linked.Status != "in_progress" {
		t.Fatalf("linked at start: %+v", run.Context.Todos)
	}
	for _, c := range run.Context.Todos.Candidates {
		if c.ID == board.ID || c.ID == done.ID {
			t.Fatalf("candidates must skip the linked and completed to-dos: %+v", run.Context.Todos.Candidates)
		}
	}
	// Retries: the same to-do or none is fine; another to-do conflicts.
	mustCall(t, tools, editor, "start_task", start)
	retry := start
	retry.TodoID = ""
	mustCall(t, tools, editor, "start_task", retry)
	retry.TodoID = idea.ID
	_, err := call(t, tools, editor, "start_task", retry)
	denied(t, err, "CONFLICT")
	bad := StartTaskInput{ProjectID: p.ID, RequestKey: "link-bad", Title: "x", Task: "x", TodoID: "00000000-0000-4000-8000-000000000000"}
	_, err = call(t, tools, editor, "start_task", bad)
	denied(t, err, "INVALID_INPUT")

	// Context candidates come from words shared with the task.
	cand := mustCall(t, tools, other, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "link-2", Title: "결제 알림", Task: "결제 알림 메시지 정리"}).(StartTaskResult).Run
	if cand.TodoID != "" || len(cand.Context.Todos.Candidates) == 0 || cand.Context.Todos.Candidates[0].ID != idea.ID {
		t.Fatalf("candidates: %+v", cand.Context.Todos)
	}
	// Only the creator, owner or admin links; linking moves an idea to in_progress.
	_, err = call(t, tools, editor, "link_task_todo", LinkTaskTodoInput{ProjectID: p.ID, TaskID: cand.ID, TodoID: idea.ID})
	denied(t, err, "FORBIDDEN")
	mustCall(t, tools, other, "link_task_todo", LinkTaskTodoInput{ProjectID: p.ID, TaskID: cand.ID, TodoID: idea.ID})
	list := mustCall(t, tools, editor, "list_todos", ListTodosInput{ProjectID: p.ID, Status: "in_progress"}).([]Todo)
	if len(list) != 2 {
		t.Fatalf("in progress after linking: %+v", list)
	}
	for _, td := range list {
		if td.TaskCount != 1 || td.OpenTaskCount != 1 {
			t.Fatalf("task counts: %+v", td)
		}
	}
	runs := mustCall(t, tools, editor, "list_task_runs", TaskListInput{ProjectID: p.ID, TodoID: idea.ID}).([]map[string]any)
	if len(runs) != 1 || runs[0]["id"] != cand.ID || runs[0]["todo"].(map[string]any)["title"] != idea.Title {
		t.Fatalf("runs by to-do: %+v", runs)
	}

	// finish_task completed completes the to-do and fills its progress.
	completion := mustCall(t, tools, editor, "finish_task", finishFixture(p.ID, run.ID)).(TaskCompletion)
	if completion.Todo == nil || completion.Todo.Status != "completed" || completion.Todo.Progress == nil || *completion.Todo.Progress != 100 {
		t.Fatalf("completed to-do: %+v", completion.Todo)
	}
	_, err = call(t, tools, editor, "link_task_todo", LinkTaskTodoInput{ProjectID: p.ID, TaskID: run.ID, TodoID: idea.ID})
	denied(t, err, "CONFLICT")

	// A partial finish keeps the to-do in progress until its pull request lands.
	partial := finishFixture(p.ID, cand.ID)
	partial.Outcome = "partial"
	partial.PullRequests = []string{"o/r#9"}
	pc := mustCall(t, tools, other, "finish_task", partial).(TaskCompletion)
	if pc.Todo == nil || pc.Todo.Status != "in_progress" {
		t.Fatalf("partial keeps the to-do: %+v", pc.Todo)
	}
	f.set("/repos/o/r/pulls/9", map[string]any{"state": "closed", "merged": true, "merge_commit_sha": "9999999", "merged_at": "2026-11-15T02:00:00Z"})
	f.setCompare("/repos/o/r/compare/"+head+"...9999999", "ahead")
	mustCall(t, tools, other, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID})
	if td := mustCall(t, tools, other, "list_todos", ListTodosInput{ProjectID: p.ID, Status: "in_progress"}).([]Todo); len(td) != 1 || td[0].ID != idea.ID || len(td[0].PullRequests) != 1 || td[0].PullRequests[0].State != "merged" {
		t.Fatalf("merged, not deployed yet: %+v", td)
	}
	f.setCompare("/repos/o/r/compare/"+head+"...9999999", "behind")
	mustCall(t, tools, other, "check_task_deployments", DeploymentCheckInput{ProjectID: p.ID})
	if td := mustCall(t, tools, other, "list_todos", ListTodosInput{ProjectID: p.ID, Status: "completed"}).([]Todo); len(td) != 3 {
		t.Fatalf("deployed pull request completes the to-do: %+v", td)
	}

	// Deleting a to-do keeps its tasks.
	mustCall(t, tools, editor, "delete_todo", TodoRef{ProjectID: p.ID, TodoID: board.ID})
	got := mustCall(t, tools, editor, "get_task_run", TaskRef{ProjectID: p.ID, TaskID: run.ID}).(TaskRun)
	if got.TodoID != "" || got.Status != "completed" {
		t.Fatalf("task after deleting its to-do: %+v", got)
	}
}

func TestTodoIssueLink(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, owner, "issue-link-editor@example.com")
	viewer := inviteAs(t, s, owner, "issue-link-viewer@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "todo-issues", Name: "todo issues"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	todo := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "정산 화면"}).(Todo)
	other := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "다른 일"}).(Todo)

	// todo_id on create_issue; a question raised from a linked task inherits its to-do.
	direct := mustCall(t, tools, editor, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "정산 기준일", Body: "언제 기준인가요?", TodoID: todo.ID, Blocking: true}).(Issue)
	if direct.TodoID == nil || *direct.TodoID != todo.ID || direct.Todo == nil || direct.Todo.Title != todo.Title {
		t.Fatalf("direct link: %+v", direct)
	}
	run := mustCall(t, tools, editor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "issue-1", Title: "정산", Task: "정산 화면", TodoID: todo.ID}).(StartTaskResult).Run
	if l := run.Context.Todos.Linked; l == nil || l.OpenIssues != 1 || l.BlockingIssues != 1 {
		t.Fatalf("linked to-do issue counts: %+v", run.Context.Todos.Linked)
	}
	inherited := mustCall(t, tools, editor, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "환율", Body: "어느 환율?", TaskID: run.ID}).(Issue)
	if inherited.TodoID == nil || *inherited.TodoID != todo.ID {
		t.Fatalf("inherited link: %+v", inherited)
	}
	plain := mustCall(t, tools, editor, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "무관", Body: "무관한 질의"}).(Issue)
	_, err := call(t, tools, editor, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "x", Body: "x", TodoID: "00000000-0000-4000-8000-000000000000"})
	denied(t, err, "INVALID_INPUT")

	// link_issue_todo: editors link, move and unlink; viewers cannot.
	_, err = call(t, tools, viewer, "link_issue_todo", LinkIssueTodoInput{ProjectID: p.ID, IssueID: plain.ID, TodoID: todo.ID})
	denied(t, err, "FORBIDDEN")
	linked := mustCall(t, tools, editor, "link_issue_todo", LinkIssueTodoInput{ProjectID: p.ID, IssueID: plain.ID, TodoID: other.ID}).(Issue)
	if linked.Todo == nil || linked.Todo.ID != other.ID {
		t.Fatalf("linked: %+v", linked)
	}
	if un := mustCall(t, tools, editor, "link_issue_todo", LinkIssueTodoInput{ProjectID: p.ID, IssueID: plain.ID}).(Issue); un.TodoID != nil || un.Todo != nil {
		t.Fatalf("unlinked: %+v", un)
	}

	// Board counts and the list filter.
	board := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Status: "in_progress"}).([]Todo)
	if len(board) != 1 || board[0].OpenIssueCount != 2 || board[0].BlockingIssueCount != 1 {
		t.Fatalf("board counts: %+v", board)
	}
	if got := mustCall(t, tools, viewer, "list_issues", IssueListInput{ProjectID: p.ID, TodoID: todo.ID, Status: "all"}).([]Issue); len(got) != 2 {
		t.Fatalf("issues by to-do: %+v", got)
	}
	mustCall(t, tools, editor, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: direct.ID, Status: "resolved"})
	board = mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Status: "in_progress"}).([]Todo)
	if board[0].OpenIssueCount != 1 || board[0].BlockingIssueCount != 0 {
		t.Fatalf("counts after resolving: %+v", board[0])
	}

	// Deleting the to-do keeps its questions.
	mustCall(t, tools, editor, "delete_todo", TodoRef{ProjectID: p.ID, TodoID: todo.ID})
	kept := mustCall(t, tools, editor, "get_issue", IssueRef{ProjectID: p.ID, IssueID: inherited.ID}).(Issue)
	if kept.TodoID != nil || kept.Todo != nil {
		t.Fatalf("issue after deleting its to-do: %+v", kept)
	}
}
