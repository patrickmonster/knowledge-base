-- Discord message command "이슈로 등록": turn a message (for example the first
-- post of a forum thread) into an issue.

-- A Discord channel (a forum channel, or any text channel) mapped to the
-- project its messages become issues in. Set by a global administrator in the
-- web console. Channel ids are not secret.
CREATE TABLE discord_channel_projects (
  channel_id text PRIMARY KEY CHECK (channel_id ~ '^[0-9]{5,25}$'),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX discord_channel_projects_project_idx ON discord_channel_projects(project_id);

-- The issue a Discord message already became, so the same message is never
-- registered twice.
CREATE TABLE discord_issue_messages (
  message_id text PRIMARY KEY CHECK (message_id ~ '^[0-9]{5,25}$'),
  issue_id uuid NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX discord_issue_messages_issue_idx ON discord_issue_messages(issue_id);
