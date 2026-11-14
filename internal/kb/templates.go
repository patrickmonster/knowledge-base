package kb

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
)

//go:embed templates/*.md
var templateFiles embed.FS

type TemplateSection struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Help  string `json:"help"`
}
type DocumentTemplate struct {
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Version  int               `json:"version"`
	Sections []TemplateSection `json:"required_sections"`
	Markdown string            `json:"markdown"`
}

func documentTemplates() []DocumentTemplate {
	items := []DocumentTemplate{
		{Kind: "brief", Name: "기획", Sections: []TemplateSection{
			{"purpose", "배경과 목적", "해결할 문제와 필요한 이유"},
			{"users", "대상 사용자", "사용자와 사용 상황"},
			{"scope", "범위와 제외 범위", "이번 작업에 포함할 것과 제외할 것"},
			{"requirements", "요구사항", "구현하거나 충족할 구체적인 동작"},
			{"acceptance", "완료 기준", "성공 여부를 확인할 관찰 가능한 기준"},
			{"risks", "제약과 미확정 사항", "의존성, 제약, 확인할 질문. 없으면 없음으로 명시"},
			{"references", "근거와 출처", "확인한 문서 ID·버전, 링크. 없으면 없음으로 명시"},
		}},
		{Kind: "decision", Name: "결정", Sections: []TemplateSection{
			{"context", "결정 배경", "결정이 필요한 문제와 조건"},
			{"decision", "결정 내용", "채택한 방안과 적용 범위"},
			{"alternatives", "검토한 대안", "비교한 대안과 제외 이유"},
			{"rationale", "선택 이유", "확인한 근거와 판단 기준"},
			{"consequences", "영향과 후속 작업", "장점, 비용, 위험, 필요한 후속 작업"},
			{"review", "재검토 조건", "언제 이 결정을 다시 검토할지"},
			{"references", "근거와 출처", "관련 문서 ID·버전, 링크와 이전 결정. 미확정 사항을 사실로 쓰지 않기"},
		}},
		{Kind: "history", Name: "히스토리", Sections: []TemplateSection{
			{"purpose", "요청 목적과 범위", "요청과 실제 수행 범위"},
			{"outcome", "작업 상태", "완료·부분 완료·진행 불가·취소 중 실제 상태"},
			{"summary", "결과 요약", "실제로 달라진 동작과 결과"},
			{"changes", "주요 변경과 산출물", "변경한 경로·자료·산출물 링크"},
			{"decisions", "결정과 이유", "중요한 결정·이유 및 결정 문서 링크"},
			{"validation", "검증과 결과", "실행한 검사와 결과, 생략한 검사와 이유"},
			{"remaining", "남은 문제와 다음 작업", "제약·미완료·다음 할 일. 없으면 없음으로 명시"},
			{"references", "근거와 출처", "확인된 문서·버전·브랜치·커밋·PR. 미커밋/미배포 상태 구분"},
		}},
		{Kind: "glossary", Name: "용어집", Sections: []TemplateSection{
			{"definition", "정의", "이 프로젝트에서 이 용어가 뜻하는 것. 제목은 용어 자체로 쓴다"},
			{"synonyms", "동의어", "쉼표로 구분한 동의어·별칭·영문 표기·약어. 검색할 때 이 용어와 함께 확장된다. 없으면 없음"},
			{"usage", "사용 맥락과 예시", "쓰이는 화면·코드·문서와 혼동하기 쉬운 용어"},
			{"references", "근거와 출처", "확인한 문서 ID·버전, 링크. 없으면 없음으로 명시"},
		}},
	}
	for i := range items {
		items[i].Version = 1
		body, _ := templateFiles.ReadFile("templates/" + items[i].Kind + ".md")
		items[i].Markdown = string(body)
	}
	return items
}

type TemplateDraft struct {
	Kind      string            `json:"kind" jsonschema:"brief, decision, history, or glossary"`
	Title     string            `json:"title"`
	Sections  map[string]string `json:"sections" jsonschema:"Fill every required section from get_document_templates; explicitly state unknown or not applicable instead of inventing facts"`
	Tags      []string          `json:"tags,omitempty"`
	Status    string            `json:"status,omitempty" jsonschema:"draft by default; active only for confirmed knowledge"`
	SourceURL string            `json:"source_url,omitempty"`
}
type TemplateDocumentInput struct {
	ProjectID string `json:"project_id"`
	TemplateDraft
}

func renderTemplate(d TemplateDraft) (string, error) {
	for _, t := range documentTemplates() {
		if t.Kind == d.Kind {
			if err := bounded("title", d.Title, 1, 200); err != nil {
				return "", err
			}
			if len(d.Sections) != len(t.Sections) {
				return "", problem("INVALID_INPUT", "Provide all required template sections and no unknown sections.")
			}
			// Write in schema order, never by map order. User values are never templates.
			var b strings.Builder
			fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(d.Title))
			for _, section := range t.Sections {
				value, ok := d.Sections[section.Key]
				if !ok {
					return "", problem("INVALID_INPUT", "Missing section: "+section.Key)
				}
				if err := bounded(section.Key, value, 1, 10000); err != nil {
					return "", err
				}
				fmt.Fprintf(&b, "## %s\n\n%s\n\n", section.Label, strings.TrimSpace(value))
			}
			return b.String(), nil
		}
	}
	return "", problem("INVALID_INPUT", "Choose the brief, decision, history, or glossary template.")
}
func createTemplatedDocument(ctx context.Context, tx *sql.Tx, a Identity, in TemplateDocumentInput) (any, error) {
	content, err := renderTemplate(in.TemplateDraft)
	if err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = "draft"
	}
	return createDocumentChecked(ctx, tx, a, DocumentInput{ProjectID: in.ProjectID, Title: in.Title, Kind: in.Kind, Content: content, Tags: in.Tags, Status: in.Status, SourceURL: in.SourceURL})
}
