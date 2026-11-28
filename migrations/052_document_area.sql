-- Wiki articles (and other documents) can belong to a shared work area so the
-- wiki index groups them by area. Deleting an area leaves its documents
-- without an area; they are never deleted.
ALTER TABLE documents ADD COLUMN area_id uuid REFERENCES work_areas(id) ON DELETE SET NULL;
CREATE INDEX documents_project_area ON documents(project_id, area_id) WHERE area_id IS NOT NULL;
