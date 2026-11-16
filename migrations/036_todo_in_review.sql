-- 할 일 보드에 검토대기(in_review) 상태를 추가한다. 진행 중 다음, 완료 앞 열.
ALTER TABLE todos DROP CONSTRAINT IF EXISTS todos_status_check;
ALTER TABLE todos ADD CONSTRAINT todos_status_check CHECK (status IN ('not_started','in_progress','in_review','completed','idea'));
