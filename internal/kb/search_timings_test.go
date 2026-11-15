package kb

import (
	"context"
	"errors"
	"testing"
)

func TestSearchTimings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "timing", Name: "timing"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "timing-other", Name: "timing-other"}).(Project)
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 정책", Kind: "reference", Content: "환불 정책 본문"})
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "배포 절차", Kind: "reference", Content: "배포 절차 본문"})
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: other.ID, Title: "다른 프로젝트", Kind: "reference", Content: "집계에서 빠져야 함"})

	// Without an embedder the default mode is lexical; only the keyword phase runs.
	mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불"})

	e := &testEmbedder{model: "timing-local"}
	s.Embedder = e
	if _, err := s.ProcessEmbeddingBatch(ctx); err != nil {
		t.Fatal(err)
	}
	mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불"})
	mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불", Mode: "semantic"})
	mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: other.ID, Query: "환불", Mode: "semantic"})
	mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "timing-1", Title: "환불", Task: "환불 정책 화면"})
	// A failed query embedding falls back to keywords and scans no vectors.
	e.fn = func(context.Context, []string) ([][]float32, error) { return nil, errors.New("down") }
	out := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불"}).(SearchResult)
	if out.Retrieval != "lexical-fallback" {
		t.Fatalf("fallback: %+v", out)
	}

	type row struct {
		Source, Mode, Retrieval string
		Embed, Vector, Lexical  bool
		Scanned, Hits           int
	}
	rows, err := s.DB.Query(`SELECT source,mode,retrieval,embed_ms IS NOT NULL,vector_ms IS NOT NULL,lexical_ms IS NOT NULL,scanned_chunks,hits
	FROM search_timings WHERE project_id=$1 ORDER BY id`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Source, &r.Mode, &r.Retrieval, &r.Embed, &r.Vector, &r.Lexical, &r.Scanned, &r.Hits); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	want := []row{
		{"search_knowledge", "lexical", "lexical", false, false, true, 0, 1},
		{"search_knowledge", "hybrid", "hybrid", true, true, true, 2, 2},
		{"search_knowledge", "semantic", "semantic", true, true, false, 2, 2},
		{"task_context", "hybrid", "hybrid", true, true, true, 2, 2},
		{"search_knowledge", "hybrid", "lexical-fallback", true, false, true, 0, 1},
	}
	if len(got) != len(want) {
		t.Fatalf("timings: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("timing %d: got %+v want %+v", i, got[i], want[i])
		}
	}

	stats := mustCall(t, tools, admin, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 1}).(UsageStats)
	by := map[string]UsageSearchLatency{}
	for _, l := range stats.SearchLatency {
		by[l.Retrieval] = l
	}
	if len(by) != 4 || by["hybrid"].Searches != 2 || by["semantic"].Searches != 1 || by["lexical"].Searches != 1 || by["lexical-fallback"].Searches != 1 {
		t.Fatalf("latency groups: %+v", stats.SearchLatency)
	}
	h := by["hybrid"]
	if h.AvgEmbedMS == nil || h.P95VectorMS == nil || h.AvgLexicalMS == nil || h.MaxScannedChunks != 2 || h.P50MS > h.P95MS || h.P95MS > h.MaxMS {
		t.Fatalf("hybrid latency: %+v", h)
	}
	if l := by["lexical"]; l.AvgEmbedMS != nil || l.P95VectorMS != nil || l.AvgLexicalMS == nil || l.MaxScannedChunks != 0 {
		t.Fatalf("lexical latency: %+v", l)
	}

	// Expired samples are purged with the search-miss retention; recent ones stay.
	if _, err := s.DB.Exec(`UPDATE search_timings SET created_at=now()-interval '400 days' WHERE project_id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PurgeSearchTimings(ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	var left int
	if err := s.DB.QueryRow(`SELECT count(*) FROM search_timings`).Scan(&left); err != nil || left != len(want) {
		t.Fatalf("left after purge: %d %v", left, err)
	}
}
