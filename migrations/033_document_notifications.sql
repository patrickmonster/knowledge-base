-- Document notifications: channels can also announce new and promoted
-- documents. The events list already holds the event names, and existing
-- channels do not list the new ones, so they stay off until a project owner
-- turns them on. document_kinds limits which kinds of document are announced;
-- the default leaves out history (one per task) and attachment text artifacts.
ALTER TABLE notification_channels ADD COLUMN document_kinds text[] NOT NULL DEFAULT '{brief,decision,reference,glossary}';
