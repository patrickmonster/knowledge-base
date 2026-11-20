-- Pull request states reported by GitHub webhooks, kept per project and
-- repository even when no task names the pull request yet. A task that names
-- it later in finish_task takes the remembered state at once, so a pull
-- request merged before the task was finished is not lost (the server keeps no
-- GitHub token and cannot read private repositories itself).
CREATE TABLE github_pull_requests (
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  repository text NOT NULL CHECK (length(repository) BETWEEN 3 AND 140 AND repository = lower(repository)),
  number integer NOT NULL CHECK (number > 0),
  state text NOT NULL CHECK (state IN ('open','closed','merged')),
  merge_commit text CHECK (merge_commit IS NULL OR merge_commit ~ '^[0-9a-f]{7,40}$'),
  merged_at timestamptz,
  received_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, repository, number)
);
CREATE INDEX github_pull_requests_received ON github_pull_requests(received_at);

-- A project owner or admin may confirm by hand that a tracked pull request was
-- merged or closed (for one merged before its repository's webhook existed).
ALTER TABLE task_pull_requests ADD COLUMN confirmed_by uuid REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE task_pull_requests ADD COLUMN confirmed_at timestamptz;
