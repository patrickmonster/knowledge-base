-- 3단계 · 지식 품질과 AI 작업 흐름
-- Numbered 012 because 011 is reserved by the team-rollout branch; keep this
-- script after 011 in migrationScripts when the branches are merged.

-- Glossary documents define a term and its synonyms; search expands queries
-- with them. Drop the original kind CHECK by definition, not by guessed name.
DO $$
DECLARE c record;
BEGIN
  FOR c IN SELECT conname FROM pg_constraint
    WHERE conrelid='documents'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%kind%'
  LOOP
    EXECUTE format('ALTER TABLE documents DROP CONSTRAINT %I', c.conname);
  END LOOP;
END $$;
ALTER TABLE documents ADD CONSTRAINT documents_kind_check
  CHECK (kind IN ('brief','decision','history','artifact','reference','glossary'));

-- Recheck owners (재확인 담당자). A document is due when neither an update nor
-- a confirmation happened within interval_days.
CREATE TABLE recheck_assignments (
    document_id uuid PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id),
    owner_id uuid NOT NULL REFERENCES users(id),
    interval_days integer NOT NULL DEFAULT 90 CHECK (interval_days BETWEEN 1 AND 3650),
    note text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
    last_checked_at timestamptz,
    last_checked_by uuid REFERENCES users(id),
    check_note text NOT NULL DEFAULT '' CHECK (length(check_note) <= 1000),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX recheck_assignments_owner ON recheck_assignments(project_id, owner_id);

-- Context usage feedback: which start_task sources were actually used.
--   used        relied on as evidence
--   unused      offered by start_task but not used
--   misleading  offered or found but wrong/outdated for the task
CREATE TABLE task_source_feedback (
    task_id uuid NOT NULL REFERENCES task_runs(id) ON DELETE CASCADE,
    document_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id),
    verdict text NOT NULL CHECK (verdict IN ('used','unused','misleading')),
    offered boolean NOT NULL,
    rank integer CHECK (rank IS NULL OR rank >= 1),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, document_id)
);
CREATE INDEX task_source_feedback_project ON task_source_feedback(project_id, created_at DESC);

-- Task runs: feedback summary and abandoned (정리) state.
ALTER TABLE task_runs ADD COLUMN feedback_note text NOT NULL DEFAULT '' CHECK (length(feedback_note) <= 2000);
ALTER TABLE task_runs ADD COLUMN feedback_at timestamptz;
ALTER TABLE task_runs ADD COLUMN closed_reason text CHECK (closed_reason IS NULL OR length(closed_reason) BETWEEN 1 AND 1000);
ALTER TABLE task_runs ADD COLUMN closed_by uuid REFERENCES users(id);
ALTER TABLE task_runs ADD COLUMN closed_at timestamptz;
DO $$
DECLARE c record;
BEGIN
  FOR c IN SELECT conname FROM pg_constraint
    WHERE conrelid='task_runs'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%status%'
  LOOP
    EXECUTE format('ALTER TABLE task_runs DROP CONSTRAINT %I', c.conname);
  END LOOP;
END $$;
ALTER TABLE task_runs ADD CONSTRAINT task_runs_status_check CHECK (status IN ('open','completed','abandoned'));
ALTER TABLE task_runs ADD CONSTRAINT task_runs_state_check CHECK (
  (status='open' AND completion IS NULL AND completion_hash IS NULL AND completed_at IS NULL AND closed_at IS NULL)
  OR (status='completed' AND completion IS NOT NULL AND completion_hash IS NOT NULL AND completed_at IS NOT NULL AND closed_at IS NULL)
  OR (status='abandoned' AND completion IS NULL AND completion_hash IS NULL AND completed_at IS NULL
      AND closed_at IS NOT NULL AND closed_reason IS NOT NULL AND closed_by IS NOT NULL));
CREATE INDEX task_runs_open ON task_runs(project_id, started_at) WHERE status='open';
