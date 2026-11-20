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

func TestUsageStatsTodos(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "usage-todos", Name: "usage-todos"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "usage-todos-other", Name: "usage-todos-other"}).(Project)

	// An empty board still reports three priority rows and zero-filled weeks.
	empty := mustCall(t, tools, admin, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 7}).(UsageStats).Todos
	if empty.Created != 0 || empty.Completed != 0 || empty.AvgLeadDays != nil || empty.MedianLeadDays != nil || len(empty.Backlog) != 3 || len(empty.Weekly) < 1 || len(empty.Weekly) > 2 {
		t.Fatalf("empty: %+v", empty)
	}

	save := func(project, title, status, priority string) Todo {
		return mustCall(t, tools, admin, "save_todo", SaveTodoInput{ProjectID: project, Title: title, Status: status, Priority: priority}).(Todo)
	}
	fast := save(p.ID, "빠른 완료", "completed", "high")
	slow := save(p.ID, "느린 완료", "completed", "medium")
	old := save(p.ID, "지난달 완료", "completed", "low")
	waitHigh := save(p.ID, "오래 기다림", "not_started", "high")
	save(p.ID, "진행 중", "in_progress", "high")
	save(p.ID, "검토", "in_review", "medium")
	save(p.ID, "반영 대기", "pending_release", "low")
	save(p.ID, "아이디어", "idea", "high")
	save(other.ID, "다른 프로젝트", "not_started", "high")

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// fast: 1 day lead; slow: 4 days lead; old: completed 40 days ago (outside a 7-day window).
	exec(`UPDATE todos SET created_at=now()-interval '1 day', completed_at=now() WHERE id=$1`, fast.ID)
	exec(`UPDATE todos SET created_at=now()-interval '5 days', completed_at=now()-interval '1 day' WHERE id=$1`, slow.ID)
	exec(`UPDATE todos SET created_at=now()-interval '50 days', completed_at=now()-interval '40 days' WHERE id=$1`, old.ID)
	exec(`UPDATE todos SET created_at=now()-interval '20 days' WHERE id=$1`, waitHigh.ID)

	got := mustCall(t, tools, admin, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 7}).(UsageStats).Todos
	// Created in the window: all but old (50 days) and waitHigh (20 days).
	if got.Created != 6 || got.Completed != 2 || got.Waiting != 4 || got.Ideas != 1 {
		t.Fatalf("summary: %+v", got)
	}
	if got.AvgLeadDays == nil || *got.AvgLeadDays != 2.5 || got.MedianLeadDays == nil || *got.MedianLeadDays != 2.5 {
		t.Fatalf("lead: avg %v median %v", got.AvgLeadDays, got.MedianLeadDays)
	}
	created, completed := 0, 0
	for i, w := range got.Weekly {
		created += w.Created
		completed += w.Completed
		if i > 0 && w.WeekStart <= got.Weekly[i-1].WeekStart {
			t.Fatalf("weeks out of order: %+v", got.Weekly)
		}
	}
	if created != got.Created || completed != got.Completed {
		t.Fatalf("weekly sums %d/%d vs %+v", created, completed, got.Weekly)
	}
	want := []UsageTodoBacklog{
		{Priority: "high", NotStarted: 1, InProgress: 1, Total: 2, OldestDays: 20},
		{Priority: "medium", InReview: 1, Total: 1},
		{Priority: "low", PendingRelease: 1, Total: 1},
	}
	for i, b := range got.Backlog {
		if b != want[i] {
			t.Fatalf("backlog[%d]: %+v want %+v", i, b, want[i])
		}
	}

	// A 90-day window includes the to-do completed 40 days ago.
	wide := mustCall(t, tools, admin, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 90}).(UsageStats).Todos
	if wide.Completed != 3 || wide.Created != 8 || wide.AvgLeadDays == nil || *wide.AvgLeadDays != 5 || *wide.MedianLeadDays != 4 {
		t.Fatalf("wide: %+v avg %v median %v", wide, wide.AvgLeadDays, wide.MedianLeadDays)
	}
}

// A dropped to-do is stored as completed but is not finished work: it must not
// count as completed, in the weekly trend or in the lead times.
func TestUsageStatsTodosExcludeDropped(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "usage-todos-dropped", Name: "usage-todos-dropped"}).(Project)

	save := func(title string) Todo {
		return mustCall(t, tools, admin, "save_todo", SaveTodoInput{ProjectID: p.ID, Title: title, Status: "completed", Priority: "medium"}).(Todo)
	}
	done := save("완료")
	dropped := save("폐기")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// done: 2 days lead; dropped: 10 days lead, which would skew the averages if counted.
	exec(`UPDATE todos SET created_at=now()-interval '3 days', completed_at=now()-interval '1 day' WHERE id=$1`, done.ID)
	exec(`UPDATE todos SET created_at=now()-interval '11 days', completed_at=now()-interval '1 day', resolution='dropped', resolution_note='범위 제외' WHERE id=$1`, dropped.ID)

	got := mustCall(t, tools, admin, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 30}).(UsageStats).Todos
	if got.Completed != 1 {
		t.Fatalf("completed: %+v", got)
	}
	if got.AvgLeadDays == nil || *got.AvgLeadDays != 2 || got.MedianLeadDays == nil || *got.MedianLeadDays != 2 {
		t.Fatalf("lead: avg %v median %v", got.AvgLeadDays, got.MedianLeadDays)
	}
	completed := 0
	for _, w := range got.Weekly {
		completed += w.Completed
	}
	if completed != 1 {
		t.Fatalf("weekly completed %d: %+v", completed, got.Weekly)
	}
}
