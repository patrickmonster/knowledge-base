-- 할 일 보드에 반영 대기(pending_release) 상태를 추가한다. 검토대기 다음, 완료 앞 열.
-- 반영(병합·배포)을 기다리는 작업 브랜치를 branch에 적는다. 기존 행은 빈 값.
ALTER TABLE todos DROP CONSTRAINT IF EXISTS todos_status_check;
ALTER TABLE todos ADD CONSTRAINT todos_status_check CHECK (status IN ('not_started','in_progress','in_review','pending_release','completed','idea'));
ALTER TABLE todos ADD COLUMN IF NOT EXISTS branch text NOT NULL DEFAULT '';
