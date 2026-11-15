-- Issues imported from an external tracker (currently GitHub). source_key
-- identifies the item in that tracker ("owner/repo#123", lowercase repository)
-- so re-importing updates the same row instead of creating a duplicate.
-- source_state is the tracker state seen at the last import; status is synced
-- only when that state changes, so answers and resolutions made here are kept.
ALTER TABLE issues
    ADD COLUMN source text CHECK (source IS NULL OR source IN ('github')),
    ADD COLUMN source_key text CHECK (source_key IS NULL OR length(source_key) BETWEEN 1 AND 300),
    ADD COLUMN source_url text CHECK (source_url IS NULL OR length(source_url) <= 500),
    ADD COLUMN source_state text CHECK (source_state IS NULL OR length(source_state) <= 40),
    ADD COLUMN source_updated_at timestamptz,
    ADD CONSTRAINT issues_source_pair CHECK ((source IS NULL) = (source_key IS NULL));
CREATE UNIQUE INDEX issues_source_key ON issues(project_id, source, source_key) WHERE source IS NOT NULL;
