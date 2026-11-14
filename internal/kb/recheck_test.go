package kb

import (
	"testing"
	"time"
)

func TestRecheckFor(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -120).Format("2006-01-02T15:04:05.000000-07:00")
	edge := now.AddDate(0, 0, -DefaultStaleDays).Add(time.Hour).Format(time.RFC3339)
	for _, c := range []struct {
		name, kind, status, updated string
		want                        bool
	}{
		{"old reference", "reference", "active", old, true},
		{"old brief", "brief", "active", old, true},
		{"decision is a point-in-time record", "decision", "active", old, false},
		{"history is a point-in-time record", "history", "active", old, false},
		{"draft is not relied upon", "reference", "draft", old, false},
		{"archived is not relied upon", "brief", "archived", old, false},
		{"just inside the threshold", "reference", "active", edge, false},
		{"unparseable timestamp", "reference", "active", "yesterday", false},
	} {
		got := recheckFor(c.kind, c.status, c.updated, now)
		if (got != nil) != c.want {
			t.Fatalf("%s: got %+v, want needs_recheck=%v", c.name, got, c.want)
		}
		if got != nil && (!got.NeedsRecheck || got.DaysSinceUpdate != 120 || got.StaleAfterDays != DefaultStaleDays || got.Message == "") {
			t.Fatalf("%s: incomplete warning %+v", c.name, got)
		}
	}
	hits := []SearchHit{{Kind: "reference", Status: "active", UpdatedAt: old}, {Kind: "reference", Status: "active", UpdatedAt: now.Format(time.RFC3339)}}
	markRecheck(hits, now)
	if hits[0].Recheck == nil || hits[1].Recheck != nil {
		t.Fatalf("markRecheck: %+v", hits)
	}
}

func TestRecheckWarningsInGetAndSearch(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "recheck", Name: "recheck"}).(Project)
	ref := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "배포 절차 참고", Kind: "reference", Content: "배포 절차 롤백 방법"}).(Document)
	fresh := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "배포 절차 최신", Kind: "reference", Content: "배포 절차 새 방법"}).(Document)
	if _, err := s.DB.Exec(`UPDATE documents SET updated_at=now()-interval '120 days' WHERE id=$1`, ref.ID); err != nil {
		t.Fatal(err)
	}

	got := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: ref.ID}).(Document)
	if got.Recheck == nil || !got.Recheck.NeedsRecheck || got.Recheck.DaysSinceUpdate < 119 {
		t.Fatalf("get_document should warn: %+v", got.Recheck)
	}
	if d := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: fresh.ID}).(Document); d.Recheck != nil {
		t.Fatalf("fresh document warned: %+v", d.Recheck)
	}
	// A past revision is a historical snapshot; no current-state warning is attached.
	if d := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: ref.ID, Revision: 1}).(Document); d.Recheck != nil {
		t.Fatalf("old revision warned: %+v", d.Recheck)
	}

	hits := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "배포 절차", Mode: "lexical"}).(SearchResult).Hits
	seen := map[string]bool{}
	for _, h := range hits {
		seen[h.DocumentID] = true
		if (h.Recheck != nil) != (h.DocumentID == ref.ID) {
			t.Fatalf("search recheck for %q: %+v", h.Title, h.Recheck)
		}
	}
	if !seen[ref.ID] || !seen[fresh.ID] {
		t.Fatalf("both documents should match: %+v", hits)
	}

	// Refreshing the reference clears the warning everywhere.
	body := "배포 절차 롤백 방법 (재확인)"
	mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: ref.ID, ExpectedRevision: ref.Revision, Reason: "재확인", Patch: DocumentPatch{Content: &body}})
	if d := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: ref.ID}).(Document); d.Recheck != nil {
		t.Fatalf("refreshed document still warned: %+v", d.Recheck)
	}
}
