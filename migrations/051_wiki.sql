ALTER TABLE documents DROP CONSTRAINT documents_kind_check;
ALTER TABLE documents ADD CONSTRAINT documents_kind_check
  CHECK (kind IN ('brief','decision','history','artifact','reference','glossary','wiki'));

ALTER TABLE document_links DROP CONSTRAINT document_links_relation_check;
ALTER TABLE document_links ADD CONSTRAINT document_links_relation_check
  CHECK (relation IN ('supersedes','related','implements','cites'));
