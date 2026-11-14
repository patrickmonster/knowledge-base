package kb

import (
	"reflect"
	"strings"
	"testing"
)

func TestIssueTerms(t *testing.T) {
	got := issueTerms("인증 모듈을 수정하는 작업, Deploy 스크립트에서 100% 처리_로직 a")
	// Particles are trimmed, stopwords (작업, 처리) and one-letter words dropped,
	// and LIKE wildcards never survive because only letters and digits are kept.
	want := []string{"인증", "모듈", "수정하", "deploy", "스크립트", "100", "로직"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issueTerms = %#v, want %#v", got, want)
	}
	if n := len(issueTerms(strings.Repeat("단어가 ", 5) + strings.Repeat("x", 1))); n != 1 {
		t.Fatalf("duplicate terms were not removed: %d", n)
	}
	many := ""
	for i := 0; i < 30; i++ {
		many += " 단어" + string(rune('가'+i))
	}
	if n := len(issueTerms(many)); n != 20 {
		t.Fatalf("terms are not capped at 20: %d", n)
	}
}

func TestIssuesAndQuestions(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "area-owner@example.com")
	viewer := inviteAs(t, s, admin, "issue-viewer@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "issues", Name: "issues"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: owner.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})

	// Viewers cannot own an area; editors can.
	_, err := call(t, tools, admin, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "인증", OwnerID: viewer.User.ID})
	denied(t, err, "INVALID_INPUT")
	area := mustCall(t, tools, admin, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "인증", Description: "로그인과 세션", OwnerID: owner.User.ID}).(WorkArea)
	_, err = call(t, tools, admin, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "인증", OwnerID: owner.User.ID})
	denied(t, err, "CONFLICT")
	_, err = call(t, tools, viewer, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "배포", OwnerID: owner.User.ID})
	denied(t, err, "FORBIDDEN")

	// A question in the area is assigned to its owner automatically and is idempotent by request_key.
	in := CreateIssueInput{ProjectID: p.ID, Title: "세션 만료 시간 변경 가능?", Body: "공통 인증의 세션 만료를 12시간으로 줄여도 되나요?", AreaID: area.ID, Blocking: true, RequestKey: "q-1"}
	q := mustCall(t, tools, admin, "create_issue", in).(Issue)
	if q.Kind != "question" || q.Status != "open" || q.AssigneeID == nil || *q.AssigneeID != owner.User.ID || q.AreaName == nil || *q.AreaName != "인증" {
		t.Fatalf("unexpected question: %+v", q)
	}
	again := mustCall(t, tools, admin, "create_issue", in).(Issue)
	if again.ID != q.ID {
		t.Fatal("request_key retry created a second issue")
	}
	changed := in
	changed.Body = "다른 내용"
	_, err = call(t, tools, admin, "create_issue", changed)
	denied(t, err, "CONFLICT")
	_, err = call(t, tools, viewer, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "x", Body: "y"})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, admin, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "x", Body: "y", AssigneeID: viewer.User.ID})
	denied(t, err, "INVALID_INPUT")

	// start_task surfaces the question for the asker, the owner, and shared-area matches.
	start := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "t-1", Title: "세션 정리", Task: "인증 세션 만료 정책 변경"}).(StartTaskResult)
	issues := start.Run.Context.Issues
	if issues == nil || len(issues.YourQuestions) != 1 || issues.YourQuestions[0].ID != q.ID || issues.BlockingOpen != 1 {
		t.Fatalf("asker context: %+v", issues)
	}
	if len(issues.SharedAreas) != 1 || issues.SharedAreas[0].OwnerIsYou || issues.SharedAreas[0].OwnerID != owner.User.ID {
		t.Fatalf("shared areas: %+v", issues.SharedAreas)
	}
	ownerStart := mustCall(t, tools, owner, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "t-2", Title: "배포", Task: "배포 스크립트 정리"}).(StartTaskResult)
	if got := ownerStart.Run.Context.Issues.AssignedToYou; len(got) != 1 || got[0].ID != q.ID {
		t.Fatalf("owner context: %+v", got)
	}

	// Filtering and search.
	list := mustCall(t, tools, viewer, "list_issues", IssueListInput{ProjectID: p.ID, Query: "세션 만료"}).([]Issue)
	if len(list) != 1 {
		t.Fatalf("query list: %d", len(list))
	}
	list = mustCall(t, tools, viewer, "list_issues", IssueListInput{ProjectID: p.ID, Query: "세션 없는단어"}).([]Issue)
	if len(list) != 0 {
		t.Fatalf("all query terms must match: %d", len(list))
	}
	_, err = call(t, tools, viewer, "list_issues", IssueListInput{ProjectID: p.ID, Status: "pending"})
	denied(t, err, "INVALID_INPUT")

	// Answer, re-answer keeps the old answer as a comment, then resolve and publish.
	_, err = call(t, tools, viewer, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: q.ID, Answer: "no"})
	denied(t, err, "FORBIDDEN")
	mustCall(t, tools, owner, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: q.ID, Answer: "8시간까지만 가능"})
	answered := mustCall(t, tools, owner, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: q.ID, Answer: "12시간 가능, 단 관리자 세션은 제외"}).(Issue)
	if answered.Status != "answered" || answered.AnsweredBy == nil || *answered.AnsweredBy != owner.User.ID || answered.CommentCount != 1 {
		t.Fatalf("answered: %+v", answered)
	}
	mustCall(t, tools, admin, "comment_issue", CommentIssueInput{ProjectID: p.ID, IssueID: q.ID, Body: "확인했습니다."})
	full := mustCall(t, tools, viewer, "get_issue", IssueRef{ProjectID: p.ID, IssueID: q.ID}).(Issue)
	if len(full.Comments) != 2 || !strings.Contains(full.Comments[0].Body, "8시간") {
		t.Fatalf("comments: %+v", full.Comments)
	}
	_, err = call(t, tools, admin, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: q.ID, Status: "answered"})
	denied(t, err, "INVALID_INPUT")

	draft := mustCall(t, tools, admin, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: q.ID, PublishAnswer: true}).(Issue)
	if draft.KnowledgeDocumentID == nil {
		t.Fatal("publish did not link a document")
	}
	doc := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: *draft.KnowledgeDocumentID}).(Document)
	if doc.Status != "draft" || doc.Kind != "reference" || !strings.Contains(doc.Content, "관리자 세션은 제외") {
		t.Fatalf("unresolved answer must publish as draft reference: %+v", doc)
	}
	note := "정책 반영"
	resolved := mustCall(t, tools, admin, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: q.ID, Status: "resolved", Resolution: &note, PublishAnswer: true}).(Issue)
	if resolved.Status != "resolved" || *resolved.KnowledgeDocumentID != doc.ID {
		t.Fatalf("resolved: %+v", resolved)
	}
	doc = mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: doc.ID}).(Document)
	if doc.Status != "active" || doc.Revision != 2 || !strings.Contains(doc.Content, "정책 반영") {
		t.Fatalf("resolved answer must update the same document as active: %+v", doc)
	}

	// Resolved items leave the active list and start context; answering them needs a reopen.
	if list := mustCall(t, tools, admin, "list_issues", IssueListInput{ProjectID: p.ID}).([]Issue); len(list) != 0 {
		t.Fatalf("active list still has resolved items: %d", len(list))
	}
	_, err = call(t, tools, owner, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: q.ID, Answer: "추가"})
	denied(t, err, "CONFLICT")
	mustCall(t, tools, admin, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: q.ID, Status: "open"})
	_, err = call(t, tools, admin, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: q.ID, Status: "open"})
	denied(t, err, "CONFLICT")

	// Only the project owner or the area owner may change an area.
	editor := inviteAs(t, s, admin, "issue-editor@example.com")
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	_, err = call(t, tools, editor, "set_work_area", WorkAreaInput{ProjectID: p.ID, AreaID: area.ID, Name: "인증", OwnerID: editor.User.ID})
	denied(t, err, "FORBIDDEN")
	moved := mustCall(t, tools, owner, "set_work_area", WorkAreaInput{ProjectID: p.ID, AreaID: area.ID, Name: "인증·세션", OwnerID: editor.User.ID}).(WorkArea)
	if moved.OwnerID != editor.User.ID || moved.OpenIssues != 1 {
		t.Fatalf("moved area: %+v", moved)
	}
}
