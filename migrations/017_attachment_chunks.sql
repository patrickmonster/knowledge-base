-- Chunked uploads through MCP tool calls (upload_attachment_chunk) for clients whose
-- network cannot reach upload_url. Chunks are appended in order to a pending
-- reservation, assembled once complete, then removed. Deleting the attachment row
-- (cleanup of expired or failed uploads) removes its chunks.
CREATE TABLE attachment_chunks (
    attachment_id uuid NOT NULL REFERENCES attachments(id) ON DELETE CASCADE,
    position bigint NOT NULL CHECK (position >= 0),
    data bytea NOT NULL CHECK (length(data) BETWEEN 1 AND 524288),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (attachment_id, position)
);
