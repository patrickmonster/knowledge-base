CREATE TABLE attachments (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    document_id uuid REFERENCES documents(id),
    document_revision integer,
    filename text NOT NULL CHECK (length(filename) BETWEEN 1 AND 200),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 26214400),
    sha256 text,
    object_key text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','uploading','ready','deleting')),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attachments_project ON attachments(project_id,created_at DESC,id);
CREATE INDEX attachments_cleanup ON attachments(status,updated_at) WHERE status <> 'ready';
