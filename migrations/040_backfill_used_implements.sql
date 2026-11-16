-- Backfill: finish_task now links the decisions and briefs a task relied on
-- (used_sources) to its history with implements. Apply the same rule to tasks
-- completed before that change. The source is the history the task saved
-- (task_runs.completion.documents, kind history); the targets are documents
-- recorded as 'used' in task_source_feedback that are decisions or briefs of
-- the same project. Existing links are kept; history bodies are not rewritten.
INSERT INTO document_links(project_id, source_id, target_id, relation, note, created_by, created_at)
SELECT DISTINCT ON (h.id, d.id) r.project_id, h.id, d.id, 'implements', 'used_sources 소급 연결', r.created_by, COALESCE(r.completed_at, now())
FROM task_runs r
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.completion->'documents', '[]'::jsonb)) saved
JOIN documents h ON h.id::text = saved->>'id' AND h.project_id = r.project_id AND h.kind = 'history'
JOIN task_source_feedback f ON f.task_id = r.id AND f.verdict = 'used'
JOIN documents d ON d.id = f.document_id AND d.project_id = r.project_id AND d.kind IN ('decision','brief')
WHERE r.status = 'completed' AND saved->>'kind' = 'history'
ORDER BY h.id, d.id, r.completed_at
ON CONFLICT (source_id, relation, target_id) DO NOTHING;
