-- Source repositories of a project, so an agent can pick the project from its
-- git remote instead of matching names in list_projects. Stored normalized as
-- host/path in lower case (github.com/owner/name). The same repository may be
-- registered in more than one project; agents must stop and ask then.
CREATE TABLE project_repositories (
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  repository text NOT NULL CHECK (length(repository) BETWEEN 5 AND 200 AND repository = lower(repository)),
  created_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, repository)
);
CREATE INDEX project_repositories_repository ON project_repositories(repository);

-- Repositories already wired to a project through a GitHub webhook.
INSERT INTO project_repositories(project_id, repository, created_by)
SELECT project_id, 'github.com/' || repository, created_by FROM github_webhooks
ON CONFLICT DO NOTHING;
