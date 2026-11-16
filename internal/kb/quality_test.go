package kb

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

func TestGlossarySynonymSearch(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "glossary", Name: "용어집"}).(Project)
	target := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "PG 웹훅 설정", Kind: "reference", Content: "PG 키와 웹훅 주소를 설정한다."}).(Document)
	before := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "결제", Mode: "lexical"}).(SearchResult)
	if len(before.ExpandedTerms) != 0 || len(before.Hits) != 0 {
		t.Fatalf("matched without glossary: %+v", before)
	}
	draft := templateFixture("glossary")
	draft.Title = "결제"
	draft.Sections["synonyms"] = "payment, PG"
	draft.Status = "active"
	mustCall(t, tools, admin, "create_templated_document", TemplateDocumentInput{ProjectID: p.ID, TemplateDraft: draft})
	// A draft glossary entry does not expand searches.
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "연동", Kind: "glossary", Status: "draft", Content: "동의어: integration"})

	after := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "결제", Mode: "lexical"}).(SearchResult)
	if !reflect.DeepEqual(after.ExpandedTerms, []string{"payment", "PG"}) {
		t.Fatalf("expanded terms: %v", after.ExpandedTerms)
	}
	if !slices.ContainsFunc(after.Hits, func(h SearchHit) bool { return h.DocumentID == target.ID }) {
		t.Fatalf("synonym document not found: %+v", after.Hits)
	}
	exact := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "결제", Mode: "lexical", NoSynonyms: true}).(SearchResult)
	if len(exact.ExpandedTerms) != 0 || slices.ContainsFunc(exact.Hits, func(h SearchHit) bool { return h.DocumentID == target.ID }) {
		t.Fatal("no_synonyms still expanded")
	}
	cross := mustCall(t, tools, admin, "search_across_projects", CrossSearchInput{Query: "결제", Mode: "lexical"}).(CrossSearchResult)
	if len(cross.ExpandedTerms) == 0 {
		t.Fatal("cross-project search did not expand")
	}
}

func TestSimilarDecisionDetection(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	viewer := inviteAs(t, s, admin, "similar-viewer@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "similar", Name: "similar"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	old := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 기한 7일", Kind: "decision", Content: "환불 기한은 결제 후 7일이다. 환불 기한 정책."}).(Document)
	if len(old.SimilarDecisions) != 0 {
		t.Fatalf("first decision had suggestions: %+v", old.SimilarDecisions)
	}
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "배포 환경", Kind: "decision", Content: "운영 배포는 Railway production 하나로 한다."})
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 기한 메모", Kind: "reference", Content: "환불 기한은 결제 후 7일이다. 환불 기한 정책."})

	fresh := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 기한 14일", Kind: "decision", Status: "draft", Content: "환불 기한은 결제 후 14일로 늘린다. 환불 기한 정책."}).(Document)
	if len(fresh.SimilarDecisions) != 1 || fresh.SimilarDecisions[0].DocumentID != old.ID || fresh.SimilarDecisions[0].SuggestedRelation != "supersedes" {
		t.Fatalf("similar decisions: %+v", fresh.SimilarDecisions)
	}
	// The suggestion is not stored in the revision snapshot.
	stored := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: fresh.ID, Revision: 1}).(Document)
	if len(stored.SimilarDecisions) != 0 {
		t.Fatal("suggestion was stored in the revision")
	}
	found := mustCall(t, tools, viewer, "find_similar_decisions", SimilarDecisionsInput{ProjectID: p.ID, DocumentID: old.ID}).(map[string]any)
	if list := found["similar_decisions"].([]SimilarDocument); len(list) != 1 || list[0].DocumentID != fresh.ID {
		t.Fatalf("find_similar_decisions: %+v", found)
	}
	mustCall(t, tools, admin, "link_documents", LinkInput{ProjectID: p.ID, SourceID: fresh.ID, TargetID: old.ID, Relation: "supersedes"})
	found = mustCall(t, tools, admin, "find_similar_decisions", SimilarDecisionsInput{ProjectID: p.ID, DocumentID: fresh.ID}).(map[string]any)
	if list := found["similar_decisions"].([]SimilarDocument); len(list) != 0 {
		t.Fatalf("linked document still suggested: %+v", list)
	}
	_, err := call(t, tools, admin, "find_similar_decisions", SimilarDecisionsInput{ProjectID: p.ID, DocumentID: mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "기획", Kind: "brief", Content: "본문"}).(Document).ID})
	denied(t, err, "INVALID_INPUT")

	// Templated decisions report suggestions too.
	draft := templateFixture("decision")
	draft.Title = "환불 기한 30일"
	draft.Sections["decision"] = "환불 기한은 결제 후 30일로 늘린다. 환불 기한 정책."
	templated := mustCall(t, tools, admin, "create_templated_document", TemplateDocumentInput{ProjectID: p.ID, TemplateDraft: draft}).(Document)
	if len(templated.SimilarDecisions) == 0 {
		t.Fatal("templated decision had no suggestions")
	}
}

