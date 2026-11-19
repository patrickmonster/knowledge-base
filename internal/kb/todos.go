package kb

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
)

// To-dos (할 일) are a project's lightweight work items for the web board: columns
// by status, rows by priority, cards ordered by position inside each cell.
// Every member reads them; editors, admins and the owner change them from the
// web console or a writable MCP key.
const (
	maxTodoList = 500
	// maxProjectTodos caps the to-dos one project holds.
	maxProjectTodos = 5000
	// todoGap spaces positions so a card can be dropped between two others
	// without renumbering the cell; renumbering happens once gaps run out.
	todoGap = 1024.0
)

var (
	todoStatuses   = []string{"not_started", "in_progress", "in_review", "completed", "idea"}
	todoPriorities = []string{"high", "medium", "low"}
)

type Todo struct {
	ID            string  `json:"id"`
	ProjectID     string  `json:"project_id"`
	Title         string  `json:"title"`
	Body          string  `json:"body"`
	Status        string  `json:"status"`
	Priority      string  `json:"priority"`
	Category      string  `json:"category"`
	Progress      *int    `json:"progress"`
	AssigneeID    string  `json:"assignee_id,omitempty"`
	AssigneeName  string  `json:"assignee_name,omitempty"`
	DueDate       string  `json:"due_date,omitempty"`
	Position      float64 `json:"position"`
	Revision      int     `json:"revision"`
	CreatedBy     string  `json:"created_by"`
	CreatedByName string  `json:"created_by_name,omitempty"`
	UpdatedBy     string  `json:"updated_by"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
	CompletedAt   string  `json:"completed_at,omitempty"`
	// Linked task runs (start_task todo_id / link_task_todo) and their pull requests.
	TaskCount     int `json:"task_count"`
	OpenTaskCount int `json:"open_task_count"`
	// Linked questions and issues still waiting (open or answered), and open blocking ones.
	OpenIssueCount     int               `json:"open_issue_count"`
	BlockingIssueCount int               `json:"blocking_issue_count"`
	PullRequests       []TodoPullRequest `json:"pull_requests"`
	// Relations (todo_relations.go): the parent this is a sub-item of, the
	// to-dos it waits for, and how many of each are still open.
	ParentID          string        `json:"parent_id,omitempty"`
	ParentTitle       string        `json:"parent_title,omitempty"`
	BlockedBy         []TodoRelated `json:"blocked_by"`
	OpenBlockerCount  int           `json:"open_blocker_count"`
	SubtaskCount      int           `json:"subtask_count"`
	SubtasksCompleted int           `json:"subtasks_completed"`
}

type TodoPullRequest struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	State      string `json:"state"`
}

type ListTodosInput struct {
	ProjectID  string `json:"project_id"`
	Status     string `json:"status,omitempty" jsonschema:"not_started, in_progress, in_review, completed or idea; omit for all"`
	Priority   string `json:"priority,omitempty" jsonschema:"high, medium or low; omit for all"`
	Category   string `json:"category,omitempty" jsonschema:"Exact category (case-insensitive); omit for all"`
	AssigneeID string `json:"assignee_id,omitempty"`
	Mine       bool   `json:"mine,omitempty" jsonschema:"true lists only to-dos assigned to you"`
	Query      string `json:"query,omitempty" jsonschema:"Words matched against title, body and category; a to-do must hold every word"`
	Limit      int    `json:"limit,omitempty" jsonschema:"1-500, default 500"`
}

type SaveTodoInput struct {
	ProjectID        string `json:"project_id"`
	TodoID           string `json:"todo_id,omitempty" jsonschema:"Omit to add a to-do; set with expected_revision to change one"`
	ExpectedRevision int    `json:"expected_revision,omitempty"`
	Title            string `json:"title"`
	Body             string `json:"body,omitempty"`
	Status           string `json:"status,omitempty" jsonschema:"not_started (default), in_progress, in_review, completed or idea"`
	Priority         string `json:"priority,omitempty" jsonschema:"high, medium (default) or low"`
	Category         string `json:"category,omitempty" jsonschema:"Free label such as Work or Personal, up to 50 characters"`
	Progress         *int   `json:"progress,omitempty" jsonschema:"0-100; omit for no progress bar"`
	AssigneeID       string `json:"assignee_id,omitempty" jsonschema:"A project member's user ID; omit for unassigned"`
	DueDate          string `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD; omit for none"`
	// Relations are kept when omitted, unlike the fields above, so callers
	// that do not know them never clear them by accident.
	ParentID  *string   `json:"parent_id,omitempty" jsonschema:"Make this a sub-item of that to-do (one level: the parent cannot itself be a sub-item); empty string clears; omit to keep"`
	BlockedBy *[]string `json:"blocked_by,omitempty" jsonschema:"IDs of to-dos (same project) this one waits for, at most 20; replaces the list, [] clears; omit to keep. Cycles are rejected"`
}

