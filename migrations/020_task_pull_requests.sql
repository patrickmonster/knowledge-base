-- Pull requests named in finish_task, tracked until they reach a final state.
-- state: unknown (not checked yet), open, closed (closed without merging),
-- merged, deployed (merged and contained in the running server build).
-- recorded_state is the state last written into the task's history as a
-- "상태 갱신" block; a row is settled when the state is final and recorded.
CREATE TABLE task_pull_requests (
  task_id uuid NOT NULL REFERENCES task_runs(id) ON DELETE CASCADE,
  project_id uuid NOT NULL REFERENCES projects(id),
  repository text NOT NULL CHECK (length(repository) BETWEEN 3 AND 140 AND repository = lower(repository)),
  number integer NOT NULL CHECK (number > 0),
  url text NOT NULL CHECK (length(url) <= 300),
  history_document_id uuid REFERENCES documents(id),
  state text NOT NULL DEFAULT 'unknown' CHECK (state IN ('unknown','open','closed','merged','deployed')),
  merge_commit text CHECK (merge_commit IS NULL OR merge_commit ~ '^[0-9a-f]{7,40}$'),
  merged_at timestamptz,
  deployed_commit text CHECK (deployed_commit IS NULL OR deployed_commit ~ '^[0-9a-f]{7,40}$'),
  deployed_at timestamptz,
  checked_at timestamptz,
  check_error text CHECK (check_error IS NULL OR length(check_error) <= 40),
  recorded_state text CHECK (recorded_state IS NULL OR recorded_state IN ('closed','merged','deployed')),
  recorded_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (task_id, repository, number)
);
CREATE INDEX task_pull_requests_unsettled ON task_pull_requests(project_id, checked_at NULLS FIRST)
  WHERE recorded_state IS DISTINCT FROM state;
