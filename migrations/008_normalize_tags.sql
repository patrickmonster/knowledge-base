-- Match the server-side tag rule (trim, drop empty, case-insensitive dedupe keeping
-- the first spelling and order) for documents saved before it existed. For example
-- finish_task histories stored "작업이력" twice. Tags are metadata; no revision is
-- created and content, chunks and vectors are unchanged.
UPDATE documents d
SET tags = n.tags
FROM (
  SELECT id, COALESCE((
    SELECT array_agg(t ORDER BY o)
    FROM (
      SELECT DISTINCT ON (lower(btrim(u.t))) btrim(u.t) AS t, u.o
      FROM unnest(documents.tags) WITH ORDINALITY AS u(t, o)
      WHERE btrim(u.t) <> ''
      ORDER BY lower(btrim(u.t)), u.o
    ) kept
  ), '{}') AS tags
  FROM documents
) n
WHERE d.id = n.id AND d.tags IS DISTINCT FROM n.tags;
