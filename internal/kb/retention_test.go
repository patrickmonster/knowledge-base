package kb

import (
	"context"
	"testing"
)

func TestParseRetention(t *testing.T) {
	p, err := ParseRetention("", "", "")
	if err != nil || p.AuditDays != 0 || p.RevisionDays != 0 || p.RevisionKeepLatest != defaultRevisionKeep {
		t.Fatalf("defaults keep everything: %+v %v", p, err)
	}
	if p, err = ParseRetention("365", "180", "5"); err != nil || p.AuditDays != 365 || p.RevisionDays != 180 || p.RevisionKeepLatest != 5 {
		t.Fatalf("parsed: %+v %v", p, err)
	}
	for _, bad := range [][3]string{{"30", "", ""}, {"", "7", ""}, {"x", "", ""}, {"", "", "0"}, {"4000", "", ""}} {
		if _, err := ParseRetention(bad[0], bad[1], bad[2]); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

func TestApplyRetention(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "retention", Name: "retention"}).(Project)
	d := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "v", Kind: "reference", Content: "v1"}).(Document)
	for i := 2; i <= 6; i++ {
		content := "v" + string(rune('0'+i))
		d = mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: d.Revision, Reason: "edit", Patch: DocumentPatch{Content: &content}}).(Document)
	}
	// Revision 2 is pinned by an attachment record.
	if _, err := s.DB.Exec(`INSERT INTO attachments(id,project_id,document_id,document_revision,filename,size_bytes,object_key,created_by,status)
		VALUES(gen_random_uuid(),$1,$2,2,'pinned.txt',1,'attachments/pinned-'||gen_random_uuid()::text,$3,'ready')`, p.ID, d.ID, admin.User.ID); err != nil {
		t.Fatal(err)
	}
	// Age everything, then add one recent audit event that must survive.
	if _, err := s.DB.Exec(`UPDATE revisions SET created_at=now()-interval '400 days' WHERE document_id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE audit_events SET created_at=now()-interval '400 days'`); err != nil {
		t.Fatal(err)
	}
	mustCall(t, tools, admin, "whoami", struct{}{})

	// Default policy deletes nothing.
	if out, err := s.ApplyRetention(ctx); err != nil || out["revisions"] != 0 || out["audit_events"] != 0 {
		t.Fatalf("disabled policy: %v %v", out, err)
	}
	s.Retention = RetentionPolicy{AuditDays: 365, RevisionDays: 365, RevisionKeepLatest: 2}
	out, err := s.ApplyRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Revisions 1..6: keep 5,6 (latest two) and 2 (attachment) → delete 1,3,4.
	if out["revisions"] != 3 || out["audit_events"] == 0 {
		t.Fatalf("purge counts: %v", out)
	}
	rows, err := s.DB.Query(`SELECT revision FROM revisions WHERE document_id=$1 ORDER BY revision`, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept := []int{}
	for rows.Next() {
		var n int
		rows.Scan(&n)
		kept = append(kept, n)
	}
	rows.Close()
	if len(kept) != 3 || kept[0] != 2 || kept[1] != 5 || kept[2] != 6 {
		t.Fatalf("kept revisions: %v", kept)
	}
	// The current document is still readable; a purged revision is gone.
	mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: d.ID})
	_, err = call(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: d.ID, Revision: 1})
	denied(t, err, "NOT_FOUND")

	var old, recent, purges int
	s.DB.QueryRow(`SELECT count(*) FILTER(WHERE created_at<now()-interval '365 days'),count(*) FILTER(WHERE action='whoami'),count(*) FILTER(WHERE action='retention_purge') FROM audit_events`).Scan(&old, &recent, &purges)
	if old != 0 || recent == 0 || purges != 1 {
		t.Fatalf("audit after purge: old=%d recent=%d purges=%d", old, recent, purges)
	}
	status := mustValue(t)(s.RetentionStatus(ctx, admin)).(map[string]any)
	if status["policy"].(RetentionPolicy).AuditDays != 365 || status["last_purge"] == nil {
		t.Fatalf("status: %v", status)
	}
}

func mustValue(t *testing.T) func(any, error) any {
	return func(v any, err error) any {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func intp(n int) *int { return &n }

func TestSetRetention(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "member@example.com")
	s.Retention = RetentionPolicy{AuditDays: 365, RevisionKeepLatest: 7}

	// Nothing saved: the server (environment) defaults apply.
	p, src, err := s.effectiveRetention(ctx, s.DB)
	if err != nil || p.AuditDays != 365 || p.RevisionKeepLatest != 7 || src["audit_days"] != "server" {
		t.Fatalf("defaults: %+v %v %v", p, src, err)
	}
	// Only a global administrator may change it, and values are validated.
	if _, err := s.SetRetention(ctx, member, RetentionInput{AuditDays: intp(0)}); err == nil {
		t.Fatal("member changed retention")
	}
	for _, bad := range []RetentionInput{{AuditDays: intp(30)}, {RevisionDays: intp(7)}, {AuditDays: intp(4000)}, {RevisionKeepLatest: intp(0)}} {
		if _, err := s.SetRetention(ctx, admin, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	// An administrator value wins per field; the others keep the default.
	if _, err := s.SetRetention(ctx, admin, RetentionInput{AuditDays: intp(0), RevisionDays: intp(90)}); err != nil {
		t.Fatal(err)
	}
	p, src, err = s.effectiveRetention(ctx, s.DB)
	if err != nil || p.AuditDays != 0 || p.RevisionDays != 90 || p.RevisionKeepLatest != 7 ||
		src["audit_days"] != "admin" || src["revision_days"] != "admin" || src["revision_keep_latest"] != "server" {
		t.Fatalf("saved: %+v %v %v", p, src, err)
	}
	status := mustValue(t)(s.RetentionStatus(ctx, admin)).(map[string]any)
	if status["policy"].(RetentionPolicy).RevisionDays != 90 || status["updated_at"] == nil {
		t.Fatalf("status: %v", status)
	}
	var audited int
	s.DB.QueryRow(`SELECT count(*) FROM audit_events WHERE action='update_retention' AND details->>'revision_days'='90'`).Scan(&audited)
	if audited != 1 {
		t.Fatalf("audit event count %d", audited)
	}
	// All null goes back to the server defaults.
	if _, err := s.SetRetention(ctx, admin, RetentionInput{}); err != nil {
		t.Fatal(err)
	}
	if p, _, _ = s.effectiveRetention(ctx, s.DB); p.AuditDays != 365 || p.RevisionDays != 0 {
		t.Fatalf("reset: %+v", p)
	}
}
