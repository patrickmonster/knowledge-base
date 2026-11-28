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

func TestWikiWorkAreas(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "wiki-areas", Name: "Wiki areas"}).(Project)
	other := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "wiki-other", Name: "Other"}).(Project)
	auth := mustCall(t, tools, owner, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "인증", OwnerID: owner.User.ID}).(WorkArea)
	deploy := mustCall(t, tools, owner, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "배포", OwnerID: owner.User.ID}).(WorkArea)
	foreign := mustCall(t, tools, owner, "set_work_area", WorkAreaInput{ProjectID: other.ID, Name: "인증", OwnerID: owner.User.ID}).(WorkArea)

	if _, err := call(t, tools, owner, "create_document", DocumentInput{
		ProjectID: p.ID, Title: "Foreign", Kind: "wiki", Content: "x", AreaID: foreign.ID,
	}); err == nil {
		t.Fatal("a work area of another project should be rejected")
	}
	login := mustCall(t, tools, owner, "create_document", DocumentInput{
		ProjectID: p.ID, Title: "Login", Kind: "wiki", Content: "## Flow\nlogin", AreaID: auth.ID,
	}).(Document)
	if login.AreaID == nil || *login.AreaID != auth.ID {
		t.Fatalf("created area = %v", login.AreaID)
	}
	plain := mustCall(t, tools, owner, "create_document", DocumentInput{
		ProjectID: p.ID, Title: "Glossary", Kind: "wiki", Content: "terms",
	}).(Document)
	if plain.AreaID != nil {
		t.Fatalf("document without area got %v", *plain.AreaID)
	}

	list := func(area string) []string {
		t.Helper()
		rows := mustCall(t, tools, owner, "list_documents", ListInput{ProjectID: p.ID, Kind: "wiki", AreaID: area, Sort: "title"}).([]map[string]any)
		titles := []string{}
		for _, row := range rows {
			titles = append(titles, row["title"].(string))
		}
		return titles
	}
	if got := list(auth.ID); len(got) != 1 || got[0] != "Login" {
		t.Fatalf("area filter = %v", got)
	}
	if got := list("none"); len(got) != 1 || got[0] != "Glossary" {
		t.Fatalf("none filter = %v", got)
	}
	if _, err := call(t, tools, owner, "list_documents", ListInput{ProjectID: p.ID, AreaID: "bad"}); err == nil {
		t.Fatal("an invalid area filter should be rejected")
	}

	// Moving to another area keeps the content; an empty area clears it.
	moved := mustCall(t, tools, owner, "update_document", UpdateInput{
		ProjectID: p.ID, DocumentID: login.ID, ExpectedRevision: 1, Reason: "Move area", Patch: DocumentPatch{AreaID: &deploy.ID},
	}).(Document)
	if moved.AreaID == nil || *moved.AreaID != deploy.ID || moved.Content != login.Content {
		t.Fatalf("moved = %+v", moved)
	}
	// A section edit leaves the area alone.
	edited := mustCall(t, tools, owner, "update_document", UpdateInput{
		ProjectID: p.ID, DocumentID: login.ID, ExpectedRevision: 2, Reason: "섹션 'Flow' 수정",
		Edits: []DocumentEdit{{OldStr: "## Flow\nlogin", NewStr: "## Flow\nlogin with MFA"}},
	}).(EditedDocument)
	if edited.AreaID == nil || *edited.AreaID != deploy.ID || edited.Content != "## Flow\nlogin with MFA" {
		t.Fatalf("section edit = %+v", edited.Document)
	}
	if _, err := call(t, tools, owner, "update_document", UpdateInput{
		ProjectID: p.ID, DocumentID: login.ID, ExpectedRevision: 3, Reason: "Bad", Patch: DocumentPatch{AreaID: &foreign.ID},
	}); err == nil {
		t.Fatal("moving to another project's area should fail")
	}

	// Deleting the area keeps the article without an area.
	mustCall(t, tools, owner, "delete_work_area", DeleteWorkAreaInput{ProjectID: p.ID, AreaID: deploy.ID})
	after := mustCall(t, tools, owner, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: login.ID}).(Document)
	if after.AreaID != nil {
		t.Fatalf("deleted area still set: %v", *after.AreaID)
	}
	empty := ""
	mustCall(t, tools, owner, "update_document", UpdateInput{
		ProjectID: p.ID, DocumentID: plain.ID, ExpectedRevision: 1, Reason: "Clear", Patch: DocumentPatch{AreaID: &empty},
	})
	if got := list("none"); len(got) != 2 {
		t.Fatalf("articles without area = %v", got)
	}
}
