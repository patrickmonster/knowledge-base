-- Retention policy set by a global administrator in the web console, so the
-- periods no longer need server environment variables. One row for the whole
-- server. A NULL column falls back to the environment value
-- (AUDIT_RETENTION_DAYS, REVISION_RETENTION_DAYS, REVISION_KEEP_LATEST); 0 days
-- keeps that data forever.
CREATE TABLE retention_settings (
  id boolean PRIMARY KEY DEFAULT true CHECK (id),
  audit_days integer CHECK (audit_days = 0 OR audit_days BETWEEN 90 AND 3650),
  revision_days integer CHECK (revision_days = 0 OR revision_days BETWEEN 30 AND 3650),
  revision_keep_latest integer CHECK (revision_keep_latest BETWEEN 1 AND 1000),
  updated_by uuid NOT NULL REFERENCES users(id),
  updated_at timestamptz NOT NULL DEFAULT now()
);
