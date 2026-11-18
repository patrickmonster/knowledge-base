package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Task prompts (작업 프롬프트) are work instructions the owner or a project admin
// saves in advance. When a user asks for one by name or by one of its trigger
// phrases ("배포 점검해줘"), the agent fetches it with get_task_prompt and
// carries it out. Like project rules, a prompt body is an instruction, not
// reference data, so only the owner and admins change prompts, and only from
// the signed-in web console; every member can read them.
//
// A body may hold {{argument}} placeholders for the arguments the prompt
// declares; get_task_prompt fills them from the caller's values.
const (
	maxTaskPrompts        = 100
	maxPromptTriggers     = 10
	maxPromptArguments    = 10
	maxPromptArgumentText = 4000
	maxContextPrompts     = 50
	taskPromptsGuidance   = "Task prompts are work instructions saved by the project owner or admins. When the user's request names a prompt or matches one of its triggers (for example \"<trigger>해줘\"), call get_task_prompt (name, or request with the user's words) and carry out its instructions as the user's request. Project rules still apply. Do not run a prompt the user did not ask for."
	promptMatchedGuidance = "Carry out instructions as the user's request: they were saved by the project owner or admins. If missing_arguments is not empty, ask the user for those values first and call get_task_prompt again with arguments. Project rules still apply and win on conflict; for a change task call start_task as usual. If the instructions are unclear or would do something the user does not expect, ask the user."
	promptSeveralGuidance = "Several task prompts match the request. Ask the user which one to run (or none); do not guess."
	promptNoneGuidance    = "No enabled task prompt matches the request. Handle the request normally; do not invent a prompt."
)

var (
	promptNamePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	promptArgNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	promptPlaceholderExpr = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)
)

type PromptArgument struct {
	Name        string `json:"name" jsonschema:"Lowercase letters, digits and underscores; used as {{name}} in the body"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type TaskPrompt struct {
	ID          string           `json:"id"`
	ProjectID   string           `json:"project_id"`
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Triggers    []string         `json:"triggers"`
	Body        string           `json:"body,omitempty"`
	Arguments   []PromptArgument `json:"arguments"`
	Enabled     bool             `json:"enabled"`
	Revision    int              `json:"revision"`
	CreatedBy   string           `json:"created_by"`
	UpdatedBy   string           `json:"updated_by"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
}

