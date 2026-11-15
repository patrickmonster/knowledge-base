package kb

import (
	"context"
	"testing"
)

func TestParseHNSWThresholds(t *testing.T) {
	got, err := ParseHNSWThresholds("", "")
	if err != nil || got != DefaultHNSWThresholds {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	got, err = ParseHNSWThresholds("250", "50000")
	if err != nil || got != (HNSWThresholds{VectorP95MS: 250, ScannedChunks: 50000}) {
		t.Fatalf("set: %+v %v", got, err)
	}
	for _, c := range [][2]string{{"0", ""}, {"60001", ""}, {"x", ""}, {"", "99"}, {"", "100000001"}, {"", "1e4"}} {
		if _, err := ParseHNSWThresholds(c[0], c[1]); err == nil {
			t.Fatalf("accepted %q", c)
		}
	}
}

func TestHNSWReviewStatus(t *testing.T) {
	th := SearchThresholds{VectorP95MS: 100, ScannedChunks: 20000}
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		searches int
		p95      *float64
		chunks   int
		status   string
		vec, chk bool
	}{
		{0, nil, 0, "insufficient_data", false, false},
		{5, f(150), 300, "insufficient_data", true, false}, // too few samples for p95 alone
		{5, f(10), 20000, "review", false, true},           // chunk count needs no samples
		{20, f(99.9), 19999, "ok", false, false},
		{20, f(100), 300, "review", true, false},
	}
	for i, c := range cases {
		r := hnswReview(th, c.searches, c.p95, c.chunks)
		if r.Status != c.status || r.VectorExceeded != c.vec || r.ChunksExceeded != c.chk || r.MinSamples != minHNSWVectorSamples {
			t.Fatalf("case %d: %+v", i, r)
		}
	}
}

func TestSearchThresholds(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "hnsw", Name: "hnsw"}).(Project)
	editor := inviteAs(t, s, owner, "hnsw-editor@example.com")
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})

	// Server defaults apply until the project overrides them.
	s.HNSWReview = HNSWThresholds{VectorP95MS: 300, ScannedChunks: 5000}
	stats := mustCall(t, tools, editor, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 1}).(UsageStats)
	th := stats.HNSWReview.Thresholds
	if th.VectorP95MS != 300 || th.VectorP95MSSource != "server" || th.ScannedChunks != 5000 || th.ScannedChunksSource != "server" || stats.HNSWReview.Status != "insufficient_data" {
		t.Fatalf("server defaults: %+v", stats.HNSWReview)
	}

	if _, err := call(t, tools, editor, "set_search_thresholds", SetSearchThresholdsInput{ProjectID: p.ID, VectorP95MS: 50}); err == nil {
		t.Fatal("editor changed thresholds")
	}
	for _, in := range []SetSearchThresholdsInput{{ProjectID: p.ID, VectorP95MS: 60001}, {ProjectID: p.ID, ScannedChunks: 99}, {ProjectID: p.ID, VectorP95MS: -1}} {
		if _, err := call(t, tools, owner, "set_search_thresholds", in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}

	// Only the chunk count overridden: the vector time stays on the server default.
	set := mustCall(t, tools, owner, "set_search_thresholds", SetSearchThresholdsInput{ProjectID: p.ID, ScannedChunks: 100}).(SearchThresholds)
	if set.ScannedChunks != 100 || set.ScannedChunksSource != "project" || set.VectorP95MS != 300 || set.VectorP95MSSource != "server" || set.UpdatedAt == nil {
		t.Fatalf("override: %+v", set)
	}

	// A vector search scanning more chunks than the override calls for a review.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO search_timings(project_id,source,mode,retrieval,total_ms,embed_ms,vector_ms,scanned_chunks,hits)
		VALUES($1,'search_knowledge','semantic','semantic',12,5,4,150,3)`, p.ID); err != nil {
		t.Fatal(err)
	}
	stats = mustCall(t, tools, editor, "get_usage_stats", UsageStatsInput{ProjectID: p.ID, Days: 1}).(UsageStats)
	r := stats.HNSWReview
	if r.Status != "review" || !r.ChunksExceeded || r.VectorExceeded || r.VectorSearches != 1 || r.MaxScannedChunks != 150 || r.P95VectorMS == nil || *r.P95VectorMS != 4 {
		t.Fatalf("review: %+v", r)
	}

	// Zero for both clears the override.
	cleared := mustCall(t, tools, owner, "set_search_thresholds", SetSearchThresholdsInput{ProjectID: p.ID}).(SearchThresholds)
	if cleared.ScannedChunksSource != "server" || cleared.ScannedChunks != 5000 || cleared.UpdatedAt != nil {
		t.Fatalf("cleared: %+v", cleared)
	}
}
