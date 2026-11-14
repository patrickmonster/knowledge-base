package kb

// registerQualityTools adds the knowledge-quality and AI workflow tools:
// duplicate/conflicting decision detection, recheck owners and digests,
// context usage feedback and retrieval metrics, and long-open task cleanup.
// Glossary expansion and decision candidates live in existing tools
// (search_knowledge, create_document, finish_task).
func registerQualityTools(t Tools, s *Store) {
	add(t, s, "find_similar_decisions", "Find active or draft decisions similar to an existing decision: possible duplicates or conflicts. Suggestions only: confirm with the user, then link_documents supersedes (newer replaces older) or related. Already linked documents are excluded.", false,
		func(in SimilarDecisionsInput) string { return in.ProjectID }, findSimilarDecisions)

	add(t, s, "assign_recheck_owner", "Assign or change the recheck owner (재확인 담당자) of a document and how often (interval_days, default 90) it must be confirmed. remove=true deletes the assignment. History documents are not rechecked.", true,
		func(in RecheckOwnerInput) string { return in.ProjectID }, assignRecheckOwner)
	add(t, s, "confirm_recheck", "Record that a document was verified and still holds, without changing its content; restarts its recheck interval. Only after actually checking. If it is wrong, update_document instead.", true,
		func(in ConfirmRecheckInput) string { return in.ProjectID }, confirmRecheck)
	add(t, s, "list_recheck_assignments", "List recheck owner assignments with next_due_at, optionally for one owner_id.", false,
		func(in RecheckDigestInput) string { return in.ProjectID }, listRecheckAssignments)
	add(t, s, "get_recheck_digest", "Summarize documents due for re-checking, grouped by owner, with a Markdown summary for sharing. include_unassigned adds stale active reference/brief documents without an owner.", false,
		func(in RecheckDigestInput) string { return in.ProjectID }, recheckDigest)

	add(t, s, "record_context_feedback", "Record which documents a task actually relied on (used_sources), which were misleading, and what was missing. start_task sources not listed count as unused. Replaces earlier feedback for the task; only its creator may record. finish_task used_sources does the same.", true,
		func(in ContextFeedbackInput) string { return in.ProjectID }, s.recordContextFeedbackTool)
	add(t, s, "get_retrieval_metrics", "Retrieval quality from context feedback over the last days: precision (offered sources used), coverage (used evidence that was offered), hit rate, MRR, most used, offered-but-never-used and misleading documents, and recent missing-knowledge notes.", false,
		func(in RetrievalMetricsInput) string { return in.ProjectID }, retrievalMetrics)
	add(t, s, "get_usage_stats", "Project usage statistics over the last days (1–365, default 30, Asia/Seoul days): tool calls, denied/errors, active users, searches, document reads and writes, tasks started/completed/abandoned, a zero-filled daily series, top actions, top users, most read documents, and the current inventory (documents by kind/status, open tasks and issues, attachments, members, embeddings).", false,
		func(in UsageStatsInput) string { return in.ProjectID }, usageStats)

	add(t, s, "close_task_run", "Close an open task that will not be finished (interrupted or abandoned) with a reason; it becomes abandoned and can no longer be finished. Only its creator or the project owner. Use finish_task instead when work was done.", true,
		func(in CloseTaskInput) string { return in.ProjectID }, closeTaskRun)
	add(t, s, "cleanup_stale_task_runs", "Preview (default) or close (apply=true, project owner only, reason required) open tasks started at least older_than_days ago (default 7), at most 200 per call.", true,
		func(in CleanupTasksInput) string { return in.ProjectID }, cleanupStaleTaskRuns)
}
