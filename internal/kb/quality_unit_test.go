package kb

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseSynonyms(t *testing.T) {
	content := "# 결제\n\n## 정의\n\n돈을 받는 흐름. 동의어가 아닌 문장: 이건 무시\n\n## 동의어\n\npayment, PG, 페이먼트、결제\n- checkout\n\n## 사용 맥락과 예시\n\n결제 화면"
	got := parseSynonyms("결제", content)
	want := []string{"payment", "PG", "페이먼트", "checkout"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("synonyms: got %v want %v", got, want)
	}
	if got := parseSynonyms("배포", "배포 절차.\n동의어: deploy, 릴리스\n별칭: 없음"); !reflect.DeepEqual(got, []string{"deploy", "릴리스"}) {
		t.Fatalf("inline synonyms: %v", got)
	}
	if got := parseSynonyms("x", "## 동의어\n\n없음"); len(got) != 0 {
		t.Fatalf("없음 should be empty: %v", got)
	}
	if glossaryTerm("용어: 결제") != "결제" || glossaryTerm("API 키") != "API 키" {
		t.Fatal("glossary title prefix not removed")
	}
}

func TestExpandWithGlossary(t *testing.T) {
	groups := [][]string{{"결제", "payment", "PG"}, {"배포", "deploy"}, {"CI", "지속 통합"}}
	if got := expandWithGlossary("결제를 취소하는 방법", groups); !reflect.DeepEqual(got, []string{"payment", "PG"}) {
		t.Fatalf("korean particle match: %v", got)
	}
	if got := expandWithGlossary("PG timeout", groups); !reflect.DeepEqual(got, []string{"결제", "payment"}) {
		t.Fatalf("synonym match: %v", got)
	}
	// Latin terms must stand alone: "upgrade" does not mention "pg", "CICD" not "ci".
	if got := expandWithGlossary("upgrade CICD pipeline", groups); len(got) != 0 {
		t.Fatalf("substring of a word matched: %v", got)
	}
	// "pg" inside "upgrade" is not the term PG, so PG is still added.
	if got := expandWithGlossary("결제 upgrade", groups); !reflect.DeepEqual(got, []string{"payment", "PG"}) {
		t.Fatalf("dedupe ignored word boundaries: %v", got)
	}
	// A Korean particle may follow a Latin term directly.
	if got := expandWithGlossary("PG를 바꾸는 방법", groups); !reflect.DeepEqual(got, []string{"결제", "payment"}) {
		t.Fatalf("latin term with korean particle: %v", got)
	}
	runs := [][]string{{"작업 실행", "task run", "task_run"}}
	if got := expandWithGlossary("task run이 뭐야", runs); !reflect.DeepEqual(got, []string{"작업 실행", "task_run"}) {
		t.Fatalf("multi-word latin term with particle: %v", got)
	}
	// Hangul before a Latin term is still not a boundary, and Latin letters after it never are.
	if got := expandWithGlossary("새PG 또는 PGs", groups); len(got) != 0 {
		t.Fatalf("latin term inside a word matched: %v", got)
	}
	if got := expandWithGlossary("배포 deploy", groups); len(got) != 0 {
		t.Fatalf("terms already in the query were added: %v", got)
	}
	many := [][]string{{"a1"}}
	for i := 0; i < 30; i++ {
		many[0] = append(many[0], "term"+strings.Repeat("x", i+1))
	}
	if got := expandWithGlossary("a1", many); len(got) != maxExpandedTerms {
		t.Fatalf("expansion not bounded: %d", len(got))
	}
}

func TestDecisionSimilarity(t *testing.T) {
	same, shared := decisionSimilarity("환불 기한 7일", "환불 기한은 결제 후 7일이다. 환불 기한 정책.", "환불 기한 14일", "환불 기한은 결제 후 14일로 늘린다. 환불 기한 정책.")
	other, _ := decisionSimilarity("환불 기한 7일", "환불 기한은 결제 후 7일이다.", "배포는 Railway 사용", "운영 배포는 Railway production 하나로 한다.")
	if same < supersedeThreshold || other >= similarDecisionThreshold {
		t.Fatalf("similar=%.3f other=%.3f", same, other)
	}
	if len(shared) == 0 || shared[0] == "" {
		t.Fatalf("shared terms missing: %v", shared)
	}
	// Template headings alone must not make decisions similar.
	a := "## 결정 배경\n\n로그 보관\n\n## 결정 내용\n\n로그는 30일 보관"
	b := "## 결정 배경\n\n결제 수단\n\n## 결정 내용\n\n카드 결제만 지원"
	if score, _ := decisionSimilarity("로그 보관 기간", a, "결제 수단", b); score >= similarDecisionThreshold {
		t.Fatalf("template headings inflated similarity: %.3f", score)
	}
}

func TestExtractDecisionStatements(t *testing.T) {
	if got := extractDecisionStatements("없음", 5); len(got) != 0 {
		t.Fatalf("없음 produced candidates: %v", got)
	}
	text := "- 첨부는 Railway 버킷에 저장하기로 함. 이유: AWS 계정을 만들지 않기 위해\n- 테스트를 실행했다\n- 검색 결과에서 대체된 문서는 숨기지 않고 뒤로 보낸다 — 근거 추적을 위해\n  (이어지는 설명)\n\n기존 결정을 유지한다. 오늘 회의를 했다."
	got := extractDecisionStatements(text, 5)
	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %+v", got)
	}
	if got[0].Statement != "첨부는 Railway 버킷에 저장하기로 함." || got[0].Reason != "AWS 계정을 만들지 않기 위해" {
		t.Fatalf("first candidate: %+v", got[0])
	}
	if got[1].Statement != "기존 결정을 유지한다." {
		t.Fatalf("sentence candidate: %+v", got[1])
	}
	if title := decisionTitle(strings.Repeat("가", 100) + "."); !strings.HasPrefix(title, "결정: ") || len([]rune(title)) != len([]rune("결정: "))+81 {
		t.Fatalf("title not bounded: %q", title)
	}
}

func TestRenderRecheckDigest(t *testing.T) {
	owner, name := "u1", "홍길동"
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	d := RecheckDigest{ByOwner: []OwnerDigest{{OwnerID: owner, OwnerName: name, Total: 2, Due: []RecheckDue{{Title: "운영 절차", Kind: "reference", DaysOverdue: 3, Note: "배포 명령", Citation: "kb://p/documents/d?revision=2"}}}}}
	text := renderRecheckDigest(d, now)
	for _, want := range []string{"## 재확인 요약 (2026-11-14)", "### 홍길동 (2건)", "- 운영 절차 (reference) — 기한 3일 지남 · 확인할 것: 배포 명령", "- 외 1건"} {
		if !strings.Contains(text, want) {
			t.Fatalf("digest missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(renderRecheckDigest(RecheckDigest{}, now), "재확인할 문서가 없습니다") {
		t.Fatal("empty digest message missing")
	}
}
