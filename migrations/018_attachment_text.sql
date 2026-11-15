-- Text extracted from uploaded PDF, DOCX and XLSX files is stored as an artifact
-- document so the existing chunk, embedding and search paths index it.
-- text_status: none (format not extracted or extraction disabled), extracted,
-- empty (no text found, e.g. a scanned PDF that needs OCR), failed (text_error
-- holds a stable code such as INVALID_FILE, ENCRYPTED, TOO_LARGE, TIMEOUT).
ALTER TABLE attachments
    ADD COLUMN text_status text NOT NULL DEFAULT 'none' CHECK (text_status IN ('none','extracted','empty','failed')),
    ADD COLUMN text_error text CHECK (text_error IS NULL OR length(text_error) <= 40),
    ADD COLUMN text_document_id uuid REFERENCES documents(id),
    ADD COLUMN text_truncated boolean NOT NULL DEFAULT false;
