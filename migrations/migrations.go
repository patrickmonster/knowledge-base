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

//go:embed 014_rule_enabled.sql
var RuleEnabled string

//go:embed 015_search_misses.sql
var SearchMisses string

//go:embed 016_mfa.sql
var MFA string

//go:embed 017_attachment_chunks.sql
var AttachmentChunks string

//go:embed 018_attachment_text.sql
var AttachmentText string

//go:embed 019_issue_sources.sql
var IssueSources string

//go:embed 020_task_pull_requests.sql
var TaskPullRequests string

//go:embed 021_github_webhooks.sql
var GitHubWebhooks string

//go:embed 022_search_timings.sql
var SearchTimings string

//go:embed 023_issue_attachments.sql
var IssueAttachments string

//go:embed 024_issue_attachment_parts.sql
var IssueAttachmentParts string

//go:embed 025_todos.sql
var Todos string

//go:embed 026_todo_task_link.sql
var TodoTaskLink string

//go:embed 027_issue_todo_link.sql
var IssueTodoLink string

//go:embed 028_webhook_plain_secret.sql
var WebhookPlainSecret string

//go:embed 029_project_repositories.sql
var ProjectRepositories string

//go:embed 030_search_thresholds.sql
var SearchThresholds string

//go:embed 031_todo_import.sql
var TodoImport string

//go:embed 032_task_scope.sql
var TaskScope string

//go:embed 033_document_notifications.sql
var DocumentNotifications string

//go:embed 034_recheck_digest_runs.sql
var RecheckDigestRuns string

//go:embed 037_discord.sql
var Discord string

//go:embed 035_retention_settings.sql
var RetentionSettings string

//go:embed 036_todo_in_review.sql
var TodoInReview string

//go:embed 038_discord_drop_guild_ids.sql
var DiscordDropGuildIDs string

//go:embed 039_discord_issue_channels.sql
var DiscordIssueChannels string

//go:embed 040_backfill_used_implements.sql
var BackfillUsedImplements string

//go:embed 041_issue_chunks.sql
var IssueChunks string

//go:embed 042_project_upstreams.sql
var ProjectUpstreams string

//go:embed 043_todo_relations.sql
var TodoRelations string

//go:embed 044_task_prompts.sql
var TaskPrompts string

//go:embed 045_search_settings.sql
var SearchSettings string

//go:embed 046_todo_pending_release.sql
var TodoPendingRelease string

//go:embed 047_todo_resolution.sql
var TodoResolution string
