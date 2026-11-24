-- 완료된 할 일의 종료 사유: ''(완료) 또는 'dropped'(폐기 — 범위 제외·불필요 판단)와 짧은 사유.
-- 폐기는 완료 상태의 한 갈래라 보드·자동 이동 규칙은 그대로 두고, 미병합 PR 경고에서만 뺀다.
-- release_auto: finish_task completed가 미병합 PR 때문에 완료 대신 반영 대기로 보낸 할 일.
-- 이 할 일은 연결 PR이 병합되면 자동 완료된다(사람이 직접 반영 대기로 옮긴 할 일은 그대로 사람이 완료).
ALTER TABLE todos ADD COLUMN IF NOT EXISTS resolution text NOT NULL DEFAULT '';
ALTER TABLE todos ADD COLUMN IF NOT EXISTS resolution_note text NOT NULL DEFAULT '';
ALTER TABLE todos ADD COLUMN IF NOT EXISTS release_auto boolean NOT NULL DEFAULT false;
ALTER TABLE todos DROP CONSTRAINT IF EXISTS todos_resolution_check;
ALTER TABLE todos ADD CONSTRAINT todos_resolution_check CHECK (resolution IN ('','dropped') AND (resolution='' OR status='completed'));
