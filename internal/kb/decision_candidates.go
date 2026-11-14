package kb

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Decision candidates (결정 후보 추출).
//
// finish_task reads the free-text "decisions" field and proposes decision
// drafts for statements that look like decisions. Nothing is saved: the
// candidates come back with a prefilled template draft so the agent can
// confirm with the user, fill the unknown sections and save it as a draft.

const maxDecisionCandidates = 5

type DecisionCandidate struct {
	Statement        string            `json:"statement"`
	Reason           string            `json:"reason,omitempty"`
	Draft            TemplateDraft     `json:"draft"`
	SimilarDecisions []SimilarDocument `json:"similar_decisions,omitempty"`
}

var (
	decisionCues = []string{"결정", "하기로", "기로 함", "기로 했", "채택", "선택", "도입", "유지", "사용한다", "사용하기", "적용한다", "적용하기", "않기로", "대신", "폐기", "전환",
		"decide", "decision", "chose", "choose", "adopt", "instead", "keep using", "switch to", "will use", "we use"}
	noDecisionText = map[string]bool{"없음": true, "없음.": true, "none": true, "none.": true, "n/a": true, "해당 없음": true, "해당없음": true, "-": true}
	reasonMarkers  = []string{"이유:", "이유는", "왜냐하면", "because", " — ", " -- ", "(이유", "→"}
	itemPrefix     = regexp.MustCompile(`^\s*(?:[-*+•]|\d+[.)])\s+`)
	sentenceEnd    = regexp.MustCompile(`([.!?。])\s+`)
)

// decisionStatement is one candidate before it becomes a draft.
type decisionStatement struct{ Statement, Reason string }

// splitDecisionItems splits text into list items, or sentences when a
// paragraph has no list markers. Continuation lines join their item.
func splitDecisionItems(text string) []string {
	items := []string{}
	for _, para := range regexp.MustCompile(`\n\s*\n`).Split(strings.ReplaceAll(text, "\r\n", "\n"), -1) {
		lines := strings.Split(para, "\n")
		listed := false
		for _, line := range lines {
			if itemPrefix.MatchString(line) {
				listed = true
				break
			}
		}
		if listed {
			current := ""
			for _, line := range lines {
				if itemPrefix.MatchString(line) {
					if strings.TrimSpace(current) != "" {
						items = append(items, current)
					}
					current = itemPrefix.ReplaceAllString(line, "")
				} else if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
					current += " " + strings.TrimSpace(line)
				}
			}
			if strings.TrimSpace(current) != "" {
				items = append(items, current)
			}
			continue
		}
		joined := strings.Join(strings.Fields(para), " ")
		for _, sentence := range strings.Split(sentenceEnd.ReplaceAllString(joined, "$1\n"), "\n") {
			if s := strings.TrimSpace(sentence); s != "" && !strings.HasPrefix(s, "#") {
				items = append(items, s)
			}
		}
	}
	return items
}

func hasDecisionCue(s string) bool {
	lower := strings.ToLower(s)
	for _, cue := range decisionCues {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}

// extractDecisionStatements returns at most max decision-like statements.
func extractDecisionStatements(text string, max int) []decisionStatement {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if noDecisionText[trimmed] {
		return nil
	}
	out := []decisionStatement{}
	seen := map[string]bool{}
	for _, item := range splitDecisionItems(text) {
		item = strings.TrimSpace(item)
		if utf8.RuneCountInString(item) < 8 || !hasDecisionCue(item) {
			continue
		}
		statement, reason := item, ""
		lower := strings.ToLower(item)
		for _, marker := range reasonMarkers {
			if i := strings.Index(lower, strings.ToLower(marker)); i > 0 {
				statement = strings.TrimSpace(item[:i])
				reason = strings.TrimSpace(strings.TrimLeft(item[i+len(marker):], " :"))
				reason = strings.TrimSuffix(reason, ")")
				break
			}
		}
		if utf8.RuneCountInString(statement) < 6 {
			statement, reason = item, ""
		}
		key := strings.ToLower(statement)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, decisionStatement{Statement: statement, Reason: reason})
		if len(out) == max {
			break
		}
	}
	return out
}

func decisionTitle(statement string) string {
	s := strings.TrimRight(strings.TrimSpace(statement), ".。!?;:, ")
	if r := []rune(s); len(r) > 80 {
		s = strings.TrimSpace(string(r[:80])) + "…"
	}
	return "결정: " + s
}

// decisionCandidates turns finish_task's decisions text into draft
// suggestions, skipping statements already covered by decisions saved in the
// same call. Existing similar decisions are attached so duplicates surface.
func decisionCandidates(ctx context.Context, tx *sql.Tx, run TaskRun, historyCitation, decisions string, saved []TemplateDraft) ([]DecisionCandidate, error) {
	out := []DecisionCandidate{}
	for _, st := range extractDecisionStatements(decisions, maxDecisionCandidates) {
		covered := false
		for _, d := range saved {
			if d.Kind != "decision" {
				continue
			}
			body := strings.Join([]string{d.Sections["decision"], d.Sections["context"], d.Sections["rationale"]}, "\n")
			if score, _ := decisionSimilarity(st.Statement, st.Statement, d.Title, body); score >= 0.3 {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		rationale := st.Reason
		if rationale == "" {
			rationale = "미확인 — 선택 이유를 작성자가 확인해 보완한다."
		}
		c := DecisionCandidate{Statement: st.Statement, Reason: st.Reason, Draft: TemplateDraft{
			Kind: "decision", Title: decisionTitle(st.Statement), Status: "draft", Tags: []string{"결정", "결정후보", "task:" + run.ID},
			Sections: map[string]string{
				"context":      "작업 '" + run.Title + "' 완료 기록에서 추출한 결정 후보. 결정이 필요했던 배경은 작성자가 확인해 보완한다.",
				"decision":     st.Statement,
				"alternatives": "미확인 — 검토한 대안이 있으면 작성자가 보완한다.",
				"rationale":    rationale,
				"consequences": "미확인 — 영향과 후속 작업을 작성자가 보완한다.",
				"review":       "미확인 — 재검토 조건을 작성자가 보완한다.",
				"references":   "작업 ID: " + run.ID + "\n작업 이력: " + historyCitation,
			},
		}}
		similar, err := similarDecisions(ctx, tx, run.ProjectID, "", c.Draft.Title, st.Statement+"\n"+st.Reason, 3)
		if err != nil {
			return nil, err
		}
		if len(similar) > 0 {
			c.SimilarDecisions = similar
		}
		out = append(out, c)
	}
	return out, nil
}
