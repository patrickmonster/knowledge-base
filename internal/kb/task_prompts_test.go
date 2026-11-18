package kb

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestRenderAndMatchTaskPrompts(t *testing.T) {
	deploy := TaskPrompt{Name: "deploy-check", Triggers: []string{"배포 점검"}, Body: "{{env}} 환경을 점검한다.{{note}}",
		Arguments: []PromptArgument{{Name: "env", Required: true}, {Name: "note"}}}
	weekly := TaskPrompt{Name: "weekly-report", Triggers: []string{"주간 보고", "주간보고서"}, Body: "b"}
	all := []TaskPrompt{deploy, weekly}

	for request, want := range map[string][]string{
		"배포점검 해줘!":          {"deploy-check"},
		"운영 배포 점검해 줘":       {"deploy-check"},
		"Deploy-Check 돌려줘":  {"deploy-check"},
		"주간 보고서 써줘":         {"weekly-report"},
		"배포 점검하고 주간 보고도 해줘": {"deploy-check", "weekly-report"},
		"아무거나 해줘":           nil,
		"   ":               nil,
	} {
		var got []string
		for _, m := range matchTaskPrompts(all, request) {
			got = append(got, m.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("match %q = %v, want %v", request, got, want)
		}
	}

	body, missing, err := renderTaskPrompt(deploy, nil)
	if err != nil || body != "{{env}} 환경을 점검한다." || !slices.Equal(missing, []string{"env"}) {
		t.Fatalf("missing required: %q %v %v", body, missing, err)
	}
	body, missing, err = renderTaskPrompt(deploy, map[string]string{"env": "운영", "note": " 로그도 본다."})
	if err != nil || body != "운영 환경을 점검한다.로그도 본다." || len(missing) != 0 {
		t.Fatalf("filled: %q %v %v", body, missing, err)
	}
	_, _, err = renderTaskPrompt(deploy, map[string]string{"unknown": "x"})
	denied(t, err, "INVALID_INPUT")

	for _, in := range []SaveTaskPromptInput{
		{Name: "Bad Name", Title: "t", Body: "b"},
		{Name: "ok", Title: "t", Body: "{{nope}}"},
		{Name: "ok", Title: "t", Body: "b", Arguments: []PromptArgument{{Name: "a"}, {Name: "a"}}},
		{Name: "ok", Title: "t", Body: "b", Arguments: []PromptArgument{{Name: "Bad"}}},
		{Name: "ok", Title: "t", Body: "b", Triggers: []string{"!!"}},
		{Name: "ok", Title: "", Body: "b"},
	} {
		if err := validateTaskPrompt(&in); err == nil {
			t.Errorf("accepted invalid prompt %+v", in)
		}
	}
	in := SaveTaskPromptInput{Name: "ok", Title: "t", Body: "{{ a }}", Triggers: []string{" 배포  점검 ", "배포점검"}, Arguments: []PromptArgument{{Name: "a"}}}
	if err := validateTaskPrompt(&in); err != nil || !slices.Equal(in.Triggers, []string{"배포 점검"}) {
		t.Fatalf("normalized triggers: %v %v", in.Triggers, err)
	}
}

func TestTaskPrompts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	admin := inviteAs(t, s, owner, "prompts-admin@example.com")
	editor := inviteAs(t, s, owner, "prompts-editor@example.com")
	viewer := inviteAs(t, s, owner, "prompts-viewer@example.com")
	outsider := inviteAs(t, s, owner, "prompts-outsider@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "prompted", Name: "prompted"}).(Project)
	for _, m := range []struct {
		id   Identity
		role string
	}{{admin, "admin"}, {editor, "editor"}, {viewer, "viewer"}} {
		mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: m.id.User.ID, Role: m.role})
	}
	keyOut, err := s.CreateKey(ctx, owner, KeyInput{Name: "agent", Access: "read_write"})
	if err != nil {
		t.Fatal(err)
	}
	ownerKey, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}

	// No prompts: nothing rides along with the task context.
	if tc := mustCall(t, tools, editor, "get_task_context", ContextInput{ProjectID: p.ID, Task: "배포"}).(TaskContext); tc.TaskPrompts != nil {
		t.Fatalf("empty project returned task prompts: %+v", tc.TaskPrompts)
	}

	deploy := SaveTaskPromptInput{ProjectID: p.ID, Name: "deploy-check", Title: "배포 점검", Description: "운영 배포 뒤 확인",
		Triggers: []string{"배포 점검"}, Body: "{{env}} 서버 버전과 SECRET-BODY 로그를 확인한다.", Arguments: []PromptArgument{{Name: "env", Description: "환경", Required: true}}}
	// Only the owner and admins change prompts, and only from a web session.
	for _, who := range []Identity{editor, viewer, ownerKey} {
		_, err = call(t, tools, who, "save_task_prompt", deploy)
		denied(t, err, "FORBIDDEN")
	}
	_, err = call(t, tools, outsider, "save_task_prompt", deploy)
	denied(t, err, "NOT_FOUND")
	saved := mustCall(t, tools, admin, "save_task_prompt", deploy).(TaskPrompt)
	if saved.Revision != 1 || !saved.Enabled || saved.CreatedBy != admin.User.ID || !slices.Equal(saved.Triggers, []string{"배포 점검"}) || len(saved.Arguments) != 1 || !saved.Arguments[0].Required {
		t.Fatalf("saved: %+v", saved)
	}
	_, err = call(t, tools, owner, "save_task_prompt", deploy)
	denied(t, err, "CONFLICT")
	weekly := mustCall(t, tools, owner, "save_task_prompt", SaveTaskPromptInput{ProjectID: p.ID, Name: "weekly-report", Title: "주간 보고", Triggers: []string{"주간 보고"}, Body: "이번 주 history를 요약한다."}).(TaskPrompt)
	if weekly.Arguments == nil || len(weekly.Arguments) != 0 {
		t.Fatalf("arguments should be an empty list: %+v", weekly.Arguments)
	}

	// Every member can read; bodies only on request.
	set := mustCall(t, tools, viewer, "list_task_prompts", ListTaskPromptsInput{ProjectID: p.ID}).(TaskPromptSet)
	if len(set.Prompts) != 2 || set.Prompts[0].Name != "deploy-check" || set.Prompts[0].Body != "" || set.CanManage || set.Guidance == "" {
		t.Fatalf("viewer list: %+v", set)
	}
	if byKey := mustCall(t, tools, ownerKey, "list_task_prompts", ListTaskPromptsInput{ProjectID: p.ID, IncludeBody: true}).(TaskPromptSet); byKey.CanManage || byKey.Prompts[0].Body == "" {
		t.Fatalf("owner key list: %+v", byKey)
	}
	if bySession := mustCall(t, tools, owner, "list_task_prompts", ListTaskPromptsInput{ProjectID: p.ID}).(TaskPromptSet); !bySession.CanManage {
		t.Fatal("owner web session must manage prompts")
	}
	_, err = call(t, tools, outsider, "list_task_prompts", ListTaskPromptsInput{ProjectID: p.ID})
	denied(t, err, "NOT_FOUND")

	// Matching the user's words.
	got := mustCall(t, tools, ownerKey, "get_task_prompt", GetTaskPromptInput{ProjectID: p.ID, Request: "운영 배포점검 해줘"}).(TaskPromptResult)
	if !got.Matched || got.Prompt == nil || got.Prompt.ID != saved.ID || got.Prompt.Body != "" || !strings.HasPrefix(got.Instructions, "{{env}}") || !slices.Equal(got.MissingArguments, []string{"env"}) {
		t.Fatalf("request match: %+v", got)
	}
	got = mustCall(t, tools, viewer, "get_task_prompt", GetTaskPromptInput{ProjectID: p.ID, Name: "deploy-check", Arguments: map[string]string{"env": "운영"}}).(TaskPromptResult)
	if !got.Matched || got.Instructions != "운영 서버 버전과 SECRET-BODY 로그를 확인한다." || len(got.MissingArguments) != 0 {
		t.Fatalf("by name: %+v", got)
	}
	got = mustCall(t, tools, editor, "get_task_prompt", GetTaskPromptInput{ProjectID: p.ID, Request: "배포 점검이랑 주간 보고 해줘"}).(TaskPromptResult)
	if got.Matched || len(got.Candidates) != 2 || got.Instructions != "" {
		t.Fatalf("several: %+v", got)
	}
	got = mustCall(t, tools, editor, "get_task_prompt", GetTaskPromptInput{ProjectID: p.ID, Request: "커피 내려줘"}).(TaskPromptResult)
	if got.Matched || len(got.Candidates) != 0 || got.Guidance != promptNoneGuidance {
		t.Fatalf("none: %+v", got)
	}
	for _, in := range []GetTaskPromptInput{{ProjectID: p.ID}, {ProjectID: p.ID, Name: "deploy-check", Request: "x"}} {
		_, err = call(t, tools, editor, "get_task_prompt", in)
		denied(t, err, "INVALID_INPUT")
	}
	_, err = call(t, tools, editor, "get_task_prompt", GetTaskPromptInput{ProjectID: p.ID, Name: "missing"})
	denied(t, err, "NOT_FOUND")

	// resolve_project and start_task context list the prompts without bodies.
	mustCall(t, tools, owner, "add_project_repository", ProjectRepositoryInput{ProjectID: p.ID, Repository: "owner/prompted"})
	resolved := mustCall(t, tools, editor, "resolve_project", ResolveProjectInput{Repository: "owner/prompted"}).(ResolvedProject)
	if resolved.TaskPrompts == nil || len(resolved.TaskPrompts.Prompts) != 2 || resolved.TaskPrompts.Prompts[0].Name != "deploy-check" {
		t.Fatalf("resolve prompts: %+v", resolved.TaskPrompts)
	}
	tc := mustCall(t, tools, editor, "get_task_context", ContextInput{ProjectID: p.ID, Task: "배포"}).(TaskContext)
	if tc.TaskPrompts == nil || len(tc.TaskPrompts.Prompts) != 2 || tc.TaskPrompts.Guidance == "" {
		t.Fatalf("context prompts: %+v", tc.TaskPrompts)
	}

	// Changes need the current revision; renaming onto another prompt's name conflicts.
	upd := deploy
	upd.PromptID, upd.ExpectedRevision, upd.Triggers = saved.ID, 1, []string{"배포 점검", "배포 확인"}
	changed := mustCall(t, tools, owner, "save_task_prompt", upd).(TaskPrompt)
	if changed.Revision != 2 || changed.UpdatedBy != owner.User.ID || len(changed.Triggers) != 2 {
		t.Fatalf("changed: %+v", changed)
	}
	_, err = call(t, tools, admin, "save_task_prompt", upd)
	denied(t, err, "CONFLICT")
	upd.ExpectedRevision, upd.Name = 2, "weekly-report"
	_, err = call(t, tools, admin, "save_task_prompt", upd)
	denied(t, err, "CONFLICT")

	// Switched-off prompts cannot be run and leave the context.
	for _, who := range []Identity{editor, ownerKey} {
		_, err = call(t, tools, who, "set_task_prompt_enabled", SetTaskPromptEnabledInput{ProjectID: p.ID, PromptID: saved.ID, Enabled: false})
		denied(t, err, "FORBIDDEN")
	}
	off := mustCall(t, tools, admin, "set_task_prompt_enabled", SetTaskPromptEnabledInput{ProjectID: p.ID, PromptID: saved.ID, Enabled: false}).(TaskPrompt)
	if off.Enabled || off.Revision != 3 {
		t.Fatalf("off: %+v", off)
	}
	_, err = call(t, tools, editor, "get_task_prompt", GetTaskPromptInput{ProjectID: p.ID, Name: "deploy-check"})
	denied(t, err, "NOT_FOUND")
	if got = mustCall(t, tools, editor, "get_task_prompt", GetTaskPromptInput{ProjectID: p.ID, Request: "배포 확인해줘"}).(TaskPromptResult); got.Matched {
		t.Fatalf("disabled prompt matched: %+v", got)
	}
	if tc = mustCall(t, tools, editor, "get_task_context", ContextInput{ProjectID: p.ID, Task: "배포"}).(TaskContext); len(tc.TaskPrompts.Prompts) != 1 {
		t.Fatalf("context kept disabled prompt: %+v", tc.TaskPrompts)
	}
	if all := mustCall(t, tools, editor, "list_task_prompts", ListTaskPromptsInput{ProjectID: p.ID, IncludeDisabled: true}).(TaskPromptSet); len(all.Prompts) != 2 {
		t.Fatalf("include_disabled: %+v", all.Prompts)
	}

	_, err = call(t, tools, editor, "delete_task_prompt", TaskPromptRef{ProjectID: p.ID, PromptID: weekly.ID})
	denied(t, err, "FORBIDDEN")
	mustCall(t, tools, owner, "delete_task_prompt", TaskPromptRef{ProjectID: p.ID, PromptID: weekly.ID})
	_, err = call(t, tools, owner, "delete_task_prompt", TaskPromptRef{ProjectID: p.ID, PromptID: weekly.ID})
	denied(t, err, "NOT_FOUND")
	if tc = mustCall(t, tools, editor, "get_task_context", ContextInput{ProjectID: p.ID, Task: "배포"}).(TaskContext); tc.TaskPrompts != nil {
		t.Fatalf("no enabled prompts left: %+v", tc.TaskPrompts)
	}

	// Audit keeps the prompt ID but never the body or arguments.
	var details, target string
	if err := s.DB.QueryRow(`SELECT details::text,target_id FROM audit_events WHERE action='save_task_prompt' AND outcome='success' AND target_id<>'' ORDER BY id LIMIT 1`).Scan(&details, &target); err != nil {
		t.Fatal(err)
	}
	if target != saved.ID || strings.Contains(details, "SECRET-BODY") {
		t.Fatalf("audit: %s %s", target, details)
	}
	var leaked int
	if err := s.DB.QueryRow(`SELECT count(*) FROM audit_events WHERE details::text LIKE '%SECRET-BODY%' OR details::text LIKE '%운영%'`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("audit copied prompt text or argument values in %d events", leaked)
	}
}