func TestRecheckOwnersAndDigest(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "recheck-editor@example.com")
	viewer := inviteAs(t, s, admin, "recheck-viewer@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "recheck-owner", Name: "recheck"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	ref := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "운영 절차", Kind: "reference", Content: "배포 명령"}).(Document)
	stale := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "오래된 기획", Kind: "brief", Content: "기획"}).(Document)
	history := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "작업", Kind: "history", Content: "기록"}).(Document)

	for _, bad := range []struct {
		actor Identity
		in    RecheckOwnerInput
		code  string
	}{
		{admin, RecheckOwnerInput{ProjectID: p.ID, DocumentID: ref.ID, OwnerID: viewer.User.ID}, "INVALID_INPUT"},
		{admin, RecheckOwnerInput{ProjectID: p.ID, DocumentID: history.ID, OwnerID: editor.User.ID}, "INVALID_INPUT"},
		{admin, RecheckOwnerInput{ProjectID: p.ID, DocumentID: ref.ID, OwnerID: editor.User.ID, IntervalDays: 4000}, "INVALID_INPUT"},
		{viewer, RecheckOwnerInput{ProjectID: p.ID, DocumentID: ref.ID, OwnerID: editor.User.ID}, "FORBIDDEN"},
	} {
		_, err := call(t, tools, bad.actor, "assign_recheck_owner", bad.in)
		denied(t, err, bad.code)
	}
	assigned := mustCall(t, tools, admin, "assign_recheck_owner", RecheckOwnerInput{ProjectID: p.ID, DocumentID: ref.ID, OwnerID: editor.User.ID, IntervalDays: 30, Note: "배포 명령 확인"}).(RecheckAssignment)
	if assigned.OwnerID != editor.User.ID || assigned.IntervalDays != 30 || assigned.NextDueAt == "" {
		t.Fatalf("assignment: %+v", assigned)
	}
	digest := mustCall(t, tools, viewer, "get_recheck_digest", RecheckDigestInput{ProjectID: p.ID}).(RecheckDigest)
	if len(digest.ByOwner) != 0 {
		t.Fatalf("fresh document already due: %+v", digest.ByOwner)
	}
	// Age both documents past their intervals.
	if _, err := s.DB.ExecContext(ctx, `UPDATE documents SET updated_at=now()-interval '120 days' WHERE id IN ($1,$2)`, ref.ID, stale.ID); err != nil {
		t.Fatal(err)
	}
	digest = mustCall(t, tools, viewer, "get_recheck_digest", RecheckDigestInput{ProjectID: p.ID, IncludeUnassigned: true}).(RecheckDigest)
	if len(digest.ByOwner) != 1 || digest.ByOwner[0].OwnerID != editor.User.ID || digest.ByOwner[0].Due[0].DocumentID != ref.ID || digest.ByOwner[0].Due[0].DaysOverdue < 89 {
		t.Fatalf("digest by owner: %+v", digest.ByOwner)
	}
	if digest.UnassignedTotal != 1 || digest.Unassigned[0].DocumentID != stale.ID || digest.Summary == "" {
		t.Fatalf("digest unassigned: %+v", digest)
	}
	// limit applies per owner: totals stay exact and every owner stays listed.
	ref2 := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "배포 체크리스트", Kind: "reference", Content: "체크리스트"}).(Document)
	mustCall(t, tools, admin, "assign_recheck_owner", RecheckOwnerInput{ProjectID: p.ID, DocumentID: ref2.ID, OwnerID: editor.User.ID, IntervalDays: 30})
	mustCall(t, tools, admin, "assign_recheck_owner", RecheckOwnerInput{ProjectID: p.ID, DocumentID: stale.ID, OwnerID: admin.User.ID})
	if _, err := s.DB.ExecContext(ctx, `UPDATE documents SET updated_at=now()-interval '120 days' WHERE id=$1`, ref2.ID); err != nil {
		t.Fatal(err)
	}
	limited := mustCall(t, tools, viewer, "get_recheck_digest", RecheckDigestInput{ProjectID: p.ID, Limit: 1}).(RecheckDigest)
	totals := map[string][2]int{}
	for _, g := range limited.ByOwner {
		totals[g.OwnerID] = [2]int{g.Total, len(g.Due)}
	}
	if len(limited.ByOwner) != 2 || totals[editor.User.ID] != [2]int{2, 1} || totals[admin.User.ID] != [2]int{1, 1} {
		t.Fatalf("per-owner limit: %+v", limited.ByOwner)
	}
	mustCall(t, tools, admin, "assign_recheck_owner", RecheckOwnerInput{ProjectID: p.ID, DocumentID: ref2.ID, Remove: true})
	mustCall(t, tools, admin, "assign_recheck_owner", RecheckOwnerInput{ProjectID: p.ID, DocumentID: stale.ID, Remove: true})
	run := mustCall(t, tools, editor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "recheck-1", Title: "배포", Task: "배포 명령 정리"}).(StartTaskResult)
	if run.Run.Context.Rechecks == nil || run.Run.Context.Rechecks.Total != 1 || run.Run.Context.Rechecks.DueForYou[0].DocumentID != ref.ID {
		t.Fatalf("task context rechecks: %+v", run.Run.Context.Rechecks)
	}
	adminRun := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "recheck-2", Title: "배포", Task: "배포 명령 정리"}).(StartTaskResult)
	if adminRun.Run.Context.Rechecks != nil {
		t.Fatal("rechecks shown to a non-owner")
	}
	_, err := call(t, tools, editor, "confirm_recheck", ConfirmRecheckInput{ProjectID: p.ID, DocumentID: stale.ID, Note: "확인"})
	denied(t, err, "CONFLICT")
	confirmed := mustCall(t, tools, editor, "confirm_recheck", ConfirmRecheckInput{ProjectID: p.ID, DocumentID: ref.ID, Note: "배포 명령이 여전히 맞음"}).(RecheckAssignment)
	if confirmed.LastCheckedAt == nil || confirmed.LastCheckedBy == nil || *confirmed.LastCheckedBy != editor.User.ID {
		t.Fatalf("confirmation: %+v", confirmed)
	}
	digest = mustCall(t, tools, admin, "get_recheck_digest", RecheckDigestInput{ProjectID: p.ID}).(RecheckDigest)
	if len(digest.ByOwner) != 0 {
		t.Fatal("confirmed document is still due")
	}
	list := mustCall(t, tools, viewer, "list_recheck_assignments", RecheckDigestInput{ProjectID: p.ID, OwnerID: editor.User.ID}).([]RecheckAssignment)
	if len(list) != 1 || list[0].CheckNote != "배포 명령이 여전히 맞음" {
		t.Fatalf("assignments: %+v", list)
	}
	removed := mustCall(t, tools, admin, "assign_recheck_owner", RecheckOwnerInput{ProjectID: p.ID, DocumentID: ref.ID, Remove: true}).(map[string]any)
	if removed["removed"] != true {
		t.Fatalf("remove: %+v", removed)
	}
}

