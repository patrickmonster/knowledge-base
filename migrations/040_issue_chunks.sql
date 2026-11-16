-- Question and issue text (title, body, answer, resolution) is split into
-- chunks like documents so search_knowledge can find related questions by
-- meaning as well as by keywords. Chunks are rebuilt in the same transaction
-- whenever that text changes; the embedding worker fills vectors with the same
-- queue columns and retry rules as document chunks. Existing questions are
-- chunked by the worker after this migration (issues with no chunks).
CREATE TABLE issue_chunks (
    id uuid PRIMARY KEY,
    issue_id uuid NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    position integer NOT NULL,
    content text NOT NULL,
    search_vector tsvector NOT NULL,
    embedding public.vector(1024),
    embedding_model text,
    embedding_status text NOT NULL DEFAULT 'pending'
        CHECK (embedding_status IN ('pending','processing','ready','failed')),
    embedding_attempts integer NOT NULL DEFAULT 0,
    embedding_retry_at timestamptz NOT NULL DEFAULT now(),
    embedding_lease uuid,
    embedding_lease_until timestamptz,
    embedding_error text,
    UNIQUE (issue_id, position)
);
CREATE INDEX issue_chunks_search ON issue_chunks USING gin(search_vector);
CREATE INDEX issue_chunks_embedding_queue ON issue_chunks(embedding_status, embedding_retry_at);
