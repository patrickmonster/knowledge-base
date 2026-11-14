package kb

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func templateFixture(kind string) TemplateDraft {
	d := TemplateDraft{Kind: kind, Title: "검증 문서", Sections: map[string]string{}}
	for _, template := range documentTemplates() {
		if template.Kind == kind {
			for _, section := range template.Sections {
				d.Sections[section.Key] = "확인한 내용: " + section.Label
			}
		}
	}
	return d
}
func finishFixture(projectID, taskID string) FinishTaskInput {
	return FinishTaskInput{ProjectID: projectID, TaskID: taskID, Outcome: "completed", Summary: "환불 화면 개선 완료", Changes: "환불 화면을 변경했습니다.", Decisions: "기존 환불 정책을 유지합니다.", Validation: "단위 테스트 통과. 실제 결제 검사는 미실행: 테스트 결제 연결이 없음.", Remaining: "실제 결제 환경에서 확인 필요", References: "기준 커밋 test-base, 이번 변경은 미커밋·미배포"}
}

func TestTemplateValidationAndMCPWorkflowSchemas(t *testing.T) {
	for _, template := range documentTemplates() {
		d := templateFixture(template.Kind)
		text, err := renderTemplate(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, section := range template.Sections {
			if !strings.Contains(template.Markdown, "{{"+section.Key+"}}") || !strings.Contains(text, "## "+section.Label) {
				t.Fatal("template and schema disagree")
			}
		}
		delete(d.Sections, template.Sections[0].Key)
		_, err = renderTemplate(d)
		denied(t, err, "INVALID_INPUT")
		d = templateFixture(template.Kind)
		d.Sections["unknown"] = "hidden content"
		_, err = renderTemplate(d)
		denied(t, err, "INVALID_INPUT")
	}
	// Exercise the actual MCP-generated schemas, including embedded draft fields
	// and nested map/list inputs, rather than only calling internal Go functions.
	ctx := context.Background()
	server := NewTools(&Store{}).MCP(Identity{})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "workflow-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range list.Tools {
		found[tool.Name] = true
		if tool.Name == "create_templated_document" {
			raw, _ := json.Marshal(tool.InputSchema)
			for _, key := range []string{"project_id", "kind", "title", "sections"} {
				if !strings.Contains(string(raw), `"`+key+`"`) {
					t.Fatalf("missing schema field %s: %s", key, raw)
				}
			}
		}
	}
	for _, name := range []string{"get_document_templates", "create_templated_document", "start_task", "finish_task", "get_task_run", "list_task_runs"} {
		if !found[name] {
			t.Fatal("missing MCP tool", name)
		}
	}
}

