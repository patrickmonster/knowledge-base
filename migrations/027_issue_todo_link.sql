-- A question or issue can belong to a to-do on the board; a to-do collects
-- the questions it waits on. Deleting the to-do keeps the issues.
ALTER TABLE issues ADD COLUMN todo_id uuid REFERENCES todos(id) ON DELETE SET NULL;
CREATE INDEX issues_todo ON issues(todo_id, status) WHERE todo_id IS NOT NULL;
