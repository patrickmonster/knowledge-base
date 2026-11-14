-- Notification channels deliver issue events to people outside the console.
--   webhook  HTTPS POST of a JSON event to target
--   slack    Slack incoming-webhook URL in target (https://hooks.slack.com/...)
--   email    mail to each recipient's account address through the server's SMTP
--            settings; target is empty
-- target is a credential-like URL: it is never returned in full or copied to
-- audit logs.
CREATE TABLE notification_channels (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    kind text NOT NULL CHECK (kind IN ('webhook','slack','email')),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    target text NOT NULL DEFAULT '' CHECK (length(target) <= 2000),
    events text[] NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_channels_project ON notification_channels(project_id) WHERE enabled;

-- Outbox rows are written in the same transaction as the issue change, then a
-- worker delivers them with retries. Payloads hold identifiers, titles and
-- names only; issue bodies and answers are not sent.
CREATE TABLE notification_outbox (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    channel_id uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event text NOT NULL,
    issue_id uuid REFERENCES issues(id) ON DELETE CASCADE,
    recipient_id uuid REFERENCES users(id),
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','sent','failed')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz
);
CREATE INDEX notification_outbox_due ON notification_outbox(next_attempt_at) WHERE status IN ('pending','processing');
CREATE INDEX notification_outbox_channel ON notification_outbox(channel_id, created_at DESC);

-- Project import keeps the exported document ID so importing the same export
-- again skips documents that were already imported.
ALTER TABLE documents ADD COLUMN imported_from uuid;
CREATE UNIQUE INDEX documents_imported_from ON documents(project_id, imported_from) WHERE imported_from IS NOT NULL;
