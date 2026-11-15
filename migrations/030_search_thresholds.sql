-- Per-project thresholds for reviewing an HNSW (approximate) vector index.
-- get_usage_stats compares search_timings against them. NULL uses the server
-- default (HNSW_REVIEW_VECTOR_P95_MS, HNSW_REVIEW_SCANNED_CHUNKS).
CREATE TABLE project_search_settings (
  project_id uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  hnsw_vector_p95_ms integer CHECK (hnsw_vector_p95_ms BETWEEN 1 AND 60000),
  hnsw_scanned_chunks integer CHECK (hnsw_scanned_chunks BETWEEN 100 AND 100000000),
  updated_by uuid NOT NULL REFERENCES users(id),
  updated_at timestamptz NOT NULL DEFAULT now()
);
