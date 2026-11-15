-- GitHub webhooks: one per project and repository, configured in the web
-- console by the project owner. The shared secret is sealed with the server's
-- webhook key (AES-GCM); it is shown once when created or rotated.
CREATE TABLE github_webhooks (
  id uuid PRIMARY KEY,
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  repository text NOT NULL CHECK (length(repository) BETWEEN 3 AND 140 AND repository = lower(repository)),
  secret_sealed text NOT NULL CHECK (length(secret_sealed) <= 500),
  import_issues boolean NOT NULL DEFAULT true,
  issue_kind text NOT NULL DEFAULT 'issue' CHECK (issue_kind IN ('issue','question','auto')),
  area_id uuid REFERENCES work_areas(id) ON DELETE SET NULL,
  assignee_id uuid REFERENCES users(id) ON DELETE SET NULL,
  tags text[] NOT NULL DEFAULT '{}',
  track_pull_requests boolean NOT NULL DEFAULT true,
  enabled boolean NOT NULL DEFAULT true,
  created_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  last_delivery_at timestamptz,
  last_event text CHECK (last_event IS NULL OR length(last_event) <= 60),
  last_outcome text CHECK (last_outcome IS NULL OR last_outcome IN ('processed','ignored','duplicate','rejected','failed')),
  last_error text CHECK (last_error IS NULL OR length(last_error) <= 60),
  UNIQUE (project_id, repository)
);
CREATE INDEX github_webhooks_repository ON github_webhooks(repository) WHERE enabled;

-- Delivery IDs seen recently, to drop GitHub redeliveries and replays.
CREATE TABLE github_webhook_deliveries (
  webhook_id uuid NOT NULL REFERENCES github_webhooks(id) ON DELETE CASCADE,
  delivery_id text NOT NULL CHECK (length(delivery_id) BETWEEN 1 AND 100),
  event text NOT NULL CHECK (length(event) <= 60),
  outcome text NOT NULL CHECK (outcome IN ('processed','ignored','rejected','failed')),
  detail text CHECK (detail IS NULL OR length(detail) <= 200),
  received_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (webhook_id, delivery_id)
);
CREATE INDEX github_webhook_deliveries_recent ON github_webhook_deliveries(webhook_id, received_at DESC);
