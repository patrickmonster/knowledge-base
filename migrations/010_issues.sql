-- Work areas name shared work and its owner (담당자). When someone other than
-- the owner works in an area, they leave a question for the owner instead of
-- deciding alone; start_task surfaces those questions before work begins.
CREATE TABLE work_areas (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    owner_id uuid NOT NULL REFERENCES users(id),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX work_areas_name ON work_areas(project_id, lower(name));

-- Issues and questions. Lifecycle:
--   open      waiting for an answer or a fix
--   answered  the assignee answered; the asker still has to confirm
--   resolved  confirmed or fixed
--   closed    withdrawn or no longer needed
-- An answered question can be published as a reference document
-- (knowledge_document_id) so the answer joins normal knowledge search.
CREATE TABLE issues (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    kind text NOT NULL CHECK (kind IN ('question','issue')),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body text NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','answered','resolved','closed')),
    blocking boolean NOT NULL DEFAULT false,
    area_id uuid REFERENCES work_areas(id) ON DELETE SET NULL,
    assignee_id uuid REFERENCES users(id),
    tags text[] NOT NULL DEFAULT '{}',
    answer text NOT NULL DEFAULT '' CHECK (length(answer) <= 20000),
    answered_by uuid REFERENCES users(id),
    answered_at timestamptz,
    resolution text NOT NULL DEFAULT '' CHECK (length(resolution) <= 2000),
    document_id uuid REFERENCES documents(id) ON DELETE SET NULL,
    task_id uuid REFERENCES task_runs(id) ON DELETE SET NULL,
    knowledge_document_id uuid REFERENCES documents(id) ON DELETE SET NULL,
    request_key text CHECK (request_key IS NULL OR length(request_key) BETWEEN 1 AND 100),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, created_by, request_key),
    CHECK ((answered_by IS NULL) = (answered_at IS NULL))
);
CREATE INDEX issues_project_status ON issues(project_id, status, updated_at DESC);
CREATE INDEX issues_assignee ON issues(assignee_id, status) WHERE status IN ('open','answered');
CREATE INDEX issues_creator ON issues(created_by, status) WHERE status IN ('open','answered');

CREATE TABLE issue_comments (
    id uuid PRIMARY KEY,
    issue_id uuid NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    author_id uuid NOT NULL REFERENCES users(id),
    body text NOT NULL CHECK (length(body) BETWEEN 1 AND 10000),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX issue_comments_issue ON issue_comments(issue_id, created_at);