type MoveTodoInput struct {
	ProjectID string `json:"project_id"`
	TodoID    string `json:"todo_id"`
	Status    string `json:"status" jsonschema:"Target column: not_started, in_progress, in_review, completed or idea"`
	Priority  string `json:"priority" jsonschema:"Target row: high, medium or low"`
	BeforeID  string `json:"before_id,omitempty" jsonschema:"Place before this to-do in the target cell; omit to place last"`
}

type TodoRef struct {
	ProjectID string `json:"project_id"`
	TodoID    string `json:"todo_id"`
}

func oneOf(value string, allowed []string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

// todoSelect returns each to-do with the assignee's and creator's names.
const todoSelect = `SELECT to_jsonb(t)||jsonb_build_object('assignee_name',COALESCE(a.name,''),'created_by_name',COALESCE(c.name,''),
	'task_count',(SELECT count(*) FROM task_runs r WHERE r.todo_id=t.id),
	'open_task_count',(SELECT count(*) FROM task_runs r WHERE r.todo_id=t.id AND r.status='open'),
	'open_issue_count',(SELECT count(*) FROM issues i WHERE i.todo_id=t.id AND i.status IN ('open','answered')),
	'blocking_issue_count',(SELECT count(*) FROM issues i WHERE i.todo_id=t.id AND i.status='open' AND i.blocking),
	'pull_requests',COALESCE((SELECT jsonb_agg(jsonb_build_object('repository',pr.repository,'number',pr.number,'url',pr.url,'state',pr.state) ORDER BY pr.created_at DESC)
		FROM task_pull_requests pr JOIN task_runs r ON r.id=pr.task_id WHERE r.todo_id=t.id),'[]'::jsonb))||` + todoRelationsSelect + `
	FROM todos t LEFT JOIN users a ON a.id=t.assignee_id LEFT JOIN users c ON c.id=t.created_by`

func validateTodo(in *SaveTodoInput) error {
	in.Title = strings.TrimSpace(in.Title)
	in.Category = strings.TrimSpace(in.Category)
	if err := bounded("title", in.Title, 1, 200); err != nil {
		return err
	}
	if len([]rune(in.Body)) > 10000 {
		return problem("INVALID_INPUT", "body must have at most 10000 characters.")
	}
	if len([]rune(in.Category)) > 50 {
		return problem("INVALID_INPUT", "category must have at most 50 characters.")
	}
	if in.Status == "" {
		in.Status = "not_started"
	}
	if !oneOf(in.Status, todoStatuses) {
		return problem("INVALID_INPUT", "status must be not_started, in_progress, in_review, completed or idea.")
	}
	if in.Priority == "" {
		in.Priority = "medium"
	}
	if !oneOf(in.Priority, todoPriorities) {
		return problem("INVALID_INPUT", "priority must be high, medium or low.")
	}
	if in.Progress != nil && (*in.Progress < 0 || *in.Progress > 100) {
		return problem("INVALID_INPUT", "progress must be between 0 and 100.")
	}
	if in.AssigneeID != "" {
		if err := validID(in.AssigneeID); err != nil {
			return err
		}
	}
	if in.DueDate != "" {
		if _, err := time.Parse("2006-01-02", in.DueDate); err != nil {
			return problem("INVALID_INPUT", "due_date must be YYYY-MM-DD.")
		}
	}
	return nil
}

func listTodos(ctx context.Context, tx *sql.Tx, a Identity, in ListTodosInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	if in.Status != "" && !oneOf(in.Status, todoStatuses) {
		return nil, problem("INVALID_INPUT", "status must be not_started, in_progress, in_review, completed or idea.")
	}
	if in.Priority != "" && !oneOf(in.Priority, todoPriorities) {
		return nil, problem("INVALID_INPUT", "priority must be high, medium or low.")
	}
	if in.AssigneeID != "" {
		if err := validID(in.AssigneeID); err != nil {
			return nil, err
		}
	}
	if in.Mine {
		if in.AssigneeID != "" && in.AssigneeID != a.User.ID {
			return nil, problem("INVALID_INPUT", "Use either mine or assignee_id.")
		}
		in.AssigneeID = a.User.ID
	}
	limit := in.Limit
	if limit == 0 {
		limit = maxTodoList
	}
	if limit < 1 || limit > maxTodoList {
		return nil, problem("INVALID_INPUT", "limit must be 1–500.")
	}
	if err := bounded("query", in.Query, 0, 500); err != nil {
		return nil, err
	}
	// Same word matching as list_issues and the task's to-do candidates: the
	// board is not indexed for search, so every word of the query must appear
	// somewhere in the title, body or category.
	terms := issueTerms(in.Query)
	return many[Todo](ctx, tx, todoSelect+` WHERE t.project_id=$1
		AND ($2='' OR t.status=$2) AND ($3='' OR t.priority=$3) AND ($4='' OR lower(t.category)=lower($4))
		AND ($5='' OR t.assignee_id=$5::uuid)
		AND NOT EXISTS (SELECT 1 FROM unnest($7::text[]) w WHERE (t.title||' '||t.body||' '||t.category) NOT ILIKE '%'||w||'%')
		ORDER BY CASE t.priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END,
		CASE t.status WHEN 'not_started' THEN 0 WHEN 'in_progress' THEN 1 WHEN 'in_review' THEN 2 WHEN 'completed' THEN 3 ELSE 4 END,
		t.position, t.created_at, t.id LIMIT $6`, p.ID, in.Status, in.Priority, strings.TrimSpace(in.Category), in.AssigneeID, limit, terms)
}

func todoAssignee(ctx context.Context, tx *sql.Tx, projectID, userID string) error {
	if userID == "" {
		return nil
	}
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_members m JOIN users u ON u.id=m.user_id
		WHERE m.project_id=$1 AND m.user_id=$2 AND NOT u.disabled)`, projectID, userID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return problem("INVALID_INPUT", "assignee_id must be an active project member.")
	}
	return nil
}

func lockTodo(ctx context.Context, tx *sql.Tx, projectID, todoID string) (Todo, error) {
	if err := validID(todoID); err != nil {
		return Todo{}, err
	}
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM todos WHERE id=$1 AND project_id=$2 FOR UPDATE`, todoID, projectID).Scan(&id); err != nil {
		return Todo{}, notFound(err)
	}
	return getTodo(ctx, tx, todoID)
}

