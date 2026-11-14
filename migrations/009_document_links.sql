-- Typed links between documents of the same project.
--   supersedes  source (newer) replaces target (older); same kind only.
--   implements  source history carried out target decision or brief.
--   related     undirected; stored once in either direction.
-- Links are metadata: creating or removing one does not create a document
-- revision or touch chunks/vectors.
CREATE TABLE document_links (
    project_id uuid NOT NULL REFERENCES projects(id),
    source_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    target_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    relation text NOT NULL CHECK (relation IN ('supersedes','related','implements')),
    note text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, relation, target_id),
    CHECK (source_id <> target_id)
);
CREATE INDEX document_links_target ON document_links(target_id, relation);
CREATE INDEX document_links_project ON document_links(project_id, relation);
