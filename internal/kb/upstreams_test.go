package kb

import (
	"context"
	"strings"
	"testing"
)

func TestProjectUpstreams(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	designer := inviteAs(t, s, admin, "upstream-designer@example.com")
	dev := inviteAs(t, s, admin, "upstream-dev@example.com")
	outsider := inviteAs(t, s, admin, "upstream-outsider@example.com")

	design := mustCall(t, tools, designer, "create_project", CreateProjectInput{Slug: "up-design", Name: "디자인 시안"}).(Project)
	app := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "up-app", Name: "서비스 앱"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: app.ID, UserID: dev.User.ID, Role: "editor"})
	spec := mustCall(t, tools, designer, "create_document", DocumentInput{ProjectID: design.ID, Title: "결제 버튼 시안", Kind: "reference", Content: "결제 버튼은 기본 색상 토큰 primary-500을 쓰고 높이 48px입니다."}).(Document)
	mustCall(t, tools, designer, "create_document", DocumentInput{ProjectID: design.ID, Title: "결제 버튼 실험안", Kind: "brief", Status: "draft", Content: "결제 버튼 초안: 높이 56px 실험."})
	own := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: app.ID, Title: "결제 화면 구현", Kind: "reference", Content: "결제 버튼 구현은 Button 컴포넌트를 씁니다."}).(Document)

	// Only the downstream's owner or admin may ask, and not for itself.
	_, err := call(t, tools, dev, "request_project_upstream", UpstreamRequestInput{ProjectID: app.ID, UpstreamProjectID: design.ID})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, admin, "request_project_upstream", UpstreamRequestInput{ProjectID: app.ID, UpstreamProjectID: app.ID})
	denied(t, err, "INVALID_INPUT")
	link := mustCall(t, tools, admin, "request_project_upstream", UpstreamRequestInput{ProjectID: app.ID, UpstreamProjectID: design.ID}).(ProjectUpstream)
	if link.Status != "pending" || link.UpstreamProjectSlug != "up-design" || link.ApprovedAt != nil {
		t.Fatalf("request should stay pending: %+v", link)
	}

	// Pending grants nothing.
	_, err = call(t, tools, dev, "get_document", DocumentRef{ProjectID: design.ID, DocumentID: spec.ID})
	denied(t, err, "NOT_FOUND")
	tc := mustCall(t, tools, dev, "get_task_context", ContextInput{ProjectID: app.ID, Task: "결제 버튼"}).(TaskContext)
	if tc.UpstreamSources != nil || tc.UpstreamProjects != nil {
		t.Fatalf("pending upstream leaked into context: %+v", tc.UpstreamSources)
	}

	// Approval is the upstream manager's, in the web console only.
	_, err = call(t, tools, admin, "approve_project_upstream", UpstreamApproveInput{ProjectID: design.ID, DownstreamProjectID: app.ID})
	denied(t, err, "NOT_FOUND")
	keyOut, err := s.CreateKey(ctx, designer, KeyInput{Name: "designer-key", Access: "read_write"})
	if err != nil {
		t.Fatal(err)
	}
	designerKey, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, designerKey, "approve_project_upstream", UpstreamApproveInput{ProjectID: design.ID, DownstreamProjectID: app.ID})
	denied(t, err, "FORBIDDEN")
	link = mustCall(t, tools, designer, "approve_project_upstream", UpstreamApproveInput{ProjectID: design.ID, DownstreamProjectID: app.ID}).(ProjectUpstream)
	if link.Status != "approved" || link.ApprovedBy == nil || *link.ApprovedBy != designer.User.ID {
		t.Fatalf("approval not recorded: %+v", link)
	}

	// Two projects cannot follow each other.
	mustCall(t, tools, designer, "set_project_member", MemberInput{ProjectID: design.ID, UserID: admin.User.ID, Role: "admin"})
	_, err = call(t, tools, designer, "request_project_upstream", UpstreamRequestInput{ProjectID: design.ID, UpstreamProjectID: app.ID})
	denied(t, err, "CONFLICT")
	mustCall(t, tools, designer, "set_project_member", MemberInput{ProjectID: design.ID, UserID: admin.User.ID, Role: "remove"})

	// Downstream members read the upstream without joining it ...
	d := mustCall(t, tools, dev, "get_document", DocumentRef{ProjectID: design.ID, DocumentID: spec.ID}).(Document)
	if d.ID != spec.ID {
		t.Fatalf("upstream document: %+v", d)
	}
	mustCall(t, tools, dev, "list_documents", ListInput{ProjectID: design.ID})
	mustCall(t, tools, dev, "search_knowledge", SearchInput{ProjectID: design.ID, Query: "결제 버튼", Mode: "lexical"})
	// ... but cannot change it.
	newContent := "높이 40px"
	_, err = call(t, tools, dev, "update_document", UpdateInput{ProjectID: design.ID, DocumentID: spec.ID, ExpectedRevision: d.Revision, Reason: "앱에서 수정", Patch: DocumentPatch{Content: &newContent}})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, dev, "create_document", DocumentInput{ProjectID: design.ID, Title: "앱이 만든 시안", Kind: "reference", Content: "x"})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, dev, "start_task", StartTaskInput{ProjectID: design.ID, RequestKey: "up-1", Title: "x", Task: "x"})
	denied(t, err, "NOT_FOUND")
	// People outside the downstream still see nothing.
	_, err = call(t, tools, outsider, "get_document", DocumentRef{ProjectID: design.ID, DocumentID: spec.ID})
	denied(t, err, "NOT_FOUND")

	// start_task in the downstream brings the upstream's active knowledge along,
	// apart from the project's own sources and without its drafts.
	tc = mustCall(t, tools, dev, "get_task_context", ContextInput{ProjectID: app.ID, Task: "결제 버튼"}).(TaskContext)
	if len(tc.Sources) != 1 || tc.Sources[0].DocumentID != own.ID {
		t.Fatalf("own sources: %+v", tc.Sources)
	}
	if len(tc.UpstreamProjects) != 1 || tc.UpstreamProjects[0].ID != design.ID || len(tc.UpstreamSources) != 1 {
		t.Fatalf("upstream context: %+v / %+v", tc.UpstreamProjects, tc.UpstreamSources)
	}
	if h := tc.UpstreamSources[0]; h.DocumentID != spec.ID || h.ProjectSlug != "up-design" || h.Status != "active" {
		t.Fatalf("upstream source: %+v", h)
	}
	if !strings.Contains(tc.Guidance, "from_project_id") {
		t.Fatalf("guidance does not explain how to report upstream problems: %q", tc.Guidance)
	}
	// Naming the upstream in related_project_ids does not repeat it (and needs no membership).
	tc = mustCall(t, tools, dev, "get_task_context", ContextInput{ProjectID: app.ID, Task: "결제 버튼", RelatedProjectIDs: []string{design.ID}}).(TaskContext)
	if tc.RelatedSources != nil || len(tc.UpstreamSources) != 1 {
		t.Fatalf("upstream repeated as related: %+v", tc.RelatedSources)
	}
	// A key scoped to the downstream gets the upstream through start_task only.
	scopedOut, err := s.CreateKey(ctx, dev, KeyInput{Name: "app-key", Access: "read_write", ProjectID: app.ID})
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := s.APIKey(ctx, scopedOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if tc := mustCall(t, tools, scoped, "get_task_context", ContextInput{ProjectID: app.ID, Task: "결제 버튼"}).(TaskContext); len(tc.UpstreamSources) != 1 {
		t.Fatalf("scoped key context: %+v", tc.UpstreamSources)
	}
	_, err = call(t, tools, scoped, "get_document", DocumentRef{ProjectID: design.ID, DocumentID: spec.ID})
	denied(t, err, "FORBIDDEN")

	// Problems go upstream as issues that name the downstream.
	_, err = call(t, tools, dev, "create_issue", CreateIssueInput{ProjectID: design.ID, Kind: "issue", Title: "x", Body: "x", TodoID: spec.ID})
	denied(t, err, "INVALID_INPUT")
	_, err = call(t, tools, dev, "create_issue", CreateIssueInput{ProjectID: design.ID, Kind: "issue", Title: "x", Body: "x", FromProjectID: design.ID})
	denied(t, err, "INVALID_INPUT")
	_, err = call(t, tools, outsider, "create_issue", CreateIssueInput{ProjectID: design.ID, Kind: "issue", Title: "x", Body: "x"})
	denied(t, err, "NOT_FOUND")
	issue := mustCall(t, tools, dev, "create_issue", CreateIssueInput{ProjectID: design.ID, Kind: "issue", Title: "결제 버튼 대비 부족", Body: "다크 모드에서 대비가 3:1 미만입니다.", DocumentID: spec.ID}).(Issue)
	if issue.OriginProjectID == nil || *issue.OriginProjectID != app.ID || issue.OriginProjectName == nil || *issue.OriginProjectName != "서비스 앱" {
		t.Fatalf("issue origin: %+v", issue)
	}
	mustCall(t, tools, designer, "create_issue", CreateIssueInput{ProjectID: design.ID, Title: "디자인 내부 질문", Body: "x"})
	fromApp := mustCall(t, tools, designer, "list_issues", IssueListInput{ProjectID: design.ID, OriginProjectID: app.ID}).([]Issue)
	if len(fromApp) != 1 || fromApp[0].ID != issue.ID {
		t.Fatalf("origin filter: %+v", fromApp)
	}
	// The reporter follows up on their own issue, but cannot answer or close it.
	mustCall(t, tools, dev, "comment_issue", CommentIssueInput{ProjectID: design.ID, IssueID: issue.ID, Body: "스크린샷 첨부 예정"})
	_, err = call(t, tools, dev, "update_issue", UpdateIssueInput{ProjectID: design.ID, IssueID: issue.ID, Status: "resolved"})
	denied(t, err, "NOT_FOUND")

	// The stored start snapshot keeps the upstream sources while the link holds.
	upRun := mustCall(t, tools, dev, "start_task", StartTaskInput{ProjectID: app.ID, RequestKey: "up-snapshot", Title: "결제 버튼", Task: "결제 버튼"}).(StartTaskResult).Run
	if got := mustCall(t, tools, dev, "get_task_run", TaskRef{ProjectID: app.ID, TaskID: upRun.ID}).(TaskRun).Context; len(got.UpstreamSources) != 1 || got.UpstreamSources[0].ProjectName != design.Name || got.UpstreamHidden != 0 {
		t.Fatalf("stored upstream snapshot: %+v", got.UpstreamSources)
	}

	// Both sides see the link; the reader does not see other downstreams.
	ups := mustCall(t, tools, dev, "list_project_upstreams", ProjectInput{ProjectID: app.ID}).(ProjectUpstreams)
	if len(ups.Upstreams) != 1 || ups.Upstreams[0].UpstreamProjectID != design.ID {
		t.Fatalf("downstream view: %+v", ups)
	}
	downs := mustCall(t, tools, designer, "list_project_upstreams", ProjectInput{ProjectID: design.ID}).(ProjectUpstreams)
	if len(downs.Downstreams) != 1 || downs.Downstreams[0].ProjectID != app.ID {
		t.Fatalf("upstream view: %+v", downs)
	}

	// Removing the link takes read access away at once.
	mustCall(t, tools, designer, "remove_project_upstream", UpstreamRemoveInput{ProjectID: design.ID, OtherProjectID: app.ID})
	_, err = call(t, tools, dev, "get_document", DocumentRef{ProjectID: design.ID, DocumentID: spec.ID})
	denied(t, err, "NOT_FOUND")
	if tc := mustCall(t, tools, dev, "get_task_context", ContextInput{ProjectID: app.ID, Task: "결제 버튼"}).(TaskContext); tc.UpstreamSources != nil {
		t.Fatalf("removed upstream still in context")
	}
	// ...and from the stored start snapshot, which only counts what it left out.
	if got := mustCall(t, tools, dev, "get_task_run", TaskRef{ProjectID: app.ID, TaskID: upRun.ID}).(TaskRun).Context; got.UpstreamSources != nil || got.UpstreamProjects != nil || got.UpstreamHidden != 1 {
		t.Fatalf("removed upstream still in stored snapshot: %+v / hidden %d", got.UpstreamSources, got.UpstreamHidden)
	}
	_, err = call(t, tools, designer, "remove_project_upstream", UpstreamRemoveInput{ProjectID: design.ID, OtherProjectID: app.ID})
	denied(t, err, "NOT_FOUND")

	// A person who manages both sides links them in one step from the web.
	mustCall(t, tools, designer, "set_project_member", MemberInput{ProjectID: design.ID, UserID: admin.User.ID, Role: "admin"})
	if l := mustCall(t, tools, admin, "request_project_upstream", UpstreamRequestInput{ProjectID: app.ID, UpstreamProjectID: design.ID}).(ProjectUpstream); l.Status != "approved" {
		t.Fatalf("self-managed link should be approved: %+v", l)
	}
}
