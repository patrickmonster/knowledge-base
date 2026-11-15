-- A task run (start_task) can carry out one to-do; a to-do can be worked on by
-- several runs (follow-ups, several PRs). Deleting the to-do keeps the runs.
ALTER TABLE task_runs ADD COLUMN todo_id uuid REFERENCES todos(id) ON DELETE SET NULL;
CREATE INDEX task_runs_todo ON task_runs(todo_id, started_at DESC) WHERE todo_id IS NOT NULL;