func TestTaskWorkflowLifecycle(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "workflow", Name: "workflow"}).(Project)
	source := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 정책", Kind: "decision", Content: "환불 기한은 7일입니다."}).(Document)
	start := StartTaskInput{ProjectID: p.ID, RequestKey: "refund-page-1", Title: "환불 화면 개선", Task: "환불 정책 확인과 화면 개선"}
	run := mustCall(t, tools, admin, "start_task", start).(StartTaskResult).Run
	if run.Status != "open" || len(run.Context.Sources) != 1 || run.Context.Sources[0].DocumentID != source.ID {
		t.Fatalf("missing starting context: %+v", run)
	}
	newContent := "환불 기한은 14일입니다."
	mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: source.ID, ExpectedRevision: 1, Reason: "정책 변경", Patch: DocumentPatch{Content: &newContent}})
	retried := mustCall(t, tools, admin, "start_task", start).(StartTaskResult).Run
	if retried.ID != run.ID || retried.Context.Sources[0].Revision != 1 || !strings.Contains(retried.Context.Sources[0].Excerpt, "7일") {
		t.Fatal("retry duplicated or rewrote start snapshot")
	}
	changedStart := start
	changedStart.Task = "다른 작업"
	_, err := call(t, tools, admin, "start_task", changedStart)
	denied(t, err, "CONFLICT")
	draft := mustCall(t, tools, admin, "create_templated_document", TemplateDocumentInput{ProjectID: p.ID, TemplateDraft: templateFixture("brief")}).(Document)
	if draft.Status != "draft" {
		t.Fatal("tentative plan was activated automatically")
	}
	finish := finishFixture(p.ID, run.ID)
	finish.Documents = []TemplateDraft{templateFixture("decision")}
	completion := mustCall(t, tools, admin, "finish_task", finish).(TaskCompletion)
	if len(completion.Documents) != 2 || completion.Documents[0].Kind != "history" || completion.Documents[1].Status != "draft" {
		t.Fatalf("unexpected completion: %+v", completion)
	}
	history := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: completion.Documents[0].ID}).(Document)
	if !strings.Contains(history.Content, run.ID) || !strings.Contains(history.Content, source.ID+"?revision=1") || !strings.Contains(history.Content, finish.Validation) {
		t.Fatal("missing history provenance or actual verification")
	}
	var queued int
	if err = s.DB.QueryRow("SELECT count(*) FROM chunks WHERE document_id=$1 AND embedding_status='pending'", history.ID).Scan(&queued); err != nil || queued == 0 {
		t.Fatalf("history not queued for embedding: %d %v", queued, err)
	}
	again := mustCall(t, tools, admin, "finish_task", finish).(TaskCompletion)
	if again.Documents[0].ID != history.ID {
		t.Fatal("replayed finish duplicated history")
	}
	finish.Summary = "changed payload"
	_, err = call(t, tools, admin, "finish_task", finish)
	denied(t, err, "CONFLICT")
	closed := mustCall(t, tools, admin, "get_task_run", TaskRef{ProjectID: p.ID, TaskID: run.ID}).(TaskRun)
	if closed.Status != "completed" || closed.CompletedAt == nil || closed.Completion.Documents[0].ID != history.ID {
		t.Fatal("completed task not recoverable")
	}
	list := mustCall(t, tools, admin, "list_task_runs", TaskListInput{ProjectID: p.ID, Status: "completed"}).([]map[string]any)
	if len(list) != 1 || list[0]["context"] != nil {
		t.Fatal("task listing leaks full context or omitted task")
	}
	var details string
	if err = s.DB.QueryRow("SELECT details::text FROM audit_events WHERE action='finish_task' AND outcome='success' LIMIT 1").Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, finish.Validation) || !strings.Contains(details, run.ID) {
		t.Fatal("audit includes body or misses task ID")
	}
}

func TestTaskWorkflowAtomicityAndConcurrency(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "atomic-workflow", Name: "atomic"}).(Project)
	start := StartTaskInput{ProjectID: p.ID, RequestKey: "same-request", Title: "동시 작업", Task: "동시 작업"}
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := call(t, tools, admin, "start_task", start)
			if err != nil {
				errs <- err
				return
			}
			ids <- out.(StartTaskResult).Run.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	runID := ""
	for id := range ids {
		if runID != "" && runID != id {
			t.Fatal("concurrent starts duplicated task")
		}
		runID = id
	}
	finish := finishFixture(p.ID, runID)
	bad := templateFixture("decision")
	delete(bad.Sections, "rationale")
	finish.Documents = []TemplateDraft{templateFixture("brief"), bad}
	_, err := call(t, tools, admin, "finish_task", finish)
	denied(t, err, "INVALID_INPUT")
	var docs int
	if err = s.DB.QueryRow("SELECT count(*) FROM documents WHERE project_id=$1", p.ID).Scan(&docs); err != nil || docs != 0 {
		t.Fatalf("partial completion leaked documents: %d %v", docs, err)
	}
	run := mustCall(t, tools, admin, "get_task_run", TaskRef{ProjectID: p.ID, TaskID: runID}).(TaskRun)
	if run.Status != "open" {
		t.Fatal("failed completion closed task")
	}
	finish.Documents = nil
	if _, err = s.DB.Exec("ALTER TABLE audit_events RENAME TO offline_audit"); err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, admin, "finish_task", finish)
	if err == nil {
		t.Fatal("completion succeeded without audit")
	}
	if _, err = s.DB.Exec("ALTER TABLE offline_audit RENAME TO audit_events"); err != nil {
		t.Fatal(err)
	}
	run = mustCall(t, tools, admin, "get_task_run", TaskRef{ProjectID: p.ID, TaskID: runID}).(TaskRun)
	if run.Status != "open" {
		t.Fatal("audit failure failed to roll back task")
	}
	results := make(chan TaskCompletion, 2)
	errs = make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := call(t, tools, admin, "finish_task", finish)
			if err != nil {
				errs <- err
				return
			}
			results <- out.(TaskCompletion)
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	docID := ""
	for result := range results {
		if docID != "" && docID != result.Documents[0].ID {
			t.Fatal("concurrent finish duplicated results")
		}
		docID = result.Documents[0].ID
	}
	if err = s.DB.QueryRow("SELECT count(*) FROM documents WHERE project_id=$1", p.ID).Scan(&docs); err != nil || docs != 1 {
		t.Fatalf("completion not exactly once: %d %v", docs, err)
	}
}

