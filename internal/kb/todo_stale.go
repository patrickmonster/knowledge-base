package kb

import (
	"context"
	"database/sql"
	"fmt"
)

// Stale to-dos (오래 묵은 할 일 정리 안내).
//
// A to-do waiting on the board (not_started, in_progress or in_review) goes
// stale when nothing has touched it for a while: the card itself was not
// edited or moved, no linked task run started, finished or closed, and no
// linked question changed. list_stale_todos lists them for clean-up, the web
// board marks their cards, and start_task/get_task_context mention a few in
// context.todos.stale so agents can suggest tidying the board. Ideas are a
// parking lot and completed to-dos are done, so neither counts.
//
// Nothing is changed automatically: someone decides whether to finish, move,
// re-prioritise or delete each card.

const (
	defaultStaleTodoDays = 14
	maxStaleTodoList     = 200
	// taskStaleTodoLimit caps the stale to-dos shown in task context.
	taskStaleTodoLimit = 5
)

var staleTodoStatuses = []string{"not_started", "in_progress", "in_review"}

type StaleTodo struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	Priority       string `json:"priority"`
	Category       string `json:"category,omitempty"`
	AssigneeID     string `json:"assignee_id,omitempty"`
	AssigneeName   string `json:"assignee_name,omitempty"`
	LastActivityAt string `json:"last_activity_at"`
	IdleDays       int    `json:"idle_days"`
	OpenTaskCount  int    `json:"open_task_count"`
}

type ListStaleTodosInput struct {
	ProjectID     string `json:"project_id"`
	OlderThanDays int    `json:"older_than_days,omitempty" jsonschema:"Idle for at least this many days, 1–365; default 14"`
	Limit         int    `json:"limit,omitempty" jsonschema:"1–200, default 200"`
}

// StaleTodos is the answer of list_stale_todos and context.todos.stale.
type StaleTodos struct {
	OlderThanDays int         `json:"older_than_days"`
	Total         int         `json:"total"`
	Todos         []StaleTodo `json:"todos"`
	Guidance      string      `json:"guidance"`
}

const staleTodosGuidance = "These to-dos have not moved for at least older_than_days days (no edit, move, linked task run or linked question change). Ask the people involved whether each is finished, still wanted or blocked, then move, re-prioritise, complete or delete it. Nothing is changed automatically."

// staleTodoActivity is a to-do's latest activity: its own update, a linked
// task run starting, finishing or closing, or a linked question changing.
const staleTodoActivity = `GREATEST(t.updated_at,
	(SELECT max(GREATEST(r.started_at,r.completed_at,r.closed_at)) FROM task_runs r WHERE r.todo_id=t.id),
	(SELECT max(i.updated_at) FROM issues i WHERE i.todo_id=t.id))`

func staleTodos(ctx context.Context, q Querier, projectID string, days, limit int) (StaleTodos, error) {
	out := StaleTodos{OlderThanDays: days, Todos: []StaleTodo{}, Guidance: staleTodosGuidance}
	from := fmt.Sprintf(` FROM todos t LEFT JOIN users a ON a.id=t.assignee_id
		CROSS JOIN LATERAL (SELECT %s AS at) act
		WHERE t.project_id=$1 AND t.status=ANY($2::text[]) AND act.at < now()-make_interval(days=>$3::int)`, staleTodoActivity)
	if err := q.QueryRowContext(ctx, `SELECT count(*)`+from, projectID, staleTodoStatuses, days).Scan(&out.Total); err != nil {
		return out, err
	}
	if out.Total == 0 {
		return out, nil
	}
	todos, err := many[StaleTodo](ctx, q, `SELECT jsonb_build_object('id',t.id,'title',t.title,'status',t.status,'priority',t.priority,'category',t.category,
		'assignee_id',t.assignee_id,'assignee_name',COALESCE(a.name,''),'last_activity_at',act.at,
		'idle_days',floor(extract(epoch FROM now()-act.at)/86400)::int,
		'open_task_count',(SELECT count(*) FROM task_runs r WHERE r.todo_id=t.id AND r.status='open'))`+from+`
		ORDER BY act.at, t.id LIMIT $4`, projectID, staleTodoStatuses, days, limit)
	if err != nil {
		return out, err
	}
	out.Todos = todos
	return out, nil
}

func listStaleTodos(ctx context.Context, tx *sql.Tx, a Identity, in ListStaleTodosInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	if in.OlderThanDays == 0 {
		in.OlderThanDays = defaultStaleTodoDays
	}
	if in.OlderThanDays < 1 || in.OlderThanDays > 365 {
		return nil, problem("INVALID_INPUT", "older_than_days must be 1–365.")
	}
	if in.Limit == 0 {
		in.Limit = maxStaleTodoList
	}
	if in.Limit < 1 || in.Limit > maxStaleTodoList {
		return nil, problem("INVALID_INPUT", "limit must be 1–200.")
	}
	return staleTodos(ctx, tx, p.ID, in.OlderThanDays, in.Limit)
}

// taskStaleTodos is context.todos.stale: the longest-idle waiting to-dos, or
// nil when none are stale so the context stays short.
func taskStaleTodos(ctx context.Context, q Querier, projectID string) (*StaleTodos, error) {
	out, err := staleTodos(ctx, q, projectID, defaultStaleTodoDays, taskStaleTodoLimit)
	if err != nil || out.Total == 0 {
		return nil, err
	}
	out.Guidance = "Waiting to-dos idle for " + fmt.Sprint(defaultStaleTodoDays) + "+ days (oldest first; total counts all). Mention them to the user as a board clean-up suggestion; do not move or delete them unless asked. list_stale_todos lists every one."
	return &out, nil
}

func registerStaleTodoTools(t Tools, s *Store) {
	add(t, s, "list_stale_todos", "List waiting to-dos (not_started, in_progress, in_review) with no activity for at least older_than_days (default 14): no edit or move, no linked task run started, finished or closed, no linked question changed. Oldest first with idle_days, at most 200. Read-only: decide with the people involved whether to move, complete or delete each.", false, func(in ListStaleTodosInput) string { return in.ProjectID }, listStaleTodos)
}
