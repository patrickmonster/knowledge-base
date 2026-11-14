package kb

import (
	"testing"
	"time"
)

func TestParseListTime(t *testing.T) {
	start, err := parseListTime("updated_after", "2026-11-01", false)
	if err != nil || !start.Equal(time.Date(2026, 10, 31, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("date lower bound should be Seoul midnight: %v %v", start, err)
	}
	end, err := parseListTime("updated_before", "2026-11-01", true)
	if err != nil || !end.Equal(time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("date upper bound should include the whole Seoul day: %v %v", end, err)
	}
	exact, err := parseListTime("updated_before", "2026-11-01T03:00:00Z", true)
	if err != nil || !exact.Equal(time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC3339 is used as given: %v %v", exact, err)
	}
	if empty, err := parseListTime("updated_after", "  ", false); empty != nil || err != nil {
		t.Fatalf("blank means no bound: %v %v", empty, err)
	}
	if _, err := parseListTime("updated_after", "11/01/2026", false); err == nil {
		t.Fatal("unsupported date format accepted")
	}
}

func TestListOrder(t *testing.T) {
	cases := []struct{ sort, order, want string }{
		{"", "", "l.updated_at DESC,l.id"},
		{"updated_at", "asc", "l.updated_at ASC,l.id"},
		{"title", "", "lower(l.title) ASC,l.updated_at DESC,l.id"},
		{"revision", "", "l.revision DESC,l.updated_at DESC,l.id"},
		{"kind", "desc", "l.kind DESC,l.updated_at DESC,l.id"},
	}
	for _, c := range cases {
		got, err := listOrder(c.sort, c.order)
		if err != nil || got != c.want {
			t.Fatalf("listOrder(%q,%q)=%q,%v want %q", c.sort, c.order, got, err, c.want)
		}
	}
	if _, err := listOrder("content; DROP TABLE documents", ""); err == nil {
		t.Fatal("unknown sort key accepted")
	}
	if _, err := listOrder("title", "sideways"); err == nil {
		t.Fatal("unknown order accepted")
	}
}

func TestListDocumentFilters(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "filter-editor@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "filters", Name: "filters"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	create := func(who Identity, title, kind string, tags ...string) Document {
		return mustCall(t, tools, who, "create_document", DocumentInput{ProjectID: p.ID, Title: title, Kind: kind, Content: title + " 본문", Tags: tags}).(Document)
	}
	oldRef := create(admin, "오래된 참고", "reference", "Railway", "운영")
	oldBrief := create(editor, "오래된 개요", "brief", "운영")
	oldDecision := create(admin, "오래된 결정", "decision", "운영")
	fresh := create(editor, "최근 참고", "reference", "railway")
	// Age three documents; decisions are point-in-time records and never stale.
	if _, err := s.DB.Exec(`UPDATE documents SET updated_at=now()-interval '120 days' WHERE id IN ($1,$2,$3)`, oldRef.ID, oldBrief.ID, oldDecision.ID); err != nil {
		t.Fatal(err)
	}
	list := func(in ListInput) map[string]map[string]any {
		t.Helper()
		in.ProjectID = p.ID
		out := map[string]map[string]any{}
		for _, d := range mustCall(t, tools, admin, "list_documents", in).([]map[string]any) {
			if _, ok := d["content"]; ok {
				t.Fatal("list must not return content")
			}
			out[d["id"].(string)] = d
		}
		return out
	}
	expect := func(name string, got map[string]map[string]any, want ...Document) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %d documents, want %d: %v", name, len(got), len(want), got)
		}
		for _, d := range want {
			if got[d.ID] == nil {
				t.Fatalf("%s: missing %q", name, d.Title)
			}
		}
	}

	all := list(ListInput{})
	expect("all", all, oldRef, oldBrief, oldDecision, fresh)
	for id, want := range map[string]bool{oldRef.ID: true, oldBrief.ID: true, oldDecision.ID: false, fresh.ID: false} {
		if all[id]["needs_recheck"] != want {
			t.Fatalf("needs_recheck for %q = %v, want %v", all[id]["title"], all[id]["needs_recheck"], want)
		}
	}
	if days := all[oldRef.ID]["days_since_update"].(float64); days < 119 || days > 121 {
		t.Fatalf("days_since_update = %v", days)
	}
	if all[fresh.ID]["stale_after_days"].(float64) != DefaultStaleDays {
		t.Fatalf("stale_after_days = %v", all[fresh.ID]["stale_after_days"])
	}

	expect("tag case-insensitive", list(ListInput{Tags: []string{"RAILWAY"}}), oldRef, fresh)
	expect("all tags must match", list(ListInput{Tags: []string{"railway", "운영"}}), oldRef)
	expect("author creator or editor", list(ListInput{Author: editor.User.ID}), oldBrief, fresh)
	expect("updated after", list(ListInput{UpdatedAfter: time.Now().AddDate(0, 0, -30).Format(time.RFC3339)}), fresh)
	expect("updated before", list(ListInput{UpdatedBefore: time.Now().AddDate(0, 0, -30).Format("2006-01-02")}), oldRef, oldBrief, oldDecision)
	expect("stale only", list(ListInput{StaleOnly: true}), oldRef, oldBrief)
	expect("stale only with kind", list(ListInput{StaleOnly: true, Kind: "brief"}), oldBrief)
	expect("custom threshold", list(ListInput{StaleOnly: true, StaleDays: 200}))

	// The editor updates the old reference: it becomes fresh and they become its author.
	checked := "오래된 참고 본문 (재확인함)"
	mustCall(t, tools, editor, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: oldRef.ID, ExpectedRevision: oldRef.Revision, Reason: "재확인", Patch: DocumentPatch{Content: &checked}})
	expect("recheck clears after update", list(ListInput{StaleOnly: true}), oldBrief)
	expect("last editor counts as author", list(ListInput{Author: editor.User.ID}), oldRef, oldBrief, fresh)

	// Archived documents are not marked: only active knowledge is relied upon.
	if _, err := s.DB.Exec(`UPDATE documents SET status='archived' WHERE id=$1`, oldBrief.ID); err != nil {
		t.Fatal(err)
	}
	expect("archived not stale", list(ListInput{StaleOnly: true}))

	for name, bad := range map[string]ListInput{
		"bad author":    {Author: "someone"},
		"bad date":      {UpdatedAfter: "yesterday"},
		"empty range":   {UpdatedAfter: "2026-11-02", UpdatedBefore: "2026-11-01"},
		"stale days":    {StaleDays: 5000},
		"too many tags": {Tags: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}},
	} {
		bad.ProjectID = p.ID
		_, err := call(t, tools, admin, "list_documents", bad)
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
		denied(t, err, "INVALID_INPUT")
	}
}
