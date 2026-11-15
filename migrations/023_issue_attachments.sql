-- Images attached to a question or issue (screenshots, photos). They reuse the
-- attachment pipeline (bucket, quota, scanning, cleanup); issue_id links them
-- to the issue and the web shows them inline through /api/attachments/{id}/image.
ALTER TABLE attachments ADD COLUMN issue_id uuid REFERENCES issues(id) ON DELETE SET NULL;
CREATE INDEX attachments_issue ON attachments(issue_id, created_at) WHERE issue_id IS NOT NULL;
