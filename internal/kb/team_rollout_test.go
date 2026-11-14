package kb

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{"결제: 환불/정책 v2!": "결제-환불-정책-v2", "../../etc/passwd": "etc-passwd", "   ": "document", "a_b": "a_b"} {
		if got := safeFileName(in, 60); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := safeFileName(strings.Repeat("가", 100), 60); len([]rune(got)) != 60 {
		t.Fatalf("file name not capped: %d runes", len([]rune(got)))
	}
}

func TestRenderExportMarkdown(t *testing.T) {
	url := "https://example.com/x"
	md := renderExportMarkdown(ExportDocument{ID: "11111111-1111-1111-1111-111111111111", Title: `제목 "따옴표"`, Kind: "decision", Status: "active", Tags: []string{"a", "b"}, Revision: 2, SourceURL: &url, Content: "# 본문"})
	for _, want := range []string{"---\nid: \"11111111-1111-1111-1111-111111111111\"\n", `title: "제목 \"따옴표\""`, `tags: ["a","b"]`, "revision: 2\n", `source_url: "https://example.com/x"`, "---\n\n# 본문\n"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func sampleExport() ProjectExport {
	return ProjectExport{Format: exportFormat, Version: exportVersion, ExportedAt: "2026-11-14T00:00:00Z", Project: ExportedProject{ID: "p", Slug: "src", Name: "원본"},
		Documents: []ExportDocument{{ID: "22222222-2222-2222-2222-222222222222", Title: "문서", Kind: "brief", Status: "active", Content: "본문", Revisions: []ExportRevision{{Revision: 1}}}}}
}

func TestParseImport(t *testing.T) {
	if _, err := parseImport([]byte("not json"), false); errorCode(err) != "INVALID_INPUT" {
		t.Fatalf("invalid JSON accepted: %v", err)
	}
	if _, err := parseImport([]byte(`{"format":"other","version":1}`), false); errorCode(err) != "INVALID_INPUT" {
		t.Fatalf("foreign format accepted: %v", err)
	}
	raw, err := exportZip(sampleExport())
	if err != nil {
		t.Fatal(err)
	}
	data, err := parseImport(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Documents) != 1 || data.Documents[0].Title != "문서" || data.Documents[0].Revisions != nil {
		t.Fatalf("zip export.json round trip failed (revisions must be dropped): %+v", data.Documents)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["README.md"] || !names["export.json"] || !names["documents/brief/문서-22222222.md"] {
		t.Fatalf("unexpected zip entries: %v", names)
	}
	var empty bytes.Buffer
	zw := zip.NewWriter(&empty)
	zw.Create("README.md")
	zw.Close()
	if _, err := parseImport(empty.Bytes(), true); errorCode(err) != "INVALID_INPUT" {
		t.Fatalf("zip without export.json accepted: %v", err)
	}
}

func TestRenderAgentsTemplate(t *testing.T) {
	out, err := renderAgentsTemplate(Project{ID: "id", Slug: "billing", Name: "결제`팀 -->"}, "https://kb.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Content, "{{") || strings.Contains(out.Content, "knowledge-base") || !strings.Contains(out.Content, "slug가 `billing`인") {
		t.Fatalf("template placeholders not replaced:\n%s", out.Content)
	}
	if strings.Contains(out.Content, "결제`팀") || strings.Count(out.Content, "-->") != 1 || !strings.Contains(out.Content, "https://kb.example.com") {
		t.Fatal("project name can break Markdown code spans or the HTML comment")
	}
	if out.Filename != "AGENTS.md" || len(out.Notes) == 0 {
		t.Fatalf("unexpected template metadata: %+v", out)
	}
}

func TestTeamRolloutTools(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "rollout-editor@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "rollout", Name: "확산"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})

	// count_documents feeds the review badge.
	decision := mustCall(t, tools, editor, "create_document", DocumentInput{ProjectID: p.ID, Title: "결정 A", Kind: "decision", Content: "A 방식을 쓴다", Status: "draft"}).(Document)
	newer := mustCall(t, tools, editor, "create_document", DocumentInput{ProjectID: p.ID, Title: "결정 B", Kind: "decision", Content: "B 방식으로 바꾼다"}).(Document)
	history := mustCall(t, tools, editor, "create_document", DocumentInput{ProjectID: p.ID, Title: "작업 이력", Kind: "history", Content: "B 적용"}).(Document)
	mustCall(t, tools, editor, "link_documents", LinkInput{ProjectID: p.ID, SourceID: newer.ID, TargetID: decision.ID, Relation: relationSupersedes, Note: "교체"})
	mustCall(t, tools, editor, "link_documents", LinkInput{ProjectID: p.ID, SourceID: history.ID, TargetID: newer.ID, Relation: relationImplements})
	count := mustCall(t, tools, editor, "count_documents", CountInput{ProjectID: p.ID, Status: "draft"}).(map[string]any)
	if count["count"] != 1 {
		t.Fatalf("draft count = %v, want 1", count["count"])
	}
	_, err := call(t, tools, editor, "count_documents", CountInput{ProjectID: p.ID, Status: "pending"})
	denied(t, err, "INVALID_INPUT")

	// Work area cleanup: inactive owners are flagged; deletion moves or detaches issues.
	a1 := mustCall(t, tools, admin, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "배포", OwnerID: editor.User.ID}).(WorkArea)
	a2 := mustCall(t, tools, admin, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "인증", OwnerID: admin.User.ID}).(WorkArea)
	if !a1.OwnerActive {
		t.Fatal("active owner flagged as inactive")
	}
	q := mustCall(t, tools, admin, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "배포 순서", Body: "질문", AreaID: a1.ID}).(Issue)
	_, err = call(t, tools, editor, "delete_work_area", DeleteWorkAreaInput{ProjectID: p.ID, AreaID: a2.ID})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, admin, "delete_work_area", DeleteWorkAreaInput{ProjectID: p.ID, AreaID: a1.ID, MoveToAreaID: a1.ID})
	denied(t, err, "INVALID_INPUT")
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "viewer"})
	areas := mustCall(t, tools, admin, "list_work_areas", ProjectInput{ProjectID: p.ID}).([]WorkArea)
	for _, a := range areas {
		if a.ID == a1.ID && a.OwnerActive {
			t.Fatal("viewer owner not flagged for cleanup")
		}
	}
	res := mustCall(t, tools, admin, "delete_work_area", DeleteWorkAreaInput{ProjectID: p.ID, AreaID: a1.ID, MoveToAreaID: a2.ID}).(map[string]any)
	if res["moved_issues"] != int64(1) || res["detached_issues"] != int64(0) {
		t.Fatalf("unexpected delete result: %v", res)
	}
	moved := mustCall(t, tools, admin, "get_issue", IssueRef{ProjectID: p.ID, IssueID: q.ID}).(Issue)
	if moved.AreaID == nil || *moved.AreaID != a2.ID {
		t.Fatal("issue was not moved to the target area")
	}
	mustCall(t, tools, admin, "delete_work_area", DeleteWorkAreaInput{ProjectID: p.ID, AreaID: a2.ID})
	detached := mustCall(t, tools, admin, "get_issue", IssueRef{ProjectID: p.ID, IssueID: q.ID}).(Issue)
	if detached.AreaID != nil || detached.Status != "open" {
		t.Fatal("deleting an area must keep its issues without an area")
	}
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})

	// AGENTS.md uses the project's slug.
	agents := mustCall(t, tools, editor, "get_agents_template", ProjectInput{ProjectID: p.ID}).(AgentsTemplate)
	if !strings.Contains(agents.Content, "`rollout`") {
		t.Fatal("AGENTS.md does not reference the project slug")
	}

	// Export, then import into another project twice: the second run skips everything.
	export, err := s.ExportProject(ctx, editor, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Documents) != 3 || len(export.Links) != 2 || len(export.Issues) != 1 || len(export.Documents[0].Revisions) == 0 {
		t.Fatalf("incomplete export: %d docs, %d links, %d issues", len(export.Documents), len(export.Links), len(export.Issues))
	}
	raw, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	data, err := parseImport(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	target := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "rollout-copy", Name: "확산 사본"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: target.ID, UserID: editor.User.ID, Role: "editor"})
	if _, err := s.ImportProject(ctx, editor, target.ID, data); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("editor import: %v", err)
	}
	first, err := s.ImportProject(ctx, admin, target.ID, data)
	if err != nil {
		t.Fatal(err)
	}
	if first.DocumentsCreated != 3 || first.LinksCreated != 2 || first.IssuesIgnored != 1 || len(first.Warnings) == 0 {
		t.Fatalf("unexpected first import: %+v", first)
	}
	second, err := s.ImportProject(ctx, admin, target.ID, data)
	if err != nil {
		t.Fatal(err)
	}
	if second.DocumentsCreated != 0 || second.DocumentsSkipped != 3 || second.LinksCreated != 0 {
		t.Fatalf("re-import duplicated data: %+v", second)
	}
	// Importing into the source project itself recognises the originals.
	self, err := s.ImportProject(ctx, admin, p.ID, data)
	if err != nil || self.DocumentsCreated != 0 || self.DocumentsSkipped != 3 {
		t.Fatalf("self import: %+v %v", self, err)
	}
	docs := mustCall(t, tools, admin, "list_documents", ListInput{ProjectID: target.ID, Status: "draft"}).([]map[string]any)
	if len(docs) != 1 || docs[0]["title"] != "결정 A" {
		t.Fatalf("draft status not preserved: %v", docs)
	}
	var importedNewer string
	if err := s.DB.QueryRow(`SELECT id FROM documents WHERE project_id=$1 AND imported_from=$2`, target.ID, newer.ID).Scan(&importedNewer); err != nil {
		t.Fatal(err)
	}
	links := mustCall(t, tools, admin, "get_document_links", LinksQuery{ProjectID: target.ID, DocumentID: importedNewer}).(DocumentLinks)
	if len(links.Supersedes) != 1 || links.Supersedes[0].Note != "교체" || len(links.ImplementedBy) != 1 {
		t.Fatalf("links were not remapped: %+v", links)
	}
	bad := data
	bad.Documents = append([]ExportDocument{}, data.Documents...)
	bad.Documents[0].Kind = "unknown"
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "rollout-bad", Name: "실패"}).(Project)
	if _, err := s.ImportProject(ctx, admin, other.ID, bad); errorCode(err) != "INVALID_INPUT" {
		t.Fatalf("invalid document accepted: %v", err)
	}
	var leftovers int
	if err := s.DB.QueryRow(`SELECT count(*) FROM documents WHERE project_id=$1`, other.ID).Scan(&leftovers); err != nil || leftovers != 0 {
		t.Fatalf("failed import left %d documents (%v)", leftovers, err)
	}
}
