package kb

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNormalizeTodoRelations(t *testing.T) {
	a, b := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	in := SaveTodoInput{TodoID: a, ParentID: ptr(" " + b + " "), BlockedBy: &[]string{b, " " + b}}
	if err := normalizeTodoRelations(&in); err != nil {
		t.Fatal(err)
	}
	if *in.ParentID != b || len(*in.BlockedBy) != 1 {
		t.Fatalf("normalized: %v %v", *in.ParentID, *in.BlockedBy)
	}
	clear := SaveTodoInput{ParentID: ptr(""), BlockedBy: &[]string{}}
	if err := normalizeTodoRelations(&clear); err != nil || *clear.ParentID != "" || len(*clear.BlockedBy) != 0 {
		t.Fatalf("clear: %v", err)
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = "00000000-0000-4000-8000-0000000000" + string(rune('a'+i/10)) + string(rune('0'+i%10))
	}
	for name, bad := range map[string]SaveTodoInput{
		"parent id":    {ParentID: ptr("nope")},
		"self parent":  {TodoID: a, ParentID: ptr(a)},
		"blocker id":   {BlockedBy: &[]string{"nope"}},
		"self blocker": {TodoID: a, BlockedBy: &[]string{a}},
		"too many":     {BlockedBy: &many},
	} {
		if err := normalizeTodoRelations(&bad); errorCode(err) != "INVALID_INPUT" {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestTodoRelations(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	ctx := context.Background()
	owner := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "todo-relations", Name: "todo relations"}).(Project)
	other := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "todo-relations-other", Name: "other"}).(Project)
	save := func(in SaveTodoInput) Todo {
		t.Helper()
		in.ProjectID = p.ID
		return mustCall(t, tools, owner, "save_todo", in).(Todo)
	}
	get := func(id string) Todo {
		t.Helper()
		got, err := getTodo(ctx, s.DB, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// A decision to make, and a test that waits for it, both under one epic.
	epic := save(SaveTodoInput{Title: "백신 검사 도입"})
	decide := save(SaveTodoInput{Title: "ClamAV 결정", ParentID: &epic.ID})
	test := save(SaveTodoInput{Title: "EICAR 시험", ParentID: &epic.ID, BlockedBy: &[]string{decide.ID}})
	if test.ParentID != epic.ID || test.ParentTitle != "백신 검사 도입" || len(test.BlockedBy) != 1 || test.BlockedBy[0].ID != decide.ID ||
		test.BlockedBy[0].Title != "ClamAV 결정" || test.OpenBlockerCount != 1 {
		t.Fatalf("relations: %+v", test)
	}
	if e := get(epic.ID); e.SubtaskCount != 2 || e.SubtasksCompleted != 0 || len(e.BlockedBy) != 0 {
		t.Fatalf("epic: %+v", e)
	}

	// Saving without the relation fields keeps them.
	test = mustCall(t, tools, owner, "save_todo", SaveTodoInput{ProjectID: p.ID, TodoID: test.ID, ExpectedRevision: test.Revision, Title: "EICAR 파일로 시험"}).(Todo)
	if test.ParentID != epic.ID || len(test.BlockedBy) != 1 {
		t.Fatalf("kept: %+v", test)
	}
	// Completing the predecessor clears the wait and counts the sub-item done.
	mustCall(t, tools, owner, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: decide.ID, Status: "completed", Priority: "medium"})
	if got := get(test.ID); got.OpenBlockerCount != 0 || got.BlockedBy[0].Status != "completed" {
		t.Fatalf("after completion: %+v", got)
	}
	if e := get(epic.ID); e.SubtaskCount != 2 || e.SubtasksCompleted != 1 {
		t.Fatalf("epic progress: %+v", e)
	}
	// Moving a to-do whose predecessor is open is allowed: relations only inform.
	mustCall(t, tools, owner, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: epic.ID, Status: "in_progress", Priority: "medium"})

	// Rejected: a second level, a parent with sub-items, other projects, cycles.
	reject := func(name string, in SaveTodoInput) {
		t.Helper()
		in.ProjectID = p.ID
		if in.TodoID != "" {
			in.ExpectedRevision = get(in.TodoID).Revision
			if in.Title == "" {
				in.Title = get(in.TodoID).Title
			}
		}
		if _, err := call(t, tools, owner, "save_todo", in); errorCode(err) != "INVALID_INPUT" {
			t.Errorf("%s: got %v", name, err)
		}
	}
	reject("second level", SaveTodoInput{Title: "x", ParentID: &test.ID})
	loose := save(SaveTodoInput{Title: "다른 일"})
	reject("parent with sub-items", SaveTodoInput{TodoID: epic.ID, ParentID: &loose.ID})
	foreign := mustCall(t, tools, owner, "save_todo", SaveTodoInput{ProjectID: other.ID, Title: "남의 일"}).(Todo)
	reject("foreign parent", SaveTodoInput{Title: "x", ParentID: &foreign.ID})
	reject("foreign blocker", SaveTodoInput{Title: "x", BlockedBy: &[]string{foreign.ID}})
	reject("missing blocker", SaveTodoInput{Title: "x", BlockedBy: &[]string{"33333333-3333-4333-8333-333333333333"}})
	reject("direct cycle", SaveTodoInput{TodoID: decide.ID, BlockedBy: &[]string{test.ID}})
	chain := save(SaveTodoInput{Title: "배포", BlockedBy: &[]string{test.ID}})
	reject("indirect cycle", SaveTodoInput{TodoID: decide.ID, BlockedBy: &[]string{chain.ID}})
	if n := len(get(decide.ID).BlockedBy); n != 0 {
		t.Fatalf("rejected save left %d blockers", n)
	}
	// Replacing the list with one that removes the cycle works; [] and "" clear.
	cur := get(test.ID)
	cleared := mustCall(t, tools, owner, "save_todo", SaveTodoInput{ProjectID: p.ID, TodoID: test.ID, ExpectedRevision: cur.Revision, Title: cur.Title, ParentID: ptr(""), BlockedBy: &[]string{}}).(Todo)
	if cleared.ParentID != "" || len(cleared.BlockedBy) != 0 {
		t.Fatalf("cleared: %+v", cleared)
	}
	cur = get(decide.ID)
	mustCall(t, tools, owner, "save_todo", SaveTodoInput{ProjectID: p.ID, TodoID: decide.ID, ExpectedRevision: cur.Revision, Title: cur.Title, Status: cur.Status, BlockedBy: &[]string{chain.ID}})
	mustCall(t, tools, owner, "save_todo", SaveTodoInput{ProjectID: p.ID, TodoID: test.ID, ExpectedRevision: cleared.Revision, Title: cleared.Title, ParentID: &epic.ID})

	// Export and import keep the relations between imported to-dos.
	export, err := s.ExportProject(ctx, owner, p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	data, err := parseImport(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	target := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "todo-relations-copy", Name: "copy"}).(Project)
	result, err := s.ImportProject(ctx, owner, target.ID, data)
	if err != nil || result.TodosCreated != 5 {
		t.Fatalf("import: %+v %v", result, err)
	}
	byTitle := map[string]Todo{}
	for _, td := range mustCall(t, tools, owner, "list_todos", ListTodosInput{ProjectID: target.ID}).([]Todo) {
		byTitle[td.Title] = td
	}
	copiedEpic, copiedTest, copiedDecide := byTitle["백신 검사 도입"], byTitle["EICAR 파일로 시험"], byTitle["ClamAV 결정"]
	if copiedEpic.SubtaskCount != 2 || copiedTest.ParentID != copiedEpic.ID || len(copiedDecide.BlockedBy) != 1 || copiedDecide.BlockedBy[0].ID != byTitle["배포"].ID {
		t.Fatalf("imported relations: %+v", byTitle)
	}

	// Deleting a parent lifts its sub-items to the top; deleting a predecessor drops the wait.
	mustCall(t, tools, owner, "delete_todo", TodoRef{ProjectID: p.ID, TodoID: epic.ID})
	if got := get(test.ID); got.ParentID != "" {
		t.Fatalf("orphan: %+v", got)
	}
	mustCall(t, tools, owner, "delete_todo", TodoRef{ProjectID: p.ID, TodoID: chain.ID})
	if got := get(decide.ID); len(got.BlockedBy) != 0 || got.OpenBlockerCount != 0 {
		t.Fatalf("dangling blocker: %+v", got)
	}
}
