package kb

import (
	"context"
	"testing"
)

func TestUsageStats(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "usage-editor@example.com")
	outsider := inviteAs(t, s, admin, "usage-outsider@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "usage", Name: "usage"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "usage-other", Name: "usage-other"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "viewer"})

	doc := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 정책", Kind: "reference", Content: "환불 정책 본문"}).(Document)
	content := "환불 정책 본문 보완"
	mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: doc.ID, ExpectedRevision: 1, Reason: "보완", Patch: DocumentPatch{Content: &content}})
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "결정: 환불은 7일", Kind: "decision", Status: "draft", Content: "환불 기한은 7일"})
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: other.ID, Title: "다른 프로젝트", Kind: "reference", Content: "집계에서 빠져야 함"})
	mustCall(t, tools, editor, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: doc.ID})
	mustCall(t, tools, editor, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: doc.ID})
	mustCall(t, tools, editor, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불", Mode: "lexical"})
	mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "usage-1", Title: "환불", Task: "환불 정책 화면"})
	// Zero-result searches are grouped after whitespace and case normalization.
	mustCall(t, tools, editor, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "Railway  배포 절차", Mode: "lexical"})
	mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "railway 배포 절차", Mode: "lexical"})
	mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: other.ID, Query: "zzqx 미존재 키워드", Mode: "lexical"})

	stats := mustCall(t, tools, editor, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 7}).(UsageStats)
	if stats.Days != 7 || len(stats.Daily) != 7 || stats.Daily[6].Date != stats.To || stats.Daily[0].Date != stats.From {
		t.Fatalf("window: %+v", stats)
	}
	tot := stats.Totals
	if tot.DocumentWrites != 3 || tot.DocumentsNew != 2 || tot.TasksStarted != 1 || tot.DocumentReads != 2 || tot.Searches != 4 || tot.ActiveUsers != 2 || tot.SearchMisses != 2 {
		t.Fatalf("totals: %+v", tot)
	}
	today := stats.Daily[6]
	if today.DocumentWrites != 3 || today.TasksStarted != 1 || today.Calls != tot.Calls || today.ActiveUsers != 2 {
		t.Fatalf("today: %+v totals %+v", today, tot)
	}
	if stats.Inventory.Documents != 2 || stats.Inventory.Members != 2 || stats.Inventory.OpenTasks != 1 || len(stats.Inventory.ByKind) != 2 || len(stats.Inventory.ByStatus) != 2 {
		t.Fatalf("inventory: %+v", stats.Inventory)
	}
	if len(stats.TopDocuments) != 1 || stats.TopDocuments[0].DocumentID != doc.ID || stats.TopDocuments[0].Reads != 2 {
		t.Fatalf("top documents: %+v", stats.TopDocuments)
	}
	if len(stats.TopUsers) != 2 || stats.TopUsers[0].UserID != admin.User.ID || stats.TopUsers[0].DocumentWrites != 3 || stats.TopUsers[0].TasksStarted != 1 {
		t.Fatalf("top users: %+v", stats.TopUsers)
	}
	if len(stats.SearchMisses) != 1 || stats.SearchMisses[0].Query != "railway 배포 절차" || stats.SearchMisses[0].Count != 2 {
		t.Fatalf("search misses: %+v", stats.SearchMisses)
	}
	if len(stats.KindGrowth) != 2 {
		t.Fatalf("kind growth: %+v", stats.KindGrowth)
	}
	for _, g := range stats.KindGrowth {
		if g.Start != 0 || g.Added != 1 || g.Total != 1 || len(g.Cumulative) != 7 || g.Cumulative[6] != 1 {
			t.Fatalf("kind growth %s: %+v", g.Kind, g)
		}
	}
	found := false
	for _, a := range stats.TopActions {
		if a.Key == "get_document" && a.Count == 2 {
			found = true
		}
		if a.Key == "get_usage_stats" {
			t.Fatal("the stats call counted itself")
		}
	}
	if !found {
		t.Fatalf("top actions: %+v", stats.TopActions)
	}

	// The previous stats call is now in the audit log; the default window is 30 days.
	again := mustCall(t, tools, editor, "get_usage_stats", UsageStatsInput{ProjectID: p.ID}).(UsageStats)
	if again.Days != 30 || len(again.Daily) != 30 || again.Totals.Calls != tot.Calls+1 {
		t.Fatalf("second call: days %d calls %d (was %d)", again.Days, again.Totals.Calls, tot.Calls)
	}

	// Expired zero-result searches are purged; recent ones stay.
	if _, err := s.DB.Exec(`UPDATE search_misses SET created_at=now()-interval '400 days' WHERE query='zzqx 미존재 키워드'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PurgeSearchMisses(context.Background()); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	var left int
	if err := s.DB.QueryRow(`SELECT count(*) FROM search_misses`).Scan(&left); err != nil || left != 2 {
		t.Fatalf("left after purge: %d %v", left, err)
	}

	_, err := call(t, tools, outsider, "get_usage_stats", UsageStatsInput{ProjectID: p.ID})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, editor, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 366})
	denied(t, err, "INVALID_INPUT")
}
