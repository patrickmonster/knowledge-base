package kb

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

// Project rules are commit and work rules an AI client follows on a project.
//
//   - project (소유자 규칙): written by the owner or a project admin, from the
//     signed-in web console only, and applied to every member's work.
//   - personal (개별 규칙): each member's own rules, visible only to that member,
//     freely added or changed (also through a writable MCP key).
//
// Project rules win when a personal rule conflicts with them.
//
// A rule can be switched off (enabled=false, 미사용) without deleting it. Disabled
// rules are listed only on request (the web console) and never reach AI task context.
const (
	maxProjectRules  = 50
	maxPersonalRules = 50
	rulesGuidance    = "Rules are written by authorized project members, unlike sources. Follow project_rules (set by the project owner or admins) for commits and work on this project, and personal_rules (your user's own preferences) where they do not conflict. Project rules win on conflict. If a rule is unclear or blocks the task, ask the user instead of guessing."
)

type Rule struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Scope     string `json:"scope"`
	UserID    string `json:"user_id,omitempty"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Enabled   bool   `json:"enabled"`
	Revision  int    `json:"revision"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type RuleSet struct {
	ProjectRules  []Rule `json:"project_rules"`
	PersonalRules []Rule `json:"personal_rules"`
	// CanManageProjectRules tells the caller whether the web console lets them edit project rules.
	CanManageProjectRules bool   `json:"can_manage_project_rules"`
	Guidance              string `json:"guidance"`
}

