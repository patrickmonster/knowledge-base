-- 할 일 사이의 관계.
-- parent_id: 하위 항목. 한 단계만 둔다(상위 할 일은 다른 할 일의 하위가 될 수 없다).
--   상위 할 일을 지우면 하위 항목은 남고 최상위로 올라온다.
-- todo_dependencies: 선행 할 일. todo_id는 blocked_by가 끝나기를 기다린다.
--   순환은 저장할 때 막는다. 어느 쪽이든 지우면 관계도 지워진다.
-- 두 관계 모두 같은 프로젝트 안에서만 맺는다(저장할 때 확인).
ALTER TABLE todos ADD COLUMN parent_id uuid REFERENCES todos(id) ON DELETE SET NULL;
ALTER TABLE todos ADD CONSTRAINT todos_parent_not_self CHECK (parent_id IS NULL OR parent_id<>id);
CREATE INDEX todos_parent ON todos(parent_id) WHERE parent_id IS NOT NULL;

CREATE TABLE todo_dependencies (
    todo_id uuid NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
    blocked_by uuid NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (todo_id, blocked_by),
    CHECK (todo_id<>blocked_by)
);
CREATE INDEX todo_dependencies_blocked_by ON todo_dependencies(blocked_by);