func TestContextFeedbackMetricsAndDecisionCandidates(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "feedback-editor@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "feedback", Name: "feedback"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	useful := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 정책", Kind: "reference", Content: "환불 정책 본문"}).(Document)
	noisy := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 회의록", Kind: "artifact", Content: "환불 회의 잡담"}).(Document)
	later := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "결제 연동 키", Kind: "reference", Content: "키 위치"}).(Document)
	oldDecision := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "결정: 첨부는 Railway 버킷에 저장", Kind: "decision", Content: "첨부는 Railway 버킷에 저장한다. AWS 계정은 만들지 않는다."}).(Document)

	start := mustCall(t, tools, editor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "feedback-1", Title: "환불", Task: "환불 정책 화면"}).(StartTaskResult)
	offered := map[string]bool{}
	for _, src := range start.Run.Context.Sources {
		offered[src.DocumentID] = true
	}
	if !offered[useful.ID] || !offered[noisy.ID] {
		t.Fatalf("expected both documents offered: %+v", start.Run.Context.Sources)
	}
	_, err := call(t, tools, admin, "record_context_feedback", ContextFeedbackInput{ProjectID: p.ID, TaskID: start.Run.ID, UsedSources: []string{useful.ID}})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, editor, "record_context_feedback", ContextFeedbackInput{ProjectID: p.ID, TaskID: start.Run.ID, UsedSources: []string{useful.ID}, MisleadingSources: []string{useful.ID}})
	denied(t, err, "INVALID_INPUT")

	in := finishFixture(p.ID, start.Run.ID)
	in.Decisions = "- 첨부는 Railway 버킷에 저장하기로 함. 이유: AWS 계정을 만들지 않기 위해\n- 환불 기한 표시는 화면 상단에 두기로 결정했다\n- 테스트를 실행했다"
	in.UsedSources = &[]string{useful.ID, later.ID}
	in.MissingKnowledge = "환불 수수료 규정"
	done := mustCall(t, tools, editor, "finish_task", in).(TaskCompletion)
	fb := done.Feedback
	if fb == nil || fb.Used != 2 || fb.UsedOffered != 1 || fb.UsedNotOffered != 1 || fb.Unused != fb.Offered-1 || fb.Precision == nil {
		t.Fatalf("feedback: %+v", fb)
	}
	if len(done.DecisionCandidates) != 2 {
		t.Fatalf("decision candidates: %+v", done.DecisionCandidates)
	}
	first := done.DecisionCandidates[0]
	if first.Reason != "AWS 계정을 만들지 않기 위해" || first.Draft.Kind != "decision" || first.Draft.Status != "draft" {
		t.Fatalf("first candidate: %+v", first)
	}
	if len(first.SimilarDecisions) == 0 || first.SimilarDecisions[0].DocumentID != oldDecision.ID {
		t.Fatalf("candidate did not surface the existing decision: %+v", first.SimilarDecisions)
	}
	// Candidates are suggestions only: nothing was saved, and the draft is valid.
	if len(done.Documents) != 1 {
		t.Fatalf("candidates were saved: %+v", done.Documents)
	}
	saved := mustCall(t, tools, editor, "create_templated_document", TemplateDocumentInput{ProjectID: p.ID, TemplateDraft: done.DecisionCandidates[1].Draft}).(Document)
	if saved.Status != "draft" {
		t.Fatal("candidate draft was not saved as draft")
	}
	// Retrying the identical finish returns the same completion.
	again := mustCall(t, tools, editor, "finish_task", in).(TaskCompletion)
	if !reflect.DeepEqual(again, done) {
		t.Fatal("retry changed the completion")
	}

	// A decision saved in the same call suppresses its candidate.
	start2 := mustCall(t, tools, editor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "feedback-2", Title: "환불 2", Task: "환불 정책 화면 보완"}).(StartTaskResult)
	in2 := finishFixture(p.ID, start2.Run.ID)
	in2.Decisions = "환불 기한 표시는 화면 상단에 두기로 결정했다."
	dec := templateFixture("decision")
	dec.Title = "결정: 환불 기한 표시는 화면 상단"
	dec.Sections["decision"] = "환불 기한 표시는 화면 상단에 두기로 결정했다."
	in2.Documents = []TemplateDraft{dec}
	in2.UsedSources = &[]string{noisy.ID}
	done2 := mustCall(t, tools, editor, "finish_task", in2).(TaskCompletion)
	if len(done2.DecisionCandidates) != 0 {
		t.Fatalf("saved decision proposed again: %+v", done2.DecisionCandidates)
	}

	// An explicit empty used_sources is recorded even when start_task offered
	// nothing, so the task still counts in the denominators.
	start3 := mustCall(t, tools, editor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "feedback-3", Title: "무관", Task: "zqx 무관 xyw"}).(StartTaskResult)
	if len(start3.Run.Context.Sources) != 0 {
		t.Fatalf("unrelated task was offered sources: %+v", start3.Run.Context.Sources)
	}
	in3 := finishFixture(p.ID, start3.Run.ID)
	in3.UsedSources = &[]string{}
	if done3 := mustCall(t, tools, editor, "finish_task", in3).(TaskCompletion); done3.Feedback == nil || done3.Feedback.Used != 0 {
		t.Fatalf("empty used_sources not recorded: %+v", done3.Feedback)
	}
	// Omitting used_sources records no feedback.
	start4 := mustCall(t, tools, editor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "feedback-4", Title: "환불 4", Task: "환불 정책 정리"}).(StartTaskResult)
	if done4 := mustCall(t, tools, editor, "finish_task", finishFixture(p.ID, start4.Run.ID)).(TaskCompletion); done4.Feedback != nil {
		t.Fatalf("omitted used_sources recorded feedback: %+v", done4.Feedback)
	}

	metrics := mustCall(t, tools, admin, "get_retrieval_metrics", RetrievalMetricsInput{ProjectID: p.ID}).(RetrievalMetrics)
	if metrics.HitRate == nil || *metrics.HitRate != 0.667 {
		t.Fatalf("hit rate with a source-free task: %+v", metrics.HitRate)
	}
	if metrics.TasksWithFeedback != 3 || metrics.UsedTotal != 3 || metrics.UsedNotOffered != 1 || metrics.Precision == nil || metrics.Coverage == nil || metrics.MeanReciprocalRank == nil {
		t.Fatalf("metrics: %+v", metrics)
	}
	if len(metrics.RecentMissing) != 1 || metrics.RecentMissing[0] != "환불 수수료 규정" {
		t.Fatalf("missing notes: %+v", metrics.RecentMissing)
	}
	if len(metrics.MostUsed) == 0 || metrics.MostUsed[0].Title == "" {
		t.Fatalf("most used: %+v", metrics.MostUsed)
	}
	_, err = call(t, tools, admin, "get_retrieval_metrics", RetrievalMetricsInput{ProjectID: p.ID, Days: 400})
	denied(t, err, "INVALID_INPUT")
}

