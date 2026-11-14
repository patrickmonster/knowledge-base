package kb

import (
	"strings"
	"testing"
)

func TestIndexableTextDropsGeneratedTaskMetadata(t *testing.T) {
	history := "# 작업\n\n## 결과 요약\n\n환불 화면을 개선했다.\n\n## 근거와 출처\n\n문서 abc revision 2, kb://p/documents/abc?revision=2#chunk-0 참조\n\n" +
		"## 작업 연결 정보\n\n작업 ID: 11111111-2222-4333-8444-555555555555\n시작 시각: 2026-11-14T08:00:00Z\n\n시작 시 검색된 자료:\n- kb://p/documents/x?revision=1#chunk-1\n\n" +
		"## 함께 저장한 기획·결정\n\n- 환불 기한 결정 (decision, draft): kb://p/documents/y?revision=1#chunk-0\n"
	got := IndexableText("history", history)
	for _, noise := range []string{"작업 연결 정보", "11111111-2222", "시작 시각", "kb://"} {
		if strings.Contains(got, noise) {
			t.Fatalf("indexed generated metadata %q:\n%s", noise, got)
		}
	}
	for _, keep := range []string{"환불 화면을 개선했다", "## 근거와 출처", "문서 abc revision 2", "## 함께 저장한 기획·결정", "환불 기한 결정"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("dropped meaningful text %q:\n%s", keep, got)
		}
	}

	// Only history documents carry the generated block; other kinds keep a
	// same-named heading written by people.
	reference := "## 작업 연결 정보\n\n수동으로 작성한 연결 안내"
	if got := IndexableText("reference", reference); !strings.Contains(got, "수동으로 작성한 연결 안내") {
		t.Fatalf("non-history section removed: %s", got)
	}
	if got := IndexableText("brief", "본문 kb://p/documents/z 끝"); strings.Contains(got, "kb://") || !strings.Contains(got, "본문") {
		t.Fatalf("citation URL not stripped: %s", got)
	}
}