type RuleText struct {
	Category string `json:"category,omitempty" jsonschema:"commit, work (default), or other"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

type SaveRuleInput struct {
	ProjectID        string `json:"project_id"`
	Scope            string `json:"scope" jsonschema:"personal (your own rules) or project (owner/admin rules; web console only)"`
	RuleID           string `json:"rule_id,omitempty" jsonschema:"Omit to add a rule; set with expected_revision to change one"`
	ExpectedRevision int    `json:"expected_revision,omitempty"`
	Category         string `json:"category,omitempty" jsonschema:"commit, work (default), or other"`
	Title            string `json:"title"`
	Body             string `json:"body"`
}

type RuleRef struct {
	ProjectID string `json:"project_id"`
	RuleID    string `json:"rule_id"`
}

type ListRulesInput struct {
	ProjectID       string `json:"project_id"`
	IncludeDisabled bool   `json:"include_disabled,omitempty" jsonschema:"Also return rules switched off (enabled=false). Disabled rules must not be followed; omit when reading rules to work by."`
}

type SetRuleEnabledInput struct {
	ProjectID string `json:"project_id"`
	RuleID    string `json:"rule_id"`
	Enabled   bool   `json:"enabled" jsonschema:"true to use the rule (사용), false to switch it off (미사용)"`
}

// canManageProjectRules: the owner and project admins.
func canManageProjectRules(role string) bool { return role == "owner" || role == "admin" }

func validateRuleText(r *RuleText) error {
	if r.Category == "" {
		r.Category = "work"
	}
	if r.Category != "commit" && r.Category != "work" && r.Category != "other" {
		return problem("INVALID_INPUT", "category must be commit, work, or other.")
	}
	if err := bounded("title", r.Title, 1, 200); err != nil {
		return err
	}
	return bounded("body", r.Body, 1, 4000)
}

func projectRuleWriter(a Identity, role string) error {
	if !canManageProjectRules(role) {
		return problem("FORBIDDEN", "Only the project owner or an admin can change project rules.")
	}
	if a.KeyID != "" {
		return problem("FORBIDDEN", "Project rules can be changed only by a signed-in owner or admin in the web console.")
	}
	return nil
}

// listRules returns the caller's rules; disabled ones only when includeDisabled.
func listRules(ctx context.Context, q Querier, userID string, p Project, includeDisabled bool) (RuleSet, error) {
	rules, err := many[Rule](ctx, q, `SELECT to_jsonb(r) FROM project_rules r
		WHERE r.project_id=$1 AND (r.scope='project' OR r.user_id=$2) AND (r.enabled OR $3)
		ORDER BY r.scope, CASE r.category WHEN 'commit' THEN 0 WHEN 'work' THEN 1 ELSE 2 END, r.created_at, r.id`, p.ID, userID, includeDisabled)
	if err != nil {
		return RuleSet{}, err
	}
	out := RuleSet{ProjectRules: []Rule{}, PersonalRules: []Rule{}, CanManageProjectRules: canManageProjectRules(p.Role), Guidance: rulesGuidance}
	for _, r := range rules {
		if r.Scope == "project" {
			out.ProjectRules = append(out.ProjectRules, r)
		} else {
			out.PersonalRules = append(out.PersonalRules, r)
		}
	}
	return out, nil
}

// insertRules adds rules within the caller's transaction after checking the per-scope limit.
func insertRules(ctx context.Context, tx *sql.Tx, projectID, scope, userID, actorID string, rules []RuleText) ([]Rule, error) {
	limit := maxProjectRules
	if scope == "personal" {
		limit = maxPersonalRules
	}
	// Serialize rule additions per project so the limit holds under concurrency.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('project_rules:'||$1))`, projectID); err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_rules WHERE project_id=$1 AND scope=$2 AND ($2='project' OR user_id=$3::uuid)`,
		projectID, scope, nullableID(userID)).Scan(&count); err != nil {
		return nil, err
	}
	if count+len(rules) > limit {
		return nil, problem("INVALID_INPUT", "Too many rules: at most 50 project rules and 50 personal rules per member.")
	}
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		rule, err := one[Rule](ctx, tx, `WITH r AS (INSERT INTO project_rules(id,project_id,scope,user_id,category,title,body,created_by,updated_by)
			VALUES($1,$2,$3,$4::uuid,$5,$6,$7,$8,$8) RETURNING *) SELECT to_jsonb(r) FROM r`,
			uuid.NewString(), projectID, scope, nullableID(userID), r.Category, r.Title, r.Body, actorID)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

func saveRule(ctx context.Context, tx *sql.Tx, a Identity, in SaveRuleInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	text := RuleText{Category: in.Category, Title: in.Title, Body: in.Body}
	if err := validateRuleText(&text); err != nil {
		return nil, err
	}
	owner := ""
	switch in.Scope {
	case "project":
		if err := projectRuleWriter(a, p.Role); err != nil {
			return nil, err
		}
	case "personal":
		owner = a.User.ID
	default:
		return nil, problem("INVALID_INPUT", "scope must be personal or project.")
	}
	if in.RuleID == "" {
		if in.ExpectedRevision != 0 {
			return nil, problem("INVALID_INPUT", "expected_revision is only used with rule_id.")
		}
		rules, err := insertRules(ctx, tx, p.ID, in.Scope, owner, a.User.ID, []RuleText{text})
		if err != nil {
			return nil, err
		}
		return rules[0], nil
	}
	if err := validID(in.RuleID); err != nil {
		return nil, err
	}
	if in.ExpectedRevision < 1 {
		return nil, problem("INVALID_INPUT", "expected_revision is required to change a rule.")
	}
	current, err := lockRule(ctx, tx, a.User.ID, p.ID, in.RuleID)
	if err != nil {
		return nil, err
	}
	if current.Scope != in.Scope {
		return nil, problem("INVALID_INPUT", "A rule's scope cannot be changed; add a new rule instead.")
	}
	if current.Revision != in.ExpectedRevision {
		return nil, problem("CONFLICT", "The rule was changed by someone else. Reload it and try again.")
	}
	return one[Rule](ctx, tx, `WITH r AS (UPDATE project_rules SET category=$2,title=$3,body=$4,updated_by=$5,updated_at=now(),revision=revision+1
		WHERE id=$1 RETURNING *) SELECT to_jsonb(r) FROM r`, in.RuleID, text.Category, text.Title, text.Body, a.User.ID)
}

// lockRule returns a rule the caller may change: their own personal rule, or a
// project rule they manage. Other members' personal rules are reported as not found.
func lockRule(ctx context.Context, tx *sql.Tx, userID, projectID, ruleID string) (Rule, error) {
	r, err := one[Rule](ctx, tx, `SELECT to_jsonb(r) FROM project_rules r WHERE id=$1 AND project_id=$2
		AND (scope='project' OR user_id=$3) FOR UPDATE`, ruleID, projectID, userID)
	return r, notFound(err)
}

func deleteRule(ctx context.Context, tx *sql.Tx, a Identity, in RuleRef) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	if err := validID(in.RuleID); err != nil {
		return nil, err
	}
	r, err := lockRule(ctx, tx, a.User.ID, p.ID, in.RuleID)
	if err != nil {
		return nil, err
	}
	if r.Scope == "project" {
		if err := projectRuleWriter(a, p.Role); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_rules WHERE id=$1`, r.ID); err != nil {
		return nil, err
	}
	return map[string]any{"id": r.ID, "project_id": p.ID, "scope": r.Scope, "deleted": true}, nil
}

