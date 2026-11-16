package kb

import "testing"

// Only search_knowledge records zero-result queries; a task description sent
// to start_task or get_task_context must not appear in search_misses.
func TestOnlySearchKnowledgeRecordsSearchMisses(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "miss-source", Name: "miss-source"}).(Project)

	count := func() int {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM search_misses WHERE project_id=$1`, p.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "miss-1", Title: "없는 주제", Task: "zzqx 존재하지 않는 키워드 작업"})
	mustCall(t, tools, admin, "get_task_context", ContextInput{ProjectID: p.ID, Task: "zzqx 존재하지 않는 키워드 작업"})
	if n := count(); n != 0 {
		t.Fatalf("task context recorded %d search misses", n)
	}
	mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "zzqx 존재하지 않는 키워드", Mode: "lexical"})
	if n := count(); n != 1 {
		t.Fatalf("search_knowledge recorded %d search misses, want 1", n)
	}
}
