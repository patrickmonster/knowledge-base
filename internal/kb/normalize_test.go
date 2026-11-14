package kb

import (
	"reflect"
	"testing"

	"knowledge-base/migrations"
)

func TestNormalizeTags(t *testing.T) {
	got := normalizeTags([]string{"작업이력", " 검색 ", "", "  ", "작업이력", "MCP", "mcp", "검색"})
	want := []string{"작업이력", "검색", "MCP"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeTags = %q, want %q", got, want)
	}
	// Decomposed and composed Hangul are the same tag.
	if got := normalizeTags([]string{"가", "가"}); len(got) != 1 || got[0] != "가" {
		t.Fatalf("NFC duplicates kept: %q", got)
	}
	if got := normalizeTags(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil tags must become an empty list: %#v", got)
	}
}

func TestHistoryTitle(t *testing.T) {
	started := "2026-11-14T08:43:13.115759+00:00" // 17:43 in Asia/Seoul
	for _, c := range []struct{ in, want string }{
		{"2026-11-14 17:55 — 태그 정규화", "2026-11-14 17:55 — 태그 정규화"},
		{"2026-11-14 — Ollama 스레드 제한", "2026-11-14 17:43 — Ollama 스레드 제한"},
		{"2026-11-14 17:55 - 하이픈 구분", "2026-11-14 17:43 — 하이픈 구분"},
		{"날짜 없는 제목", "2026-11-14 17:43 — 날짜 없는 제목"},
		{"  앞뒤 공백  ", "2026-11-14 17:43 — 앞뒤 공백"},
	} {
		if got := historyTitle(c.in, started); got != c.want {
			t.Errorf("historyTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := historyTitle("시간 형식 오류", "not-a-time"); got != "시간 형식 오류" {
		t.Errorf("unparseable start time changed title: %q", got)
	}
	// Day boundary: 15:30 UTC is 00:30 the next day in Seoul.
	if got := historyTitle("자정 이후", "2026-11-14T15:30:00Z"); got != "2026-11-15 00:30 — 자정 이후" {
		t.Errorf("Seoul date not applied: %q", got)
	}
}

func TestNormalizeTagsMigrationCleansExistingDocuments(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "tags", Name: "tags"}).(Project)
	d := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "태그", Kind: "history", Content: "본문", Tags: []string{"작업이력", "작업이력", "MCP"}}).(Document)
	if !reflect.DeepEqual(d.Tags, []string{"작업이력", "MCP"}) {
		t.Fatalf("create_document kept duplicate tags: %q", d.Tags)
	}
	// Simulate a document saved before normalization existed.
	if _, err := s.DB.Exec(`UPDATE documents SET tags=$2 WHERE id=$1`, d.ID, []string{"작업이력", "task:1", " 작업이력 ", "", "mcp", "MCP"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(migrations.NormalizeTags); err != nil {
		t.Fatal(err)
	}
	got := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: d.ID}).(Document)
	if !reflect.DeepEqual(got.Tags, []string{"작업이력", "task:1", "mcp"}) || got.Revision != d.Revision {
		t.Fatalf("migration result: tags=%q revision=%d", got.Tags, got.Revision)
	}
}
