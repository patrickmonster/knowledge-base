package kb

import (
	"context"
	"testing"
)

func TestSearchAcrossProjects(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "cross-member@example.com")
	first := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "cross-a", Name: "횡단 A"}).(Project)
	second := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "cross-b", Name: "횡단 B"}).(Project)
	hidden := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "cross-hidden", Name: "비공개"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: first.ID, UserID: member.User.ID, Role: "viewer"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: second.ID, UserID: member.User.ID, Role: "editor"})
	a := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: first.ID, Title: "배포 절차 A", Kind: "reference", Content: "배포 전에 마이그레이션을 확인합니다."}).(Document)
	b := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: second.ID, Title: "배포 절차 B", Kind: "reference", Content: "배포 후 상태를 점검합니다."}).(Document)
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: hidden.ID, Title: "배포 비밀", Kind: "reference", Content: "비공개 배포 정보"})

	found := func(r CrossSearchResult) map[string]SearchHit {
		out := map[string]SearchHit{}
		for _, h := range r.Hits {
			out[h.DocumentID] = h
		}
		return out
	}

	// Default scope: every project the caller belongs to, never others.
	all := mustCall(t, tools, member, "search_across_projects", CrossSearchInput{Query: "배포", Mode: "lexical"}).(CrossSearchResult)
	hits := found(all)
	if len(all.Projects) != 2 || len(all.Hits) != 2 || all.Retrieval != "lexical" {
		t.Fatalf("cross search scope: %+v", all)
	}
	if h, ok := hits[a.ID]; !ok || h.ProjectID != first.ID || h.ProjectSlug != "cross-a" || h.ProjectName != "횡단 A" {
		t.Fatalf("hit from first project missing or unlabeled: %+v", h)
	}
	if h, ok := hits[b.ID]; !ok || h.ProjectSlug != "cross-b" || h.Citation == "" {
		t.Fatalf("hit from second project missing: %+v", h)
	}

	// Explicit subset.
	only := mustCall(t, tools, member, "search_across_projects", CrossSearchInput{ProjectIDs: []string{second.ID}, Query: "배포", Mode: "lexical"}).(CrossSearchResult)
	if len(only.Projects) != 1 || len(only.Hits) != 1 || only.Hits[0].DocumentID != b.ID {
		t.Fatalf("subset search: %+v", only)
	}

	// A project without membership fails like single-project search.
	_, err := call(t, tools, member, "search_across_projects", CrossSearchInput{ProjectIDs: []string{first.ID, hidden.ID}, Query: "배포", Mode: "lexical"})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, member, "search_across_projects", CrossSearchInput{ProjectIDs: []string{"not-a-uuid"}, Query: "배포"})
	denied(t, err, "INVALID_INPUT")
	_, err = call(t, tools, member, "search_across_projects", CrossSearchInput{Query: ""})
	denied(t, err, "INVALID_INPUT")

	// A project-scoped key only reaches its own project.
	keyOut, err := s.CreateKey(ctx, member, KeyInput{Name: "cross-scoped", Access: "read_only", ProjectID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	scoped := mustCall(t, tools, key, "search_across_projects", CrossSearchInput{Query: "배포", Mode: "lexical"}).(CrossSearchResult)
	if len(scoped.Projects) != 1 || scoped.Projects[0].ID != first.ID || len(scoped.Hits) != 1 || scoped.Hits[0].DocumentID != a.ID {
		t.Fatalf("scoped key search: %+v", scoped)
	}
	_, err = call(t, tools, key, "search_across_projects", CrossSearchInput{ProjectIDs: []string{second.ID}, Query: "배포", Mode: "lexical"})
	denied(t, err, "FORBIDDEN")

	// Semantic and hybrid paths embed the query once for all projects.
	e := &testEmbedder{model: "test-local"}
	s.Embedder = e
	if _, err := s.ProcessEmbeddingBatch(ctx); err != nil {
		t.Fatal(err)
	}
	before := e.calls.Load()
	hybrid := mustCall(t, tools, member, "search_across_projects", CrossSearchInput{Query: "배포"}).(CrossSearchResult)
	if hybrid.Retrieval != "hybrid" || len(hybrid.Hits) != 2 || e.calls.Load()-before != 1 {
		t.Fatalf("hybrid cross search: %+v calls=%d", hybrid, e.calls.Load()-before)
	}
	for _, h := range hybrid.Hits {
		if h.ProjectID == hidden.ID {
			t.Fatal("hybrid search leaked a non-member project")
		}
	}
}