func TestStaleTaskRunCleanup(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "cleanup-editor@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "cleanup", Name: "cleanup"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	start := func(actor Identity, key string) TaskRun {
		return mustCall(t, tools, actor, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: key, Title: key, Task: key + " 작업"}).(StartTaskResult).Run
	}
	old1, old2, fresh, mine := start(editor, "old-1"), start(editor, "old-2"), start(editor, "fresh"), start(admin, "admin-old")
	if _, err := s.DB.ExecContext(ctx, `UPDATE task_runs SET started_at=now()-interval '30 days' WHERE id IN ($1,$2,$3)`, old1.ID, old2.ID, mine.ID); err != nil {
		t.Fatal(err)
	}
	stale := mustCall(t, tools, editor, "list_task_runs", TaskListInput{ProjectID: p.ID, Status: "open", OlderThanDays: 7}).([]map[string]any)
	if len(stale) != 3 {
		t.Fatalf("stale runs: %+v", stale)
	}
	// The task board listing carries names, linked pull requests and open issues.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO task_pull_requests(task_id,project_id,repository,number,url,state) VALUES($1,$2,'acme/kb',7,'https://github.com/acme/kb/pull/7','merged')`, old2.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	linked := mustCall(t, tools, editor, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "작업 질의", Body: "확인 필요", AssigneeID: admin.User.ID, TaskID: old2.ID}).(Issue)
	board := mustCall(t, tools, admin, "list_task_runs", TaskListInput{ProjectID: p.ID, Status: "open", Mine: true}).([]map[string]any)
	if len(board) != 1 || board[0]["id"] != mine.ID || board[0]["created_by_name"] == "" {
		t.Fatalf("mine filter: %+v", board)
	}
	board = mustCall(t, tools, admin, "list_task_runs", TaskListInput{ProjectID: p.ID, Status: "open"}).([]map[string]any)
	for _, r := range board {
		prs, _ := r["pull_requests"].([]any)
		switch r["id"] {
		case old2.ID:
			issues, _ := r["issues"].([]any)
			if len(prs) != 1 || prs[0].(map[string]any)["state"] != "merged" || r["open_issues"] != float64(1) || r["issue_count"] != float64(1) ||
				len(issues) != 1 || issues[0].(map[string]any)["id"] != linked.ID || issues[0].(map[string]any)["status"] != "open" {
				t.Fatalf("board row with PR and issue: %+v", r)
			}
		default:
			if issues, _ := r["issues"].([]any); len(prs) != 0 || r["open_issues"] != float64(0) || len(issues) != 0 {
				t.Fatalf("board row without links: %+v", r)
			}
		}
	}
	// Only the creator or the owner may close a task.
	_, err := call(t, tools, editor, "close_task_run", CloseTaskInput{ProjectID: p.ID, TaskID: mine.ID, Reason: "중단"})
	denied(t, err, "FORBIDDEN")
	closed := mustCall(t, tools, editor, "close_task_run", CloseTaskInput{ProjectID: p.ID, TaskID: old1.ID, Reason: "세션 중단, 다른 작업으로 대체"}).(map[string]any)
	if closed["status"] != "abandoned" || closed["closed_reason"] != "세션 중단, 다른 작업으로 대체" {
		t.Fatalf("closed: %+v", closed)
	}
	if again := mustCall(t, tools, editor, "close_task_run", CloseTaskInput{ProjectID: p.ID, TaskID: old1.ID, Reason: "다른 이유"}).(map[string]any); again["closed_reason"] != "세션 중단, 다른 작업으로 대체" {
		t.Fatal("repeated close changed the reason")
	}
	_, err = call(t, tools, editor, "finish_task", finishFixture(p.ID, old1.ID))
	denied(t, err, "CONFLICT")

	preview := mustCall(t, tools, editor, "cleanup_stale_task_runs", CleanupTasksInput{ProjectID: p.ID}).(map[string]any)
	if preview["applied"] != false || preview["total_open_stale"] != 2 {
		t.Fatalf("preview: %+v", preview)
	}
	_, err = call(t, tools, editor, "cleanup_stale_task_runs", CleanupTasksInput{ProjectID: p.ID, Apply: true, Reason: "정리"})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, admin, "cleanup_stale_task_runs", CleanupTasksInput{ProjectID: p.ID, Apply: true})
	denied(t, err, "INVALID_INPUT")
	applied := mustCall(t, tools, admin, "cleanup_stale_task_runs", CleanupTasksInput{ProjectID: p.ID, Apply: true, Reason: "30일 이상 열린 작업 정리"}).(map[string]any)
	if applied["closed"] != int64(2) {
		t.Fatalf("applied: %+v", applied)
	}
	open := mustCall(t, tools, admin, "list_task_runs", TaskListInput{ProjectID: p.ID, Status: "open"}).([]map[string]any)
	if len(open) != 1 || open[0]["id"] != fresh.ID {
		t.Fatalf("open after cleanup: %+v", open)
	}
	abandoned := mustCall(t, tools, admin, "list_task_runs", TaskListInput{ProjectID: p.ID, Status: "abandoned"}).([]map[string]any)
	if len(abandoned) != 3 {
		t.Fatalf("abandoned: %+v", abandoned)
	}
	// Completed tasks cannot be closed.
	mustCall(t, tools, editor, "finish_task", finishFixture(p.ID, fresh.ID))
	_, err = call(t, tools, editor, "close_task_run", CloseTaskInput{ProjectID: p.ID, TaskID: fresh.ID, Reason: "x"})
	denied(t, err, "CONFLICT")
}