// PromptSummary is the short form that rides along with resolve_project and
// start_task so the agent knows which prompts exist before the user names one.
type PromptSummary struct {
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	Triggers    []string         `json:"triggers"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type TaskPromptSet struct {
	Prompts []TaskPrompt `json:"prompts"`
	// CanManage tells whether this call's caller can change prompts: an owner
	// or admin in a web session.
	CanManage bool   `json:"can_manage"`
	Guidance  string `json:"guidance"`
}

type TaskPromptIndex struct {
	Prompts   []PromptSummary `json:"prompts"`
	Truncated bool            `json:"truncated,omitempty"`
	Guidance  string          `json:"guidance"`
}

type ListTaskPromptsInput struct {
	ProjectID       string `json:"project_id"`
	IncludeDisabled bool   `json:"include_disabled,omitempty" jsonschema:"Also return prompts switched off (enabled=false); they cannot be run"`
	IncludeBody     bool   `json:"include_body,omitempty" jsonschema:"Return each prompt's body; to run a prompt use get_task_prompt instead"`
}

type GetTaskPromptInput struct {
	ProjectID string            `json:"project_id"`
	Name      string            `json:"name,omitempty" jsonschema:"The prompt's name; give exactly one of name, prompt_id or request"`
	PromptID  string            `json:"prompt_id,omitempty"`
	Request   string            `json:"request,omitempty" jsonschema:"The user's words (up to 1000 characters); matched against prompt names and triggers"`
	Arguments map[string]string `json:"arguments,omitempty" jsonschema:"Values for the prompt's declared arguments, filled into its {{name}} placeholders"`
}

type TaskPromptResult struct {
	Matched          bool            `json:"matched"`
	Prompt           *TaskPrompt     `json:"prompt,omitempty"`
	Instructions     string          `json:"instructions,omitempty"`
	MissingArguments []string        `json:"missing_arguments,omitempty"`
	Candidates       []PromptSummary `json:"candidates,omitempty"`
	Guidance         string          `json:"guidance"`
}

type SaveTaskPromptInput struct {
	ProjectID        string           `json:"project_id"`
	PromptID         string           `json:"prompt_id,omitempty" jsonschema:"Omit to add a prompt; set with expected_revision to change one"`
	ExpectedRevision int              `json:"expected_revision,omitempty"`
	Name             string           `json:"name" jsonschema:"Unique per project: lowercase letters, digits and hyphens, up to 63"`
	Title            string           `json:"title"`
	Description      string           `json:"description,omitempty"`
	Triggers         []string         `json:"triggers,omitempty" jsonschema:"Phrases that call this prompt, such as 배포 점검; at most 10"`
	Body             string           `json:"body" jsonschema:"The instructions, up to 20000 characters; {{argument}} placeholders are filled from arguments"`
	Arguments        []PromptArgument `json:"arguments,omitempty"`
}

type TaskPromptRef struct {
	ProjectID string `json:"project_id"`
	PromptID  string `json:"prompt_id"`
}

type SetTaskPromptEnabledInput struct {
	ProjectID string `json:"project_id"`
	PromptID  string `json:"prompt_id"`
	Enabled   bool   `json:"enabled"`
}

func taskPromptWriter(a Identity, role string) error {
	if !canManageProjectRules(role) {
		return problem("FORBIDDEN", "Only the project owner or an admin can change task prompts.")
	}
	if a.KeyID != "" {
		return problem("FORBIDDEN", "Task prompts can be changed only by a signed-in owner or admin in the web console.")
	}
	return nil
}

// normalizePromptText folds case and drops spaces and punctuation, so
// "배포 점검 해줘" and "배포점검해줘!" compare equal.
func normalizePromptText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validateTaskPrompt(in *SaveTaskPromptInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if !promptNamePattern.MatchString(in.Name) {
		return problem("INVALID_INPUT", "name must be 1–63 lowercase letters, digits or hyphens, starting with a letter or digit.")
	}
	if err := bounded("title", in.Title, 1, 200); err != nil {
		return err
	}
	if utf8.RuneCountInString(in.Description) > 1000 {
		return problem("INVALID_INPUT", "description must have at most 1000 characters.")
	}
	if err := bounded("body", in.Body, 1, 20000); err != nil {
		return err
	}
	if len(in.Triggers) > maxPromptTriggers {
		return problem("INVALID_INPUT", "At most 10 triggers.")
	}
	triggers := make([]string, 0, len(in.Triggers))
	seen := map[string]bool{}
	for _, t := range in.Triggers {
		t = strings.Join(strings.Fields(t), " ")
		if err := bounded("trigger", t, 1, 100); err != nil {
			return err
		}
		key := normalizePromptText(t)
		if key == "" {
			return problem("INVALID_INPUT", "A trigger needs at least one letter or digit.")
		}
		if !seen[key] {
			seen[key] = true
			triggers = append(triggers, t)
		}
	}
	in.Triggers = triggers
	if len(in.Arguments) > maxPromptArguments {
		return problem("INVALID_INPUT", "At most 10 arguments.")
	}
	declared := map[string]bool{}
	for i, arg := range in.Arguments {
		if !promptArgNamePattern.MatchString(arg.Name) {
			return problem("INVALID_INPUT", "Argument names must be 1–40 lowercase letters, digits or underscores, starting with a letter.")
		}
		if declared[arg.Name] {
			return problem("INVALID_INPUT", "Argument names must be unique.")
		}
		if utf8.RuneCountInString(arg.Description) > 200 {
			return problem("INVALID_INPUT", "An argument description must have at most 200 characters.")
		}
		declared[arg.Name] = true
		in.Arguments[i].Description = strings.TrimSpace(arg.Description)
	}
	if in.Arguments == nil {
		in.Arguments = []PromptArgument{}
	}
	for _, m := range promptPlaceholderExpr.FindAllStringSubmatch(in.Body, -1) {
		if !declared[m[1]] {
			return problem("INVALID_INPUT", "The body uses {{"+m[1]+"}}, which is not a declared argument.")
		}
	}
	return nil
}

func listTaskPrompts(ctx context.Context, q Querier, a Identity, p Project, in ListTaskPromptsInput) (TaskPromptSet, error) {
	prompts, err := many[TaskPrompt](ctx, q, `SELECT CASE WHEN $3 THEN to_jsonb(t) ELSE to_jsonb(t)-'body' END FROM task_prompts t
		WHERE t.project_id=$1 AND (t.enabled OR $2) ORDER BY t.name`, p.ID, in.IncludeDisabled, in.IncludeBody)
	if err != nil {
		return TaskPromptSet{}, err
	}
	if prompts == nil {
		prompts = []TaskPrompt{}
	}
	return TaskPromptSet{Prompts: prompts, CanManage: canManageProjectRules(p.Role) && a.KeyID == "", Guidance: taskPromptsGuidance}, nil
}

func summarizePrompt(t TaskPrompt) PromptSummary {
	return PromptSummary{Name: t.Name, Title: t.Title, Description: t.Description, Triggers: t.Triggers, Arguments: t.Arguments}
}

// taskPromptIndex lists the enabled prompts for resolve_project and start_task;
// nil when the project has none.
func taskPromptIndex(ctx context.Context, q Querier, projectID string) (*TaskPromptIndex, error) {
	prompts, err := many[TaskPrompt](ctx, q, `SELECT to_jsonb(t)-'body' FROM task_prompts t
		WHERE t.project_id=$1 AND t.enabled ORDER BY t.name LIMIT $2`, projectID, maxContextPrompts+1)
	if err != nil || len(prompts) == 0 {
		return nil, err
	}
	out := &TaskPromptIndex{Prompts: []PromptSummary{}, Guidance: taskPromptsGuidance}
	if len(prompts) > maxContextPrompts {
		prompts, out.Truncated = prompts[:maxContextPrompts], true
	}
	for _, t := range prompts {
		out.Prompts = append(out.Prompts, summarizePrompt(t))
	}
	return out, nil
}

// matchTaskPrompts returns the prompts whose name or a trigger appears in the request.
func matchTaskPrompts(prompts []TaskPrompt, request string) []TaskPrompt {
	text := normalizePromptText(request)
	if text == "" {
		return nil
	}
	var out []TaskPrompt
	for _, t := range prompts {
		keys := []string{normalizePromptText(t.Name)}
		for _, trigger := range t.Triggers {
			keys = append(keys, normalizePromptText(trigger))
		}
		if slices.ContainsFunc(keys, func(k string) bool { return k != "" && strings.Contains(text, k) }) {
			out = append(out, t)
		}
	}
	return out
}

// renderTaskPrompt fills {{argument}} placeholders. Missing required arguments
// keep their placeholder and are reported; missing optional ones become empty.
func renderTaskPrompt(t TaskPrompt, values map[string]string) (string, []string, error) {
	declared := map[string]PromptArgument{}
	for _, arg := range t.Arguments {
		declared[arg.Name] = arg
	}
	for name, value := range values {
		if _, ok := declared[name]; !ok {
			return "", nil, problem("INVALID_INPUT", "The prompt has no argument named "+name+".")
		}
		if utf8.RuneCountInString(value) > maxPromptArgumentText {
			return "", nil, problem("INVALID_INPUT", "An argument value must have at most 4000 characters.")
		}
	}
	missing := []string{}
	for _, arg := range t.Arguments {
		if arg.Required && strings.TrimSpace(values[arg.Name]) == "" {
			missing = append(missing, arg.Name)
		}
	}
	body := promptPlaceholderExpr.ReplaceAllStringFunc(t.Body, func(m string) string {
		name := promptPlaceholderExpr.FindStringSubmatch(m)[1]
		if _, ok := declared[name]; !ok {
			return m
		}
		if v := strings.TrimSpace(values[name]); v != "" {
			return v
		}
		if declared[name].Required {
			return m
		}
		return ""
	})
	return body, missing, nil
}

func getTaskPrompt(ctx context.Context, tx *sql.Tx, a Identity, in GetTaskPromptInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	given := 0
	for _, s := range []string{in.Name, in.PromptID, in.Request} {
		if strings.TrimSpace(s) != "" {
			given++
		}
	}
	if given != 1 {
		return nil, problem("INVALID_INPUT", "Give exactly one of name, prompt_id or request.")
	}
	var chosen TaskPrompt
	switch {
	case in.PromptID != "":
		if err := validID(in.PromptID); err != nil {
			return nil, err
		}
		chosen, err = one[TaskPrompt](ctx, tx, `SELECT to_jsonb(t) FROM task_prompts t WHERE t.id=$1 AND t.project_id=$2 AND t.enabled`, in.PromptID, p.ID)
		if err != nil {
			return nil, notFound(err)
		}
	case in.Name != "":
		chosen, err = one[TaskPrompt](ctx, tx, `SELECT to_jsonb(t) FROM task_prompts t WHERE t.name=$1 AND t.project_id=$2 AND t.enabled`, strings.TrimSpace(in.Name), p.ID)
		if err != nil {
			return nil, notFound(err)
		}
	default:
		if err := bounded("request", in.Request, 1, 1000); err != nil {
			return nil, err
		}
		prompts, err := many[TaskPrompt](ctx, tx, `SELECT to_jsonb(t) FROM task_prompts t WHERE t.project_id=$1 AND t.enabled ORDER BY t.name`, p.ID)
		if err != nil {
			return nil, err
		}
		matches := matchTaskPrompts(prompts, in.Request)
		switch len(matches) {
		case 0:
			return TaskPromptResult{Guidance: promptNoneGuidance}, nil
		case 1:
			chosen = matches[0]
		default:
			out := TaskPromptResult{Guidance: promptSeveralGuidance}
			for _, m := range matches {
				out.Candidates = append(out.Candidates, summarizePrompt(m))
			}
			return out, nil
		}
	}
	instructions, missing, err := renderTaskPrompt(chosen, in.Arguments)
	if err != nil {
		return nil, err
	}
	chosen.Body = ""
	out := TaskPromptResult{Matched: true, Prompt: &chosen, Instructions: instructions, Guidance: promptMatchedGuidance}
	if len(missing) > 0 {
		out.MissingArguments = missing
	}
	return out, nil
}

func lockTaskPrompt(ctx context.Context, tx *sql.Tx, projectID, promptID string) (TaskPrompt, error) {
	if err := validID(promptID); err != nil {
		return TaskPrompt{}, err
	}
	t, err := one[TaskPrompt](ctx, tx, `SELECT to_jsonb(t) FROM task_prompts t WHERE id=$1 AND project_id=$2 FOR UPDATE`, promptID, projectID)
	return t, notFound(err)
}

func saveTaskPrompt(ctx context.Context, tx *sql.Tx, a Identity, in SaveTaskPromptInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	if err := taskPromptWriter(a, p.Role); err != nil {
		return nil, err
	}
	if err := validateTaskPrompt(&in); err != nil {
		return nil, err
	}
	triggers, _ := json.Marshal(in.Triggers)
	args, _ := json.Marshal(in.Arguments)
	if in.PromptID == "" {
		if in.ExpectedRevision != 0 {
			return nil, problem("INVALID_INPUT", "expected_revision is only used with prompt_id.")
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('task_prompts:'||$1))`, p.ID); err != nil {
			return nil, err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_prompts WHERE project_id=$1`, p.ID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= maxTaskPrompts {
			return nil, problem("INVALID_INPUT", "Too many task prompts: at most 100 per project.")
		}
		if err := promptNameFree(ctx, tx, p.ID, in.Name, ""); err != nil {
			return nil, err
		}
		return one[TaskPrompt](ctx, tx, `WITH t AS (INSERT INTO task_prompts(id,project_id,name,title,description,triggers,body,arguments,created_by,updated_by)
			VALUES($1,$2,$3,$4,$5,ARRAY(SELECT jsonb_array_elements_text($6::jsonb)),$7,$8::jsonb,$9,$9) RETURNING *) SELECT to_jsonb(t) FROM t`,
			uuid.NewString(), p.ID, in.Name, strings.TrimSpace(in.Title), strings.TrimSpace(in.Description), string(triggers), in.Body, string(args), a.User.ID)
	}
	if in.ExpectedRevision < 1 {
		return nil, problem("INVALID_INPUT", "expected_revision is required to change a task prompt.")
	}
	current, err := lockTaskPrompt(ctx, tx, p.ID, in.PromptID)
	if err != nil {
		return nil, err
	}
	if current.Revision != in.ExpectedRevision {
		return nil, problem("CONFLICT", "The task prompt was changed by someone else. Reload it and try again.")
	}
	if err := promptNameFree(ctx, tx, p.ID, in.Name, current.ID); err != nil {
		return nil, err
	}
	return one[TaskPrompt](ctx, tx, `WITH t AS (UPDATE task_prompts SET name=$2,title=$3,description=$4,triggers=ARRAY(SELECT jsonb_array_elements_text($5::jsonb)),
		body=$6,arguments=$7::jsonb,updated_by=$8,updated_at=now(),revision=revision+1 WHERE id=$1 RETURNING *) SELECT to_jsonb(t) FROM t`,
		current.ID, in.Name, strings.TrimSpace(in.Title), strings.TrimSpace(in.Description), string(triggers), in.Body, string(args), a.User.ID)
}

func promptNameFree(ctx context.Context, tx *sql.Tx, projectID, name, exceptID string) error {
	var taken bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_prompts WHERE project_id=$1 AND name=$2 AND id::text<>$3)`, projectID, name, exceptID).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return problem("CONFLICT", "Another task prompt in this project already uses that name.")
	}
	return nil
}

