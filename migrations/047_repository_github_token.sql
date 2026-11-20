-- GitHub access token per project repository, set by a project owner or admin
-- in the web console (프로젝트와 권한 → 저장소). It replaces the server-wide
-- GITHUB_TOKEN variable: PR merge checks and issue imports of a private
-- repository use the token of that repository in the project. The token is
-- never returned; only whether one is set, who set it and when.
ALTER TABLE project_repositories ADD COLUMN github_token text CHECK (github_token IS NULL OR length(github_token) BETWEEN 20 AND 255);
ALTER TABLE project_repositories ADD COLUMN github_token_set_by uuid REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE project_repositories ADD COLUMN github_token_set_at timestamptz;
