-- Issue images can belong to the question body, its current answer, or one
-- comment. When an answer is replaced the previous answer becomes a comment and
-- its images move with it (issue_part='comment', issue_comment_id set).
-- issue_id and issue_comment_id are ON DELETE SET NULL, so the check only ties
-- a comment link to the comment part and never blocks deleting an issue.
ALTER TABLE attachments
    ADD COLUMN issue_part text CHECK (issue_part IN ('body','answer','comment')),
    ADD COLUMN issue_comment_id uuid REFERENCES issue_comments(id) ON DELETE SET NULL,
    ADD CONSTRAINT attachments_issue_comment_part CHECK (issue_comment_id IS NULL OR issue_part = 'comment');
UPDATE attachments SET issue_part='body' WHERE issue_id IS NOT NULL AND issue_part IS NULL;
