-- Search latency samples (검색 시간 측정). One row per project-scoped search
-- (search_knowledge and start_task/get_task_context) records how long each
-- phase took and how many embedded chunks the exact vector scan compared, so the
-- decision to add an HNSW index rests on measured latency against chunk count.
-- No query text or user is stored; rows are purged with search_misses.
CREATE TABLE search_timings (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    source text NOT NULL CHECK (source IN ('search_knowledge','task_context')),
    mode text NOT NULL,
    retrieval text NOT NULL,
    total_ms real NOT NULL CHECK (total_ms >= 0),
    embed_ms real CHECK (embed_ms >= 0),
    vector_ms real CHECK (vector_ms >= 0),
    lexical_ms real CHECK (lexical_ms >= 0),
    scanned_chunks integer NOT NULL DEFAULT 0 CHECK (scanned_chunks >= 0),
    hits integer NOT NULL DEFAULT 0 CHECK (hits >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX search_timings_project ON search_timings(project_id, created_at DESC);
CREATE INDEX search_timings_created ON search_timings(created_at);