// setRuleEnabled switches a rule on (사용) or off (미사용) with the same permissions as
// deleting it. A real change bumps the revision so an open edit form sees a conflict;
// setting the current value again returns the rule unchanged.
func setRuleEnabled(ctx context.Context, tx *sql.Tx, a Identity, in SetRuleEnabledInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	if err := validID(in.RuleID); err != nil {
		return nil, err
	}
	r, err := lockRule(ctx, tx, a.User.ID, p.ID, in.RuleID)
	if err != nil {
		return nil, err
	}
	if r.Scope == "project" {
		if err := projectRuleWriter(a, p.Role); err != nil {
			return nil, err
		}
	}
	if r.Enabled == in.Enabled {
		return r, nil
	}
	return one[Rule](ctx, tx, `WITH r AS (UPDATE project_rules SET enabled=$2,updated_by=$3,updated_at=now(),revision=revision+1
		WHERE id=$1 RETURNING *) SELECT to_jsonb(r) FROM r`, r.ID, in.Enabled, a.User.ID)
}

func registerRuleTools(t Tools, s *Store) {
	add(t, s, "list_rules", "Read the commit and work rules for a project: project_rules set by the owner or admins (apply to everyone) and your own personal_rules. Follow them while working; project rules win on conflict. start_task also returns them in context.rules. Rules switched off (enabled=false) are left out unless include_disabled is set, and must not be followed.", false, func(in ListRulesInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ListRulesInput) (any, error) {
		p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
		if err != nil {
			return nil, err
		}
		return listRules(ctx, tx, a.User.ID, p, in.IncludeDisabled)
	})
	add(t, s, "save_rule", "Add or change a rule. scope personal: your own rules, any member. scope project: owner or admin in the web console only (MCP keys are refused). To change a rule pass rule_id and expected_revision.", true, func(in SaveRuleInput) string { return in.ProjectID }, saveRule)
	add(t, s, "delete_rule", "Delete one of your personal rules, or (owner/admin in the web console) a project rule.", true, func(in RuleRef) string { return in.ProjectID }, deleteRule)
	add(t, s, "set_rule_enabled", "Switch a rule on (enabled=true, 사용) or off (enabled=false, 미사용) without deleting it. Same permissions as delete_rule: your own personal rules, or (owner/admin in the web console) a project rule.", true, func(in SetRuleEnabledInput) string { return in.ProjectID }, setRuleEnabled)
}

// taskRules attaches the caller's rules to the task context; nil when there are none.
func taskRules(ctx context.Context, q Querier, userID string, p Project) (*RuleSet, error) {
	rules, err := listRules(ctx, q, userID, p, false)
	if err != nil {
		return nil, err
	}
	if len(rules.ProjectRules) == 0 && len(rules.PersonalRules) == 0 {
		return nil, nil
	}
	return &rules, nil
}