func TestTaskWorkflowPermissionsAndFallback(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	writer := inviteAs(t, s, admin, "task-writer@example.com")
	viewer := inviteAs(t, s, admin, "task-viewer@example.com")
	p := mustCall(t, tools, writer, "create_project", CreateProjectInput{Slug: "private-task", Name: "private task"}).(Project)
	mustCall(t, tools, writer, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	start := StartTaskInput{ProjectID: p.ID, RequestKey: "fallback", Title: "환불 작업", Task: "환불 작업"}
	s.Embedder = &testEmbedder{model: "offline", fn: func(context.Context, []string) ([][]float32, error) {
		return nil, problem("EMBEDDING_UNAVAILABLE", "offline")
	}}
	_, err := call(t, tools, viewer, "start_task", start)
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, admin, "start_task", start)
	denied(t, err, "NOT_FOUND")
	run := mustCall(t, tools, writer, "start_task", start).(StartTaskResult).Run
	if run.Context.Retrieval != "lexical-fallback" || run.Context.Warning == "" {
		t.Fatal("fallback was hidden from task context")
	}
	_, err = call(t, tools, admin, "get_task_run", TaskRef{ProjectID: p.ID, TaskID: run.ID})
	denied(t, err, "NOT_FOUND")
	mustCall(t, tools, viewer, "get_task_run", TaskRef{ProjectID: p.ID, TaskID: run.ID})
	finish := finishFixture(p.ID, run.ID)
	_, err = call(t, tools, viewer, "finish_task", finish)
	denied(t, err, "FORBIDDEN")
	mustCall(t, tools, writer, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "editor"})
	_, err = call(t, tools, viewer, "finish_task", finish)
	denied(t, err, "FORBIDDEN")
	keyOut, err := s.CreateKey(ctx, writer, KeyInput{Name: "read only", Access: "read_only", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "finish_task", finish)
	denied(t, err, "FORBIDDEN")
	other := mustCall(t, tools, writer, "create_project", CreateProjectInput{Slug: "other-task", Name: "other task"}).(Project)
	_, err = call(t, tools, key, "list_task_runs", TaskListInput{ProjectID: other.ID})
	denied(t, err, "FORBIDDEN")
	finish.ProjectID = other.ID
	_, err = call(t, tools, writer, "finish_task", finish)
	denied(t, err, "NOT_FOUND")
	finish.ProjectID = p.ID
	finish.Outcome = "partial"
	out := mustCall(t, tools, writer, "finish_task", finish).(TaskCompletion)
	if out.Outcome != "partial" {
		t.Fatal("partial outcome was relabeled as success")
	}
}

func TestWorkflowThroughMCP(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	actor := loginAs(t, s, "admin@example.com")
	tools := NewTools(s)
	p := mustCall(t, tools, actor, "create_project", CreateProjectInput{Slug: "mcp-workflow", Name: "MCP workflow"}).(Project)
	server := tools.MCP(actor)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "workflow-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	invoke := func(name string, args any, out any) {
		t.Helper()
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("%s: %+v", name, result.Content)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
	var started StartTaskResult
	invoke("start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "mcp-roundtrip", Title: "도구 검증", Task: "MCP 작업 흐름"}, &started)
	var planned Document
	invoke("create_templated_document", TemplateDocumentInput{ProjectID: p.ID, TemplateDraft: templateFixture("brief")}, &planned)
	if planned.Status != "draft" || !strings.Contains(planned.Content, "## 완료 기준") {
		t.Fatal("template fields lost through MCP schema")
	}
	finish := finishFixture(p.ID, started.Run.ID)
	finish.Tags = []string{"knowledge-base", "검증"}
	finish.Documents = []TemplateDraft{templateFixture("decision")}
	var completed TaskCompletion
	invoke("finish_task", finish, &completed)
	if len(completed.Documents) != 2 {
		t.Fatal("nested documents lost through MCP")
	}
	history := mustCall(t, tools, actor, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: completed.Documents[0].ID}).(Document)
	if !strings.Contains(strings.Join(history.Tags, ","), "knowledge-base") {
		t.Fatal("history tags lost")
	}
}
