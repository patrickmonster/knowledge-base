package kb

import (
	"context"
	"testing"
)

func TestStaleTodos(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	viewer := inviteAs(t, s, owner, "stale-viewer@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "stale-todos", Name: "stale todos"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})

	save := func(title, status string) Todo {
		return mustCall(t, tools, owner, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: title, Status: status, AssigneeID: owner.User.ID}).(Todo)
	}
	age := func(id string, days int) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx, `UPDATE todos SET updated_at=now()-make_interval(days=>$2) WHERE id=$1`, id, days); err != nil {
			t.Fatal(err)
		}
	}
	oldest := save("오래 멈춘 카드", "in_progress")
	old := save("검토 대기 카드", "in_review")
	fresh := save("방금 만든 카드", "not_started")
	idea := save("오래된 아이디어", "idea")
	done := save("오래전 완료", "completed")
	busy := save("작업이 이어진 카드", "not_started")
	age(oldest.ID, 40)
	age(old.ID, 20)
	age(idea.ID, 90)
	age(done.ID, 90)
	age(busy.ID, 30)
	// A task run linked recently counts as activity even though the card is old.
	run := mustCall(t, tools, owner, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "stale-busy", Title: "이어진 작업", Task: "이어진 작업"}).(StartTaskResult).Run
	mustCall(t, tools, owner, "link_task_todo", LinkTaskTodoInput{ProjectID: p.ID, TaskID: run.ID, TodoID: busy.ID})
	age(busy.ID, 30)

	got := mustCall(t, tools, viewer, "list_stale_todos", ListStaleTodosInput{ProjectID: p.ID}).(StaleTodos)
	if got.OlderThanDays != defaultStaleTodoDays || got.Total != 2 || len(got.Todos) != 2 {
		t.Fatalf("stale to-dos: %+v", got)
	}
	if got.Todos[0].ID != oldest.ID || got.Todos[1].ID != old.ID || got.Todos[0].IdleDays < 39 || got.Todos[0].AssigneeName == "" || got.Todos[0].LastActivityAt == "" {
		t.Fatalf("oldest first with idle days: %+v", got.Todos)
	}
	for _, td := range got.Todos {
		if td.ID == fresh.ID || td.ID == idea.ID || td.ID == done.ID || td.ID == busy.ID {
			t.Fatalf("fresh, idea, completed and recently linked to-dos are not stale: %+v", got.Todos)
		}
	}
	if longer := mustCall(t, tools, viewer, "list_stale_todos", ListStaleTodosInput{ProjectID: p.ID, OlderThanDays: 30}).(StaleTodos); longer.Total != 1 || longer.Todos[0].ID != oldest.ID {
		t.Fatalf("older_than_days 30: %+v", longer)
	}
	if limited := mustCall(t, tools, viewer, "list_stale_todos", ListStaleTodosInput{ProjectID: p.ID, Limit: 1}).(StaleTodos); limited.Total != 2 || len(limited.Todos) != 1 {
		t.Fatalf("limit keeps total: %+v", limited)
	}
	for _, bad := range []ListStaleTodosInput{{ProjectID: p.ID, OlderThanDays: 366}, {ProjectID: p.ID, OlderThanDays: -1}, {ProjectID: p.ID, Limit: 201}} {
		_, err := call(t, tools, viewer, "list_stale_todos", bad)
		denied(t, err, "INVALID_INPUT")
	}

	// Task context mentions them; moving a card clears it from the list.
	res := mustCall(t, tools, owner, "get_task_context", ContextInput{ProjectID: p.ID, Task: "보드 정리"}).(TaskContext)
	if res.Todos == nil || res.Todos.Stale == nil || res.Todos.Stale.Total != 2 || res.Todos.Stale.Todos[0].ID != oldest.ID {
		t.Fatalf("context stale: %+v", res.Todos)
	}
	mustCall(t, tools, owner, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: oldest.ID, Status: "in_progress", Priority: oldest.Priority})
	mustCall(t, tools, owner, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: old.ID, Status: "completed", Priority: old.Priority})
	res = mustCall(t, tools, owner, "get_task_context", ContextInput{ProjectID: p.ID, Task: "보드 정리"}).(TaskContext)
	if res.Todos.Stale != nil {
		t.Fatalf("no stale to-dos left: %+v", res.Todos.Stale)
	}
}
