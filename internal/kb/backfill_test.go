package kb

import (
	"testing"

	"knowledge-base/migrations"
)

func TestBackfillUsedImplementsMigration(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "backfill", Name: "backfill"}).(Project)
	doc := func(kind, title string) Document {
		return mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: title, Kind: kind, Status: "active", Content: title + " 본문"}).(Document)
	}
	decision, brief, reference := doc("decision", "결정"), doc("brief", "기획"), doc("reference", "참고")
	finish := func(key string, used *[]string) string {
		run := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: key, Title: key, Task: key}).(StartTaskResult).Run
		c := mustCall(t, tools, admin, "finish_task", FinishTaskInput{ProjectID: p.ID, TaskID: run.ID, Outcome: "completed", Summary: "x", Changes: "x", Decisions: "x", Validation: "x", Remaining: "x", References: "x", UsedSources: used}).(TaskCompletion)
		return c.Documents[0].ID
	}
	withFeedback := finish("backfill-1", &[]string{decision.ID, brief.ID, reference.ID})
	noFeedback := finish("backfill-2", nil)
	// Simulate histories completed before finish_task linked used_sources.
	if _, err := s.DB.Exec(`DELETE FROM document_links WHERE source_id=ANY($1::uuid[])`, []string{withFeedback, noFeedback}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := s.DB.Exec(migrations.BackfillUsedImplements); err != nil {
			t.Fatal(err)
		}
	}
	links := mustCall(t, tools, admin, "get_document_links", LinksQuery{ProjectID: p.ID, DocumentID: withFeedback}).(DocumentLinks)
	got := map[string]string{}
	for _, d := range links.Implements {
		got[d.DocumentID] = d.Note
	}
	if len(got) != 2 || got[decision.ID] != "used_sources 소급 연결" || got[brief.ID] == "" {
		t.Fatalf("backfilled implements wrong: %+v", links.Implements)
	}
	if other := mustCall(t, tools, admin, "get_document_links", LinksQuery{ProjectID: p.ID, DocumentID: noFeedback}).(DocumentLinks); len(other.Implements) != 0 {
		t.Fatalf("history without feedback was linked: %+v", other.Implements)
	}
	if by := mustCall(t, tools, admin, "get_document_links", LinksQuery{ProjectID: p.ID, DocumentID: decision.ID}).(DocumentLinks); len(by.ImplementedBy) != 1 || by.ImplementedBy[0].DocumentID != withFeedback {
		t.Fatalf("reverse lookup wrong: %+v", by.ImplementedBy)
	}
}
