-- start_task can say which branch, worktree and paths a task works in, so a
-- second task on the same ground gets a concurrent_runs warning. All three are
-- optional; existing rows keep the empty defaults.
ALTER TABLE task_runs ADD COLUMN branch text NOT NULL DEFAULT '';
ALTER TABLE task_runs ADD COLUMN worktree text NOT NULL DEFAULT '';
ALTER TABLE task_runs ADD COLUMN paths jsonb NOT NULL DEFAULT '[]';
CREATE INDEX task_runs_open_scope ON task_runs(project_id, started_at DESC)
	WHERE status='open' AND (branch<>'' OR worktree<>'' OR jsonb_array_length(paths)>0);
