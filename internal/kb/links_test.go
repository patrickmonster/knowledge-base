package kb

import (
	"context"
	"strings"
	"testing"
)

func TestDocumentLinks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	viewer := inviteAs(t, s, admin, "links-viewer@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "links", Name: "links"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "links-other", Name: "other"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	doc := func(project, kind, status, title, content string) Document {
		return mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: project, Title: title, Kind: kind, Status: status, Content: content}).(Document)
	}
	oldDecision := doc(p.ID, "decision", "active", "환불 기한 7일", "환불 기한은 결제 후 7일이다. 환불 기한 정책.")
	newDecision := doc(p.ID, "decision", "draft", "환불 기한 14일", "환불 기한은 결제 후 14일로 늘린다. 환불 기한 정책.")
	brief := doc(p.ID, "brief", "active", "환불 화면 기획", "환불 화면 기획 본문")
	history := doc(p.ID, "history", "active", "환불 작업", "환불 작업 기록")
	foreign := doc(other.ID, "decision", "active", "다른 프로젝트 결정", "본문")

	link := func(source, target, relation string) LinkResult {
		return mustCall(t, tools, admin, "link_documents", LinkInput{ProjectID: p.ID, SourceID: source, TargetID: target, Relation: relation}).(LinkResult)
	}
	first := link(newDecision.ID, oldDecision.ID, "supersedes")
	if !first.Created || first.Link.Relation != "supersedes" {
		t.Fatalf("supersedes link not created: %+v", first)
	}
	if again := link(newDecision.ID, oldDecision.ID, "supersedes"); again.Created {
		t.Fatal("repeated link was created twice")
	}

	for _, bad := range []struct {
		in   LinkInput
		code string
	}{
		{LinkInput{ProjectID: p.ID, SourceID: oldDecision.ID, TargetID: newDecision.ID, Relation: "supersedes"}, "CONFLICT"},     // cycle
		{LinkInput{ProjectID: p.ID, SourceID: brief.ID, TargetID: oldDecision.ID, Relation: "supersedes"}, "INVALID_INPUT"},      // kind mismatch
		{LinkInput{ProjectID: p.ID, SourceID: oldDecision.ID, TargetID: brief.ID, Relation: "implements"}, "INVALID_INPUT"},      // source not history
		{LinkInput{ProjectID: p.ID, SourceID: history.ID, TargetID: history.ID, Relation: "related"}, "INVALID_INPUT"},           // self
		{LinkInput{ProjectID: p.ID, SourceID: history.ID, TargetID: brief.ID, Relation: "blocks"}, "INVALID_INPUT"},              // unknown relation
		{LinkInput{ProjectID: p.ID, SourceID: newDecision.ID, TargetID: foreign.ID, Relation: "related"}, "NOT_FOUND"},           // other project
		{LinkInput{ProjectID: p.ID, SourceID: history.ID, TargetID: brief.ID, Relation: "related", Note: strings.Repeat("가", 501)}, "INVALID_INPUT"},
	} {
		_, err := call(t, tools, admin, "link_documents", bad.in)
		denied(t, err, bad.code)
	}
	_, err := call(t, tools, viewer, "link_documents", LinkInput{ProjectID: p.ID, SourceID: history.ID, TargetID: brief.ID, Relation: "related"})
	denied(t, err, "FORBIDDEN")

	// related is undirected: the reverse request returns the existing link.
	link(brief.ID, oldDecision.ID, "related")
	if reverse := link(oldDecision.ID, brief.ID, "related"); reverse.Created || reverse.Link.SourceID != brief.ID {
		t.Fatalf("reverse related link duplicated: %+v", reverse)
	}

	// A draft replacement does not demote the decision still in effect.
	search := func() []SearchHit {
		return mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불 기한 정책", Mode: "lexical", Status: "active"}).(SearchResult).Hits
	}
	if hits := search(); len(hits) == 0 || hits[0].DocumentID != oldDecision.ID || len(hits[0].SupersededBy) != 0 {
		t.Fatalf("draft replacement affected ranking: %+v", hits)
	}
	activeStatus := "active"
	newDecision = mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: newDecision.ID, ExpectedRevision: newDecision.Revision, Reason: "확정",
		Patch: DocumentPatch{Status: &activeStatus}}).(Document)
	hits := search()
	if len(hits) < 2 || hits[0].DocumentID != newDecision.ID || hits[len(hits)-1].DocumentID != oldDecision.ID {
		t.Fatalf("superseded decision not ranked last: %+v", hits)
	}
	if by := hits[len(hits)-1].SupersededBy; len(by) != 1 || by[0].DocumentID != newDecision.ID {
		t.Fatalf("superseded_by missing: %+v", by)
	}
	decisions, err := recentDecisionsForTest(ctx, s, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decisions {
		if d["id"] == oldDecision.ID {
			t.Fatal("superseded decision listed as a recent decision")
		}
	}

	// finish_task links the history to the decision it carried out.
	run := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "links-1", Title: "환불 기한 변경 적용", Task: "환불 기한 14일 적용"}).(StartTaskResult).Run
	finish := FinishTaskInput{ProjectID: p.ID, TaskID: run.ID, Outcome: "completed", Summary: "적용", Changes: "환불 기한 변경", Decisions: "없음", Validation: "테스트", Remaining: "없음", References: "없음",
		Implements: []string{newDecision.ID, newDecision.ID}}
	_, err = call(t, tools, admin, "finish_task", FinishTaskInput{ProjectID: p.ID, TaskID: run.ID, Outcome: "completed", Summary: "x", Changes: "x", Decisions: "x", Validation: "x", Remaining: "x", References: "x", Implements: []string{history.ID}})
	denied(t, err, "INVALID_INPUT")
	completion := mustCall(t, tools, admin, "finish_task", finish).(TaskCompletion)
	historyID := completion.Documents[0].ID
	saved := mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: historyID}).(Document)
	if !strings.Contains(saved.Content, "## 실행한 기획·결정") || !strings.Contains(saved.Content, "환불 기한 14일") {
		t.Fatalf("history body lacks implemented decisions:\n%s", saved.Content)
	}
	if saved.Links == nil || len(saved.Links.Implements) != 1 || saved.Links.Implements[0].DocumentID != newDecision.ID {
		t.Fatalf("history implements link missing: %+v", saved.Links)
	}

	// Reverse lookup: which tasks carried out this decision, and what it replaced.
	links := mustCall(t, tools, viewer, "get_document_links", LinksQuery{ProjectID: p.ID, DocumentID: newDecision.ID}).(DocumentLinks)
	if len(links.ImplementedBy) != 1 || links.ImplementedBy[0].DocumentID != historyID || len(links.Supersedes) != 1 || links.Supersedes[0].DocumentID != oldDecision.ID {
		t.Fatalf("reverse links wrong: %+v", links)
	}
	old := mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: oldDecision.ID}).(Document)
	if len(old.Links.SupersededBy) != 1 || len(old.Links.Related) != 1 || old.Links.Related[0].DocumentID != brief.ID {
		t.Fatalf("old decision links wrong: %+v", old.Links)
	}
	if past := mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: oldDecision.ID, Revision: 1}).(Document); past.Links != nil {
		t.Fatal("links attached to an old revision")
	}
	_, err = call(t, tools, admin, "get_document_links", LinksQuery{ProjectID: other.ID, DocumentID: newDecision.ID})
	denied(t, err, "NOT_FOUND")

	// Unlinking restores normal ranking; related matches either direction.
	if out := mustCall(t, tools, admin, "unlink_documents", LinkInput{ProjectID: p.ID, SourceID: newDecision.ID, TargetID: oldDecision.ID, Relation: "supersedes"}).(map[string]any); out["removed"] != true {
		t.Fatalf("unlink failed: %+v", out)
	}
	if out := mustCall(t, tools, admin, "unlink_documents", LinkInput{ProjectID: p.ID, SourceID: oldDecision.ID, TargetID: brief.ID, Relation: "related"}).(map[string]any); out["removed"] != true {
		t.Fatalf("reverse related unlink failed: %+v", out)
	}
	for _, h := range search() {
		if len(h.SupersededBy) != 0 {
			t.Fatalf("hit still superseded after unlink: %+v", h)
		}
	}
}

func recentDecisionsForTest(ctx context.Context, s *Store, projectID string) ([]map[string]any, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return recentDecisions(ctx, tx, projectID, 10)
}
