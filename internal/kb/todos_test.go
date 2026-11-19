package kb

import "testing"

func TestValidateTodo(t *testing.T) {
	ok := SaveTodoInput{Title: "  Save 500$  "}
	if err := validateTodo(&ok); err != nil {
		t.Fatal(err)
	}
	if ok.Title != "Save 500$" || ok.Status != "not_started" || ok.Priority != "medium" {
		t.Fatalf("defaults: %+v", ok)
	}
	over, under := 101, -1
	for name, in := range map[string]SaveTodoInput{
		"empty title":  {Title: " "},
		"status":       {Title: "x", Status: "done"},
		"priority":     {Title: "x", Priority: "urgent"},
		"progress>100": {Title: "x", Progress: &over},
		"progress<0":   {Title: "x", Progress: &under},
		"assignee":     {Title: "x", AssigneeID: "nope"},
		"due date":     {Title: "x", DueDate: "2026-13-01"},
		"category":     {Title: "x", Category: string(make([]rune, 51))},
	} {
		if err := validateTodo(&in); errorCode(err) != "INVALID_INPUT" {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestTodoBoard(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, owner, "todo-editor@example.com")
	viewer := inviteAs(t, s, owner, "todo-viewer@example.com")
	outsider := inviteAs(t, s, owner, "todo-outsider@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "todo-board", Name: "todo board"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})

	half := 50
	a := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "Find a job", Status: "in_progress", Priority: "high", Category: "Work", Progress: &half, AssigneeID: editor.User.ID, DueDate: "2026-12-01"}).(Todo)
	if a.Revision != 1 || a.Progress == nil || *a.Progress != 50 || a.AssigneeName == "" || a.DueDate != "2026-12-01" || a.CompletedAt != "" {
		t.Fatalf("created: %+v", a)
	}
	b := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "Save 100$", Status: "in_progress", Priority: "high"}).(Todo)
	c := mustCall(t, tools, owner, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "Water the plants"}).(Todo)
	if b.Position <= a.Position || c.Priority != "medium" || c.Status != "not_started" || c.Progress != nil {
		t.Fatalf("placement: a=%v b=%v c=%+v", a.Position, b.Position, c)
	}

	// Viewers read but cannot write; outsiders see nothing; assignees must be members.
	if list := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID}).([]Todo); len(list) != 3 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("list order: %+v", list)
	}
	_, err := call(t, tools, viewer, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "x"})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, outsider, "list_todos", ListTodosInput{ProjectID: p.ID})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "x", AssigneeID: outsider.User.ID})
	denied(t, err, "INVALID_INPUT")

	// Filters.
	if mine := mustCall(t, tools, editor, "list_todos", ListTodosInput{ProjectID: p.ID, Mine: true}).([]Todo); len(mine) != 1 || mine[0].ID != a.ID {
		t.Fatalf("mine: %+v", mine)
	}
	if work := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Category: "work"}).([]Todo); len(work) != 1 {
		t.Fatalf("category: %+v", work)
	}

	// query keeps only to-dos holding every word of the query in their title,
	// body or category. Korean particles are trimmed, so 검색을 finds 검색.
	ko := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "검색 결과 재확인", Body: "보드에서 검색을 지원한다"}).(Todo)
	for name, q := range map[string]string{"title word": "job", "two words": "find job", "category": "work"} {
		if got := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Query: q}).([]Todo); len(got) != 1 || got[0].ID != a.ID {
			t.Errorf("query %s: %+v", name, got)
		}
	}
	if got := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Query: "검색을"}).([]Todo); len(got) != 1 || got[0].ID != ko.ID {
		t.Errorf("query of a word in the body: %+v", got)
	}
	if got := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Query: "job plants"}).([]Todo); len(got) != 0 {
		t.Errorf("query whose words sit on different to-dos: %+v", got)
	}
	// Wildcards and words too short or too common to match leave the list whole.
	if got := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Query: "% a 확인"}).([]Todo); len(got) != 4 {
		t.Errorf("query of stopwords and wildcards: %+v", got)
	}
	if got := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Query: "job", Category: "personal"}).([]Todo); len(got) != 0 {
		t.Errorf("query combined with another filter: %+v", got)
	}
	mustCall(t, tools, editor, "delete_todo", TodoRef{ProjectID: p.ID, TodoID: ko.ID})

	// Moving before another card places it first; completing fills progress.
	moved := mustCall(t, tools, editor, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: b.ID, Status: "in_progress", Priority: "high", BeforeID: a.ID}).(Todo)
	if moved.Position >= a.Position || moved.Revision != 2 {
		t.Fatalf("moved before: %+v", moved)
	}
	done := mustCall(t, tools, editor, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: a.ID, Status: "completed", Priority: "high"}).(Todo)
	if done.Progress == nil || *done.Progress != 100 || done.CompletedAt == "" {
		t.Fatalf("completed: %+v", done)
	}
	_, err = call(t, tools, editor, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: c.ID, Status: "idea", Priority: "low", BeforeID: b.ID})
	denied(t, err, "INVALID_INPUT")
	// A drop into an exhausted gap renumbers the cell instead of failing.
	d := mustCall(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: "Save 500$", Status: "in_progress", Priority: "high"}).(Todo)
	if _, err := s.DB.Exec(`UPDATE todos SET position=CASE WHEN id=$1 THEN 1 ELSE 1.0000000001 END WHERE id IN ($1,$2)`, b.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	between := mustCall(t, tools, editor, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: c.ID, Status: "in_progress", Priority: "high", BeforeID: d.ID}).(Todo)
	cell := mustCall(t, tools, viewer, "list_todos", ListTodosInput{ProjectID: p.ID, Status: "in_progress", Priority: "high"}).([]Todo)
	if len(cell) != 3 || cell[0].ID != b.ID || cell[1].ID != between.ID || cell[2].ID != d.ID {
		t.Fatalf("renumbered cell: %+v", cell)
	}
	mustCall(t, tools, editor, "move_todo", MoveTodoInput{ProjectID: p.ID, TodoID: c.ID, Status: "not_started", Priority: "medium"})

	// Updates need the current revision and replace every field.
	_, err = call(t, tools, editor, "save_todo", SaveTodoInput{ProjectID: p.ID, TodoID: done.ID, ExpectedRevision: 1, Title: "stale"})
	denied(t, err, "CONFLICT")
	reopened := mustCall(t, tools, owner, "save_todo", SaveTodoInput{ProjectID: p.ID, TodoID: done.ID, ExpectedRevision: done.Revision, Title: "Find a better job", Status: "idea", Priority: "low"}).(Todo)
	if reopened.CompletedAt != "" || reopened.Progress != nil || reopened.AssigneeID != "" || reopened.DueDate != "" || reopened.Title != "Find a better job" {
		t.Fatalf("reopened: %+v", reopened)
	}

	_, err = call(t, tools, viewer, "delete_todo", TodoRef{ProjectID: p.ID, TodoID: c.ID})
	denied(t, err, "FORBIDDEN")
	mustCall(t, tools, editor, "delete_todo", TodoRef{ProjectID: p.ID, TodoID: c.ID})
	_, err = call(t, tools, editor, "delete_todo", TodoRef{ProjectID: p.ID, TodoID: c.ID})
	denied(t, err, "NOT_FOUND")
}
