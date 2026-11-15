-- Project to-dos shown on the web board (할 일 보드): columns by status,
-- rows by priority. Members read them; editors and above change them, from
-- the web console or an MCP key.
--   not_started  시작 전
--   in_progress  진행 중
--   completed    완료
--   idea         아이디어 (not committed to yet)
-- position orders cards inside one status × priority cell (ascending).
-- progress is optional (NULL hides the bar).
CREATE TABLE todos (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body text NOT NULL DEFAULT '' CHECK (length(body) <= 10000),
    status text NOT NULL DEFAULT 'not_started' CHECK (status IN ('not_started','in_progress','completed','idea')),
    priority text NOT NULL DEFAULT 'medium' CHECK (priority IN ('high','medium','low')),
    category text NOT NULL DEFAULT '' CHECK (length(category) <= 50),
    progress smallint CHECK (progress IS NULL OR progress BETWEEN 0 AND 100),
    assignee_id uuid REFERENCES users(id),
    due_date date,
    position double precision NOT NULL DEFAULT 0,
    revision integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);
CREATE INDEX todos_board ON todos(project_id, priority, status, position, id);
CREATE INDEX todos_assignee ON todos(assignee_id) WHERE assignee_id IS NOT NULL AND status<>'completed';
