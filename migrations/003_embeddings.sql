CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;

ALTER TABLE chunks ADD COLUMN embedding public.vector(1024);
ALTER TABLE chunks ADD COLUMN embedding_model text;
ALTER TABLE chunks ADD COLUMN embedding_status text NOT NULL DEFAULT 'pending'
  CHECK (embedding_status IN ('pending','processing','ready','failed'));
ALTER TABLE chunks ADD COLUMN embedding_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN embedding_retry_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE chunks ADD COLUMN embedding_lease uuid;
ALTER TABLE chunks ADD COLUMN embedding_lease_until timestamptz;
ALTER TABLE chunks ADD COLUMN embedding_error text;
CREATE INDEX chunks_embedding_queue ON chunks(embedding_status,embedding_retry_at);
CREATE INDEX chunks_embedding_model ON chunks(embedding_model) WHERE embedding IS NOT NULL;

-- Existing chunks become pending too. Exact cosine search is scoped to one
-- project before ranking; no cross-project approximate-search filtering loss.
