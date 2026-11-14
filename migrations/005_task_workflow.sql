CREATE TABLE task_runs (
  id uuid PRIMARY KEY,
  project_id uuid NOT NULL REFERENCES projects(id),
  created_by uuid NOT NULL REFERENCES users(id),
  request_key text NOT NULL CHECK (length(request_key) BETWEEN 1 AND 100),
  title text NOT NULL CHECK (length(title) BETWEEN 1 AND 150),
  task text NOT NULL CHECK (length(task) BETWEEN 1 AND 1000),
  excerpt_budget integer NOT NULL,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','completed')),
  context jsonb,
  completion jsonb,
  completion_hash text,
  started_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE(project_id,created_by,request_key),
  CHECK ((status='open' AND completion IS NULL AND completion_hash IS NULL AND completed_at IS NULL)
    OR (status='completed' AND completion IS NOT NULL AND completion_hash IS NOT NULL AND completed_at IS NOT NULL))
);
CREATE INDEX task_runs_project ON task_runs(project_id,started_at DESC,id);
