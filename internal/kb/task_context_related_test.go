package kb

import (
	"context"
	"strings"
	"testing"
)

func TestTaskContextRelatedProjects(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "related-member@example.com")
	main := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "related-main", Name: "주 프로젝트"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "related-other", Name: "관련 프로젝트"}).(Project)
	hidden := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "related-hidden", Name: "비공개"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: main.ID, UserID: member.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: other.ID, UserID: member.User.ID, Role: "viewer"})
	own := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: main.ID, Title: "결제 재시도 정책", Kind: "reference", Content: "결제 재시도는 세 번까지 합니다."}).(Document)
	rel := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: other.ID, Title: "결제 재시도 구현", Kind: "reference", Content: "결제 재시도 간격은 지수 백오프입니다."}).(Document)
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: hidden.ID, Title: "결제 재시도 비밀", Kind: "reference", Content: "결제 재시도 비공개 메모"})

	// Without related_project_ids the context stays single-project.
	plain := mustCall(t, tools, member, "get_task_context", ContextInput{ProjectID: main.ID, Task: "결제 재시도"}).(TaskContext)
	if len(plain.Sources) != 1 || plain.Sources[0].DocumentID != own.ID || plain.RelatedSources != nil || plain.RelatedProjects != nil {
		t.Fatalf("default context crossed projects: %+v", plain)
	}

	// Opt-in: related hits come apart from sources and name their project.
	// The main project and duplicates in the list are ignored.
	tc := mustCall(t, tools, member, "get_task_context", ContextInput{ProjectID: main.ID, Task: "결제 재시도", RelatedProjectIDs: []string{other.ID, main.ID, other.ID}}).(TaskContext)
	if len(tc.Sources) != 1 || tc.Sources[0].DocumentID != own.ID || tc.Sources[0].ProjectSlug != "" {
		t.Fatalf("main sources changed by related projects: %+v", tc.Sources)
	}
	if len(tc.RelatedProjects) != 1 || tc.RelatedProjects[0].ID != other.ID || len(tc.RelatedSources) != 1 {
		t.Fatalf("related context: %+v / %+v", tc.RelatedProjects, tc.RelatedSources)
	}
	if h := tc.RelatedSources[0]; h.DocumentID != rel.ID || h.ProjectID != other.ID || h.ProjectSlug != "related-other" || h.ProjectName != "관련 프로젝트" {
		t.Fatalf("related source not labeled: %+v", h)
	}
	if !strings.Contains(tc.Guidance, "related_sources") {
		t.Fatalf("guidance does not explain related sources: %q", tc.Guidance)
	}

	// Projects the caller cannot read fail the whole call, as do bad inputs.
	_, err := call(t, tools, member, "get_task_context", ContextInput{ProjectID: main.ID, Task: "결제 재시도", RelatedProjectIDs: []string{other.ID, hidden.ID}})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, member, "get_task_context", ContextInput{ProjectID: main.ID, Task: "결제 재시도", RelatedProjectIDs: []string{"not-a-uuid"}})
	denied(t, err, "INVALID_INPUT")
	many := []string{}
	for range maxRelatedProjects + 1 {
		many = append(many, mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "related-many-" + string(rune('a'+len(many))), Name: "많음"}).(Project).ID)
	}
	_, err = call(t, tools, admin, "get_task_context", ContextInput{ProjectID: main.ID, Task: "결제 재시도", RelatedProjectIDs: many})
	denied(t, err, "INVALID_INPUT")

	// A project-scoped key never reaches another project.
	keyOut, err := s.CreateKey(ctx, member, KeyInput{Name: "related-scoped", Access: "read_only", ProjectID: main.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "get_task_context", ContextInput{ProjectID: main.ID, Task: "결제 재시도", RelatedProjectIDs: []string{other.ID}})
	denied(t, err, "FORBIDDEN")

	// Related excerpts share excerpt_budget: a quarter is reserved for them.
	long := strings.Repeat("결제 재시도 기록 ", 300)
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: main.ID, Title: "결제 재시도 긴 기록", Kind: "reference", Content: long})
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: other.ID, Title: "결제 재시도 긴 메모", Kind: "reference", Content: long})
	budgeted := mustCall(t, tools, member, "get_task_context", ContextInput{ProjectID: main.ID, Task: "결제 재시도", ExcerptBudget: 1000, RelatedProjectIDs: []string{other.ID}}).(TaskContext)
	mainChars, relatedChars := 0, 0
	for _, h := range budgeted.Sources {
		mainChars += len([]rune(h.Excerpt))
	}
	for _, h := range budgeted.RelatedSources {
		relatedChars += len([]rune(h.Excerpt))
	}
	if mainChars > 750 || relatedChars > 250 || relatedChars == 0 || !budgeted.Truncated {
		t.Fatalf("budget split main=%d related=%d truncated=%v", mainChars, relatedChars, budgeted.Truncated)
	}

	// start_task stores related context and treats other related projects as a changed input.
	start := StartTaskInput{ProjectID: main.ID, RequestKey: "related-start", Title: "결제 재시도", Task: "결제 재시도", RelatedProjectIDs: []string{other.ID}}
	run := mustCall(t, tools, member, "start_task", start).(StartTaskResult).Run
	if len(run.Context.RelatedSources) == 0 || run.Context.RelatedSources[0].ProjectSlug != "related-other" {
		t.Fatalf("start_task related context: %+v", run.Context.RelatedSources)
	}
	stored := mustCall(t, tools, member, "get_task_run", TaskRef{ProjectID: main.ID, TaskID: run.ID}).(TaskRun)
	if len(stored.Context.RelatedProjects) != 1 || len(stored.Context.RelatedSources) != len(run.Context.RelatedSources) {
		t.Fatalf("related context not stored: %+v", stored.Context.RelatedProjects)
	}
	retried := mustCall(t, tools, member, "start_task", start).(StartTaskResult).Run
	if retried.ID != run.ID {
		t.Fatal("identical retry created another task")
	}
	changed := start
	changed.RelatedProjectIDs = nil
	_, err = call(t, tools, member, "start_task", changed)
	denied(t, err, "CONFLICT")
}
