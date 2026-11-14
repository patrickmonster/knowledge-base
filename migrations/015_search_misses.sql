-- Searches that returned no hits (사용 통계: 결과 0건 검색어).
-- Only the normalized query text (at most 200 characters) is kept, without the
-- searching user, and rows are purged after at most 365 days (or the shorter
-- AUDIT_RETENTION_DAYS).
CREATE TABLE search_misses (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    query text NOT NULL CHECK (length(query) BETWEEN 1 AND 200),
    mode text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX search_misses_project ON search_misses(project_id, created_at DESC);
CREATE INDEX search_misses_created ON search_misses(created_at);
