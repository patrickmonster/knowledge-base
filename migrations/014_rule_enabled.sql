-- Rules can be switched off (미사용) without deleting them. Disabled rules stay
-- visible in the web console but are left out of what AI clients follow
-- (list_rules default, start_task/get_task_context context.rules).
ALTER TABLE project_rules ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;
