-- Project import now brings to-dos along. imported_from keeps the source
-- to-do's ID so importing the same export again skips to-dos already made.
ALTER TABLE todos ADD COLUMN imported_from uuid;
CREATE INDEX todos_imported ON todos(project_id, imported_from) WHERE imported_from IS NOT NULL;
