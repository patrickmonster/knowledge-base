package kb

import (
	"context"
	"testing"
)

func TestWikiSourcesAndSearch(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "wiki", Name: "Wiki"}).(Project)
	original := mustCall(t, tools, owner, "create_document", DocumentInput{
		ProjectID: p.ID, Title: "Original planning document", Kind: "brief", Content: "refundpolicy source text",
	}).(Document)
	article := mustCall(t, tools, owner, "create_document", DocumentInput{
		ProjectID: p.ID, Title: "Refund policy", Kind: "wiki", Status: "draft", Content: "refundpolicy wiki draft",
	}).(Document)
	link := LinkInput{ProjectID: p.ID, SourceID: article.ID, TargetID: original.ID, Relation: relationCites}
	mustCall(t, tools, owner, "link_documents", link)
	link.SourceID, link.TargetID = link.TargetID, link.SourceID
	if _, err := call(t, tools, owner, "link_documents", link); err == nil {
		t.Fatal("a source document should not cite a wiki article via cites")
	}

	wiki := "wiki"
	if _, err := call(t, tools, owner, "update_document", UpdateInput{
		ProjectID: p.ID, DocumentID: original.ID, ExpectedRevision: original.Revision,
		Reason: "Convert original", Patch: DocumentPatch{Kind: &wiki},
	}); err == nil {
		t.Fatal("changing the cited original into a wiki article should fail")
	}

	get := func(d Document, revision int) Document {
		t.Helper()
		return mustCall(t, tools, owner, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: d.ID, Revision: revision}).(Document)
	}
	if cites := get(article, 0).Links.Cites; len(cites) != 1 || cites[0].DocumentID != original.ID {
		t.Fatalf("wiki sources = %+v", cites)
	}
	if citedBy := get(original, 0).Links.CitedBy; len(citedBy) != 1 || citedBy[0].DocumentID != article.ID {
		t.Fatalf("source backlinks = %+v", citedBy)
	}
	search := func(kind string) []SearchHit {
		t.Helper()
		return mustCall(t, tools, owner, "search_knowledge", SearchInput{
			ProjectID: p.ID, Query: "refundpolicy", Kind: kind, Mode: "lexical",
		}).(SearchResult).Hits
	}
	if hits := search("wiki"); len(hits) != 0 {
		t.Fatalf("active wiki search returned a draft or source document: %+v", hits)
	}
	active, edited := "active", "refundpolicy updated wiki article"
	mustCall(t, tools, owner, "update_document", UpdateInput{
		ProjectID: p.ID, DocumentID: article.ID, ExpectedRevision: 1, Reason: "Policy verified",
		Patch: DocumentPatch{Content: &edited, Status: &active},
	})
	if got := get(article, 1); got.Content != article.Content {
		t.Fatalf("wiki revision 1 changed: %+v", got)
	}
	if got := get(original, 0); got.Revision != original.Revision || got.Content != original.Content {
		t.Fatalf("editing the wiki changed the original: %+v", got)
	}
	if hits := search("wiki"); len(hits) != 1 || hits[0].DocumentID != article.ID || hits[0].Revision != 2 {
		t.Fatalf("wiki search hits = %+v", hits)
	}
	if hits := search(""); len(hits) != 2 {
		t.Fatalf("unfiltered search should still return both documents: %+v", hits)
	}
	s.Embedder = &testEmbedder{model: "wiki-test"}
	if _, err := s.ProcessEmbeddingBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"semantic", "hybrid"} {
		found := mustCall(t, tools, owner, "search_knowledge", SearchInput{
			ProjectID: p.ID, Query: "refundpolicy", Kind: "wiki", Mode: mode,
		}).(SearchResult)
		if len(found.Hits) != 1 || found.Hits[0].DocumentID != article.ID || found.UnindexedChunks != 0 {
			t.Fatalf("%s wiki search = %+v", mode, found)
		}
	}

}
