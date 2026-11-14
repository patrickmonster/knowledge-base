-- One-time upload capability for MCP clients that cannot read their own Bearer key
-- (for example hosted connectors). Only the SHA-256 hash is stored. The token is
-- cleared when the upload starts and is bound to the key that reserved the file.
ALTER TABLE attachments ADD COLUMN upload_token_hash text UNIQUE;
ALTER TABLE attachments ADD COLUMN upload_key_id uuid REFERENCES api_keys(id);
