package migrations

import _ "embed"

//go:embed 001_initial.sql
var Initial string

//go:embed 002_auth.sql
var Auth string

//go:embed 003_embeddings.sql
var Embeddings string

//go:embed 004_permanent_keys.sql
var PermanentKeys string

//go:embed 005_task_workflow.sql
var TaskWorkflow string

//go:embed 006_attachments.sql
var Attachments string

//go:embed 007_attachment_upload_tokens.sql
var AttachmentUploadTokens string

//go:embed 008_normalize_tags.sql
var NormalizeTags string

//go:embed 009_document_links.sql
var DocumentLinks string

//go:embed 010_issues.sql
var Issues string

//go:embed 011_team_rollout.sql
var TeamRollout string

//go:embed 012_knowledge_quality.sql
var KnowledgeQuality string

//go:embed 013_project_rules.sql
var ProjectRules string

//go:embed 014_mfa.sql
var MFA string