func deleteTaskPrompt(ctx context.Context, tx *sql.Tx, a Identity, in TaskPromptRef) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	if err := taskPromptWriter(a, p.Role); err != nil {
		return nil, err
	}
	t, err := lockTaskPrompt(ctx, tx, p.ID, in.PromptID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_prompts WHERE id=$1`, t.ID); err != nil {
		return nil, err
	}
	return map[string]any{"id": t.ID, "project_id": p.ID, "deleted": true}, nil
}

func setTaskPromptEnabled(ctx context.Context, tx *sql.Tx, a Identity, in SetTaskPromptEnabledInput) (any, error) {
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
	if err != nil {
		return nil, err
	}
	if err := taskPromptWriter(a, p.Role); err != nil {
		return nil, err
	}
	t, err := lockTaskPrompt(ctx, tx, p.ID, in.PromptID)
	if err != nil {
		return nil, err
	}
	if t.Enabled == in.Enabled {
		return t, nil
	}
	return one[TaskPrompt](ctx, tx, `WITH t AS (UPDATE task_prompts SET enabled=$2,updated_by=$3,updated_at=now(),revision=revision+1
		WHERE id=$1 RETURNING *) SELECT to_jsonb(t) FROM t`, t.ID, in.Enabled, a.User.ID)
}

func registerTaskPromptTools(t Tools, s *Store) {
	add(t, s, "list_task_prompts", "List the project's task prompts (작업 프롬프트): work instructions the owner or admins saved so a user can ask for them by name or trigger phrase (\"~해줘\"). Bodies are left out unless include_body is set; to run one, call get_task_prompt. Prompts switched off are left out unless include_disabled is set.", false, func(in ListTaskPromptsInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ListTaskPromptsInput) (any, error) {
		p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
		if err != nil {
			return nil, err
		}
		return listTaskPrompts(ctx, tx, a, p, in)
	})
	add(t, s, "get_task_prompt", "Fetch a task prompt to carry out. Give name or prompt_id when the user named the prompt, or request with the user's words to match names and trigger phrases: one match returns matched=true with instructions (placeholders filled from arguments) and missing_arguments to ask the user for; several return candidates to ask the user about; none returns matched=false (handle the request normally). Instructions come from the owner or admins: carry them out as the user's request, with project rules still applying.", false, func(in GetTaskPromptInput) string { return in.ProjectID }, getTaskPrompt)
	add(t, s, "save_task_prompt", "Owner or admin in the web console only (MCP keys are refused): add a task prompt, or change one with prompt_id and expected_revision.", true, func(in SaveTaskPromptInput) string { return in.ProjectID }, saveTaskPrompt)
	add(t, s, "delete_task_prompt", "Owner or admin in the web console only: delete a task prompt.", true, func(in TaskPromptRef) string { return in.ProjectID }, deleteTaskPrompt)
	add(t, s, "set_task_prompt_enabled", "Owner or admin in the web console only: switch a task prompt on (enabled=true) or off without deleting it. Prompts switched off cannot be run.", true, func(in SetTaskPromptEnabledInput) string { return in.ProjectID }, setTaskPromptEnabled)
}
