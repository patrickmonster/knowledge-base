package kb

import (
	"context"
	"database/sql"
	"strings"
)

// Long-open task runs (오래 열린 작업 정리).
//
// A task run stays open when an agent started work and never called
// finish_task (interrupted session, abandoned plan). list_task_runs with
// older_than_days finds them; close_task_run marks one abandoned with a
// reason; cleanup_stale_task_runs previews or closes many at once. Abandoned
// runs keep their starting context but can no longer be finished.

const defaultStaleTaskDays = 7

type CloseTaskInput struct {
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	Reason    string `json:"reason" jsonschema:"Why the task is closed without finish_task, 1–1000 characters"`
}

type CleanupTasksInput struct {
	ProjectID     string `json:"project_id"`
	OlderThanDays int    `json:"older_than_days,omitempty" jsonschema:"Close open tasks started at least this many days ago, 1–365; default 7"`
	Reason        string `json:"reason,omitempty" jsonschema:"Reason recorded on each closed task; required when apply is true"`
	Apply         bool   `json:"apply,omitempty" jsonschema:"false (default) only previews; true closes at most 200 tasks. Applying requires the project owner"`
}

type StaleTaskRun struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	CreatedBy     string `json:"created_by"`
	CreatedByName string `json:"created_by_name"`
	RequestKey    string `json:"request_key"`
	StartedAt     string `json:"started_at"`
	AgeDays       int    `json:"age_days"`
}

const staleTaskSelect = `SELECT jsonb_build_object('id',r.id,'title',r.title,'created_by',r.created_by,'created_by_name',u.name,'request_key',r.request_key,
	'started_at',r.started_at,'age_days',floor(extract(epoch FROM now()-r.started_at)/86400)::int)
	FROM task_runs r JOIN users u ON u.id=r.created_by
	WHERE r.project_id=$1 AND r.status='open' AND r.started_at < now()-make_interval(days=>$2::int)`

func closeTaskRun(ctx context.Context, tx *sql.Tx, a Identity, in CloseTaskInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true)
	if err != nil {
		return nil, err
	}
	if err := validID(in.TaskID); err != nil {
		return nil, err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if err := bounded("reason", in.Reason, 1, 1000); err != nil {
		return nil, err
	}
	run, err := one[TaskRun](ctx, tx, `SELECT to_jsonb(r)-'completion_hash' FROM task_runs r WHERE id=$1 AND project_id=$2 FOR UPDATE`, in.TaskID, in.ProjectID)
	if err != nil {
		return nil, notFound(err)
	}
	if run.CreatedBy != a.User.ID && p.OwnerID != a.User.ID {
		return nil, problem("FORBIDDEN", "Only the user who started this task or the project owner may close it.")
	}
	switch run.Status {
	case "completed":
		return nil, problem("CONFLICT", "Task is already completed; nothing to close.")
	case "abandoned":
		// Retrying the same close is safe; the first reason is kept.
		return taskRunSummary(ctx, tx, run.ID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_runs SET status='abandoned',closed_reason=$2,closed_by=$3,closed_at=clock_timestamp() WHERE id=$1`, run.ID, in.Reason, a.User.ID); err != nil {
		return nil, err
	}
	return taskRunSummary(ctx, tx, run.ID)
}

func taskRunSummary(ctx context.Context, tx *sql.Tx, id string) (map[string]any, error) {
	return one[map[string]any](ctx, tx, `SELECT to_jsonb(r)-'context'-'completion_hash' FROM task_runs r WHERE id=$1`, id)
}

func cleanupStaleTaskRuns(ctx context.Context, tx *sql.Tx, a Identity, in CleanupTasksInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true)
	if err != nil {
		return nil, err
	}
	if in.OlderThanDays == 0 {
		in.OlderThanDays = defaultStaleTaskDays
	}
	if in.OlderThanDays < 1 || in.OlderThanDays > 365 {
		return nil, problem("INVALID_INPUT", "older_than_days must be 1–365.")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Apply {
		if p.OwnerID != a.User.ID {
			return nil, problem("FORBIDDEN", "Only the project owner can close other users' tasks in bulk. Use close_task_run for your own task.")
		}
		if err := bounded("reason", in.Reason, 1, 1000); err != nil {
			return nil, err
		}
	}
	runs, err := many[StaleTaskRun](ctx, tx, staleTaskSelect+` ORDER BY r.started_at,r.id LIMIT 200`, in.ProjectID, in.OlderThanDays)
	if err != nil {
		return nil, err
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_runs r WHERE r.project_id=$1 AND r.status='open' AND r.started_at < now()-make_interval(days=>$2::int)`, in.ProjectID, in.OlderThanDays).Scan(&total); err != nil {
		return nil, err
	}
	out := map[string]any{"older_than_days": in.OlderThanDays, "total_open_stale": total, "tasks": runs, "applied": false, "closed": 0,
		"guidance": "Preview only. Check with each creator whether the work finished elsewhere: finish_task records real results; close only abandoned work. Re-run with apply=true and a reason to close them."}
	if !in.Apply || len(runs) == 0 {
		return out, nil
	}
	ids := make([]string, len(runs))
	for i, r := range runs {
		ids[i] = r.ID
	}
	result, err := tx.ExecContext(ctx, `UPDATE task_runs SET status='abandoned',closed_reason=$3,closed_by=$4,closed_at=clock_timestamp()
	WHERE project_id=$1 AND status='open' AND id::text=ANY($2::text[])`, in.ProjectID, ids, in.Reason, a.User.ID)
	if err != nil {
		return nil, err
	}
	closed, err := result.RowsAffected()
	out["applied"], out["closed"] = true, closed
	out["guidance"] = "Closed tasks are kept as abandoned with the reason and can no longer be finished. Start a new task (new request_key) to resume the work."
	return out, err
}
