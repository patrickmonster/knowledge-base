-- grep_documents의 고정 문자열 검색(LIKE/ILIKE)을 빠르게 하는 trigram 색인.
-- 정규식 검색은 서버(Go RE2)에서 줄 단위로 맞추므로 이 색인을 쓰지 않는다.
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
CREATE INDEX IF NOT EXISTS documents_content_trgm ON documents USING gin (content gin_trgm_ops);
CREATE INDEX IF NOT EXISTS documents_title_trgm ON documents USING gin (title gin_trgm_ops);
