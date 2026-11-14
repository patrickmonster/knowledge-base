-- Project admin role (관리자): editor access plus managing project rules.
ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_role_check;
ALTER TABLE project_members ADD CONSTRAINT project_members_role_check
    CHECK (role IN ('owner', 'admin', 'editor', 'viewer'));

-- Commit and work rules that AI clients follow when working on a project.
--   project   소유자 규칙: set by the owner or a project admin; applies to everyone.
--   personal  개별 규칙: each member's own rules; visible only to that member.
-- Project rules win when a personal rule conflicts with them.
CREATE TABLE project_rules (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope IN ('project', 'personal')),
    user_id uuid REFERENCES users(id),
    category text NOT NULL DEFAULT 'work' CHECK (category IN ('commit', 'work', 'other')),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body text NOT NULL CHECK (length(body) BETWEEN 1 AND 4000),
    revision integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'personal') = (user_id IS NOT NULL))
);
CREATE INDEX project_rules_project ON project_rules(project_id, scope, category, created_at);
CREATE INDEX project_rules_personal ON project_rules(project_id, user_id) WHERE scope = 'personal';
