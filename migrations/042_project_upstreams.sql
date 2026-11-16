-- Upstream projects: a project (downstream) follows another project (upstream)
-- that holds shared source-of-truth knowledge such as design mockups, product
-- planning or API specs. One upstream serves many downstreams. Members of an
-- approved downstream can read the upstream without joining it and report
-- problems there as issues; they cannot edit its documents. The downstream's
-- owner or admin requests the link and the upstream's owner or admin approves
-- it, so the upstream decides who reads it.
CREATE TABLE project_upstreams (
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  upstream_project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved')),
  requested_by uuid REFERENCES users(id) ON DELETE SET NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  approved_by uuid REFERENCES users(id) ON DELETE SET NULL,
  approved_at timestamptz,
  PRIMARY KEY (project_id, upstream_project_id),
  CHECK (project_id <> upstream_project_id),
  CHECK ((status = 'approved') = (approved_at IS NOT NULL))
);
CREATE INDEX project_upstreams_upstream ON project_upstreams(upstream_project_id, status);

-- An issue a downstream member raised on the upstream names the downstream it
-- came from, so the upstream can see which projects a problem affects.
ALTER TABLE issues ADD COLUMN origin_project_id uuid REFERENCES projects(id) ON DELETE SET NULL;
CREATE INDEX issues_origin_project ON issues(project_id, origin_project_id) WHERE origin_project_id IS NOT NULL;