func getTodo(ctx context.Context, q Querier, id string) (Todo, error) {
	t, err := one[Todo](ctx, q, todoSelect+` WHERE t.id=$1`, id)
	return t, notFound(err)
}

// lastPosition is the position after every other card in a cell.
func lastPosition(ctx context.Context, tx *sql.Tx, projectID, status, priority, exclude string) (float64, error) {
	var pos float64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(position),0)+$5 FROM todos WHERE project_id=$1 AND status=$2 AND priority=$3 AND id::text<>$4`,
		projectID, status, priority, exclude, todoGap).Scan(&pos)
	return pos, err
}

func saveTodo(ctx context.Context, tx *sql.Tx, a Identity, in SaveTodoInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true)
	if err != nil {
		return nil, err
	}
	if err := validateTodo(&in); err != nil {
		return nil, err
	}
	if err := todoAssignee(ctx, tx, p.ID, in.AssigneeID); err != nil {
		return nil, err
	}
	if err := normalizeTodoRelations(&in); err != nil {
		return nil, err
	}
	if in.TodoID == "" {
		if in.ExpectedRevision != 0 {
			return nil, problem("INVALID_INPUT", "expected_revision is only used with todo_id.")
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM todos WHERE project_id=$1`, p.ID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= maxProjectTodos {
			return nil, problem("QUOTA_EXCEEDED", "A project can hold at most 5000 to-dos; delete finished ones first.")
		}
		pos, err := lastPosition(ctx, tx, p.ID, in.Status, in.Priority, "")
		if err != nil {
			return nil, err
		}
		id := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO todos(id,project_id,title,body,status,priority,category,progress,assignee_id,due_date,position,created_by,updated_by,completed_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')::date,$11,$12,$12,CASE WHEN $5='completed' THEN now() END)`,
			id, p.ID, in.Title, in.Body, in.Status, in.Priority, in.Category, in.Progress, nullableID(in.AssigneeID), in.DueDate, pos, a.User.ID); err != nil {
			return nil, err
		}
		if err := setTodoRelations(ctx, tx, p.ID, id, in.ParentID, in.BlockedBy); err != nil {
			return nil, err
		}
		return getTodo(ctx, tx, id)
	}
	if in.ExpectedRevision < 1 {
		return nil, problem("INVALID_INPUT", "expected_revision is required to change a to-do.")
	}
	current, err := lockTodo(ctx, tx, p.ID, in.TodoID)
	if err != nil {
		return nil, err
	}
	if current.Revision != in.ExpectedRevision {
		return nil, problem("CONFLICT", "The to-do was changed by someone else. Reload it and try again.")
	}
	pos := current.Position
	if current.Status != in.Status || current.Priority != in.Priority {
		if pos, err = lastPosition(ctx, tx, p.ID, in.Status, in.Priority, current.ID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE todos SET title=$2,body=$3,status=$4,priority=$5,category=$6,progress=$7,assignee_id=$8,
		due_date=NULLIF($9,'')::date,position=$10,updated_by=$11,updated_at=now(),revision=revision+1,
		completed_at=CASE WHEN $4<>'completed' THEN NULL WHEN status='completed' THEN completed_at ELSE now() END
		WHERE id=$1`, current.ID, in.Title, in.Body, in.Status, in.Priority, in.Category, in.Progress, nullableID(in.AssigneeID), in.DueDate, pos, a.User.ID); err != nil {
		return nil, err
	}
	if err := setTodoRelations(ctx, tx, p.ID, current.ID, in.ParentID, in.BlockedBy); err != nil {
		return nil, err
	}
	return getTodo(ctx, tx, current.ID)
}

// moveTodo drops a card into a status × priority cell, before another card or
// last. Completing a card with a progress bar fills the bar.
func moveTodo(ctx context.Context, tx *sql.Tx, a Identity, in MoveTodoInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true)
	if err != nil {
		return nil, err
	}
	if !oneOf(in.Status, todoStatuses) {
		return nil, problem("INVALID_INPUT", "status must be not_started, in_progress, in_review, completed or idea.")
	}
	if !oneOf(in.Priority, todoPriorities) {
		return nil, problem("INVALID_INPUT", "priority must be high, medium or low.")
	}
	// Lock the whole board so concurrent drops in one cell cannot pick the same gap.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('todos:'||$1,0))`, p.ID); err != nil {
		return nil, err
	}
	current, err := lockTodo(ctx, tx, p.ID, in.TodoID)
	if err != nil {
		return nil, err
	}
	if in.BeforeID == current.ID {
		return nil, problem("INVALID_INPUT", "before_id must be a different to-do.")
	}
	pos, err := dropPosition(ctx, tx, p.ID, in, current.ID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE todos SET status=$2,priority=$3,position=$4,updated_by=$5,updated_at=now(),revision=revision+1,
		progress=CASE WHEN $2='completed' AND status<>'completed' AND progress IS NOT NULL THEN 100 ELSE progress END,
		completed_at=CASE WHEN $2<>'completed' THEN NULL WHEN status='completed' THEN completed_at ELSE now() END
		WHERE id=$1`, current.ID, in.Status, in.Priority, pos, a.User.ID); err != nil {
		return nil, err
	}
	return getTodo(ctx, tx, current.ID)
}

func dropPosition(ctx context.Context, tx *sql.Tx, projectID string, in MoveTodoInput, moving string) (float64, error) {
	if in.BeforeID == "" {
		return lastPosition(ctx, tx, projectID, in.Status, in.Priority, moving)
	}
	if err := validID(in.BeforeID); err != nil {
		return 0, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		var before float64
		var prev sql.NullFloat64
		err := tx.QueryRowContext(ctx, `SELECT b.position,(SELECT max(o.position) FROM todos o WHERE o.project_id=b.project_id AND o.status=b.status
			AND o.priority=b.priority AND o.id<>$3 AND o.id<>b.id AND (o.position,o.id)<(b.position,b.id))
			FROM todos b WHERE b.id=$1 AND b.project_id=$2 AND b.status=$4 AND b.priority=$5`, in.BeforeID, projectID, moving, in.Status, in.Priority).Scan(&before, &prev)
		if err != nil {
			if err == sql.ErrNoRows {
				return 0, problem("INVALID_INPUT", "before_id must be a to-do in the target status and priority.")
			}
			return 0, err
		}
		if !prev.Valid {
			return before - todoGap, nil
		}
		if before-prev.Float64 > 1e-6 {
			return (prev.Float64 + before) / 2, nil
		}
		// Gaps ran out: renumber the cell, then retry once.
		if _, err := tx.ExecContext(ctx, `UPDATE todos t SET position=r.n*$4 FROM (SELECT id,row_number() OVER (ORDER BY position,id) n
			FROM todos WHERE project_id=$1 AND status=$2 AND priority=$3) r WHERE t.id=r.id`, projectID, in.Status, in.Priority, todoGap); err != nil {
			return 0, err
		}
	}
	return 0, problem("CONFLICT", "Could not place the to-do; reload the board and try again.")
}

func deleteTodo(ctx context.Context, tx *sql.Tx, a Identity, in TodoRef) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true)
	if err != nil {
		return nil, err
	}
	t, err := lockTodo(ctx, tx, p.ID, in.TodoID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM todos WHERE id=$1`, t.ID); err != nil {
		return nil, err
	}
	return map[string]any{"id": t.ID, "project_id": p.ID, "deleted": true}, nil
}

