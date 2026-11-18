-- 작업 프롬프트: 소유자·관리자가 미리 지정해 둔 작업 지시.
-- 사용자가 호출 문구(triggers)나 이름으로 "~해줘"라고 요청하면 AI가
-- get_task_prompt로 본문을 받아 그대로 진행한다. 본문은 규칙처럼 지시로
-- 취급되므로 소유자·관리자만 웹 화면에서 바꾼다.
-- arguments: [{"name":"..","description":"..","required":true}], 본문의 {{name}} 자리에 채운다.
CREATE TABLE task_prompts (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    triggers text[] NOT NULL DEFAULT '{}',
    body text NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    arguments jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(arguments) = 'array'),
    enabled boolean NOT NULL DEFAULT true,
    revision integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
