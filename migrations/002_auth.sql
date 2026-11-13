ALTER TABLE users ADD COLUMN IF NOT EXISTS email text;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS global_role text NOT NULL DEFAULT 'member' CHECK (global_role IN ('admin','member'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX IF NOT EXISTS users_email ON users(email);

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS name text NOT NULL DEFAULT 'MCP key';
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS prefix text NOT NULL DEFAULT '';
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS access text NOT NULL DEFAULT 'read_write' CHECK (access IN ('read_only','read_write'));
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS project_id uuid REFERENCES projects(id);
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS expires_at timestamptz NOT NULL DEFAULT (now() + interval '30 days');

CREATE TABLE IF NOT EXISTS sessions (
  token_hash text PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id),
  csrf_token text NOT NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id);
CREATE TABLE IF NOT EXISTS invitations (
  token_hash text PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id),
  purpose text NOT NULL CHECK (purpose IN ('invite','reset')),
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS audit_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor_id uuid REFERENCES users(id),
  action text NOT NULL,
  project_id uuid,
  target_id text NOT NULL DEFAULT '',
  outcome text NOT NULL CHECK (outcome IN ('success','denied','error')),
  error_code text NOT NULL DEFAULT '',
  request_id text NOT NULL,
  duration_ms bigint NOT NULL DEFAULT 0,
  details jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_recent ON audit_events(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS audit_actor ON audit_events(actor_id,created_at DESC);
CREATE INDEX IF NOT EXISTS audit_project ON audit_events(project_id,created_at DESC);
CREATE TABLE IF NOT EXISTS rate_limits (
  bucket text PRIMARY KEY,
  window_start timestamptz NOT NULL DEFAULT now(),
  attempts integer NOT NULL DEFAULT 1
);
