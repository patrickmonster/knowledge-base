-- Search settings a global administrator changes in the web console, so the
-- values no longer need server environment variables. One row for the whole
-- server. A NULL column falls back to the environment value.
--
-- issue_score_floor: the lowest vector similarity a question or issue must
-- reach to join search_knowledge / search_across_projects results
-- (ISSUE_SCORE_FLOOR). 0 turns the floor off and returns every match, as
-- before this table existed.
CREATE TABLE search_settings (
  id boolean PRIMARY KEY DEFAULT true CHECK (id),
  issue_score_floor double precision CHECK (issue_score_floor = 0 OR issue_score_floor BETWEEN 0.1 AND 0.95),
  updated_by uuid NOT NULL REFERENCES users(id),
  updated_at timestamptz NOT NULL DEFAULT now()
);