func registerTodoTools(t Tools, s *Store) {
	add(t, s, "list_todos", "List a project's to-dos (할 일) for the board: each has status (not_started, in_progress, in_review, completed, idea), priority (high, medium, low), category, optional progress 0-100, assignee and due date, ordered by priority, status and board position. Each also shows its parent (parent_id, one level of sub-items), the to-dos it waits for (blocked_by, open_blocker_count) and its sub-item counts. Filter by status, priority, category, assignee_id, mine, or query (words that must all appear in the title, body or category) — use query to check whether something is already on the board instead of reading every to-do. Up to 500.", false, func(in ListTodosInput) string { return in.ProjectID }, listTodos)
	add(t, s, "save_todo", "Add a to-do, or change one with todo_id and expected_revision. Changing replaces every field, so send the current values you want to keep (omitted progress, assignee_id and due_date are cleared); parent_id and blocked_by are kept when omitted. Relations stay inside the project, sub-items are one level deep and blocked_by cycles are rejected. Editor access required.", true, func(in SaveTodoInput) string { return in.ProjectID }, saveTodo)
	add(t, s, "move_todo", "Move a to-do to a status column and priority row on the board, before before_id or last. Moving to completed fills an existing progress bar. Editor access required.", true, func(in MoveTodoInput) string { return in.ProjectID }, moveTodo)
	add(t, s, "delete_todo", "Delete a to-do permanently. Editor access required.", true, func(in TodoRef) string { return in.ProjectID }, deleteTodo)
}
