-- Recheck digests (재확인 요약) are queued once per project and period. The
-- primary key is the claim: the instance that inserts the row queues the
-- digest in the same transaction, and any other instance skips the period.
CREATE TABLE recheck_digest_runs (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    period date NOT NULL,
    queued_at timestamptz NOT NULL DEFAULT now(),
    document_count integer NOT NULL,
    PRIMARY KEY (project_id, period)
);
