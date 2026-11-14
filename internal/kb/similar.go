package kb

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Duplicate and conflicting decisions (중복·충돌 결정 감지).
//
// When a decision is saved, existing active or draft decisions with similar
// wording are returned as suggestions. Nothing is linked automatically: the
// author decides whether the new decision supersedes an old one, merely
// relates to it, or conflicts and needs the user's call.

const (
	// similarDecisionThreshold is the minimum similarity reported.
	similarDecisionThreshold = 0.18
	// supersedeThreshold suggests supersedes instead of related.
	supersedeThreshold  = 0.4
	maxSimilarDecisions = 5
)

type SimilarDocument struct {
	DocumentID        string   `json:"document_id"`
	Title             string   `json:"title"`
	Kind              string   `json:"kind"`
	Status            string   `json:"status"`
	Revision          int      `json:"revision"`
	UpdatedAt         string   `json:"updated_at"`
	Similarity        float64  `json:"similarity"`
	SharedTerms       []string `json:"shared_terms"`
	SuggestedRelation string   `json:"suggested_relation"`
	Suggestion        string   `json:"suggestion"`
	Citation          string   `json:"citation"`
}

// similarityStopwords are template headings and filler that every decision
// shares; counting them would make all templated decisions look alike.
var similarityStopwords = map[string]bool{
	"결정": true, "배경": true, "내용": true, "검토": true, "대안": true, "선택": true, "이유": true, "영향": true, "후속": true,
	"작업": true, "재검토": true, "조건": true, "근거": true, "출처": true, "없음": true, "미확인": true, "확인": true, "필요": true,
	"한다": true, "있다": true, "없다": true, "하는": true, "the": true, "and": true, "for": true, "with": true, "this": true, "that": true,
}

// similarityTerms returns the distinctive lexemes of text, ignoring Markdown
// headings, citations and stopwords.
func similarityTerms(text string) map[string]bool {
	lines := strings.Split(IndexableText("decision", text), "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	out := map[string]bool{}
	for _, term := range Lexemes(strings.Join(kept, "\n")) {
		if utf8.RuneCountInString(term) < 2 || similarityStopwords[term] {
			continue
		}
		out[term] = true
	}
	return out
}

func jaccard(a, b map[string]bool) (float64, []string) {
	if len(a) == 0 || len(b) == 0 {
		return 0, nil
	}
	shared := []string{}
	for term := range a {
		if b[term] {
			shared = append(shared, term)
		}
	}
	union := len(a) + len(b) - len(shared)
	return float64(len(shared)) / float64(union), shared
}

// decisionSimilarity weighs titles (which name the decided topic) and bodies.
func decisionSimilarity(titleA, bodyA, titleB, bodyB string) (float64, []string) {
	titleScore, sharedTitle := jaccard(similarityTerms(titleA), similarityTerms(titleB))
	bodyScore, sharedBody := jaccard(similarityTerms(titleA+"\n"+bodyA), similarityTerms(titleB+"\n"+bodyB))
	seen := map[string]bool{}
	shared := []string{}
	for _, term := range append(sharedTitle, sharedBody...) {
		if !seen[term] {
			seen[term] = true
			shared = append(shared, term)
		}
	}
	// Longer shared terms first: they are the more specific evidence.
	sort.SliceStable(shared, func(i, j int) bool {
		li, lj := utf8.RuneCountInString(shared[i]), utf8.RuneCountInString(shared[j])
		if li != lj {
			return li > lj
		}
		return shared[i] < shared[j]
	})
	if len(shared) > 8 {
		shared = shared[:8]
	}
	return 0.4*titleScore + 0.6*bodyScore, shared
}

// similarDecisions finds active or draft decisions resembling title/content,
// excluding excludeID and documents already linked to it. projectAccess must
// already have been checked by the caller.
func similarDecisions(ctx context.Context, tx *sql.Tx, projectID, excludeID, title, content string, limit int) ([]SimilarDocument, error) {
	body := []rune(IndexableText("decision", content))
	if len(body) > 1500 {
		body = body[:1500]
	}
	query := QueryText(title + " " + string(body))
	if query == "" {
		return []SimilarDocument{}, nil
	}
	type candidate struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		Revision  int    `json:"revision"`
		UpdatedAt string `json:"updated_at"`
		Content   string `json:"content"`
	}
	candidates, err := many[candidate](ctx, tx, `WITH q AS (SELECT to_tsquery('simple',$3) AS q)
	SELECT jsonb_build_object('id',d.id,'title',d.title,'status',d.status,'revision',d.revision,'updated_at',d.updated_at,'content',left(d.content,20000))
	FROM documents d CROSS JOIN q
	CROSS JOIN LATERAL (SELECT max(ts_rank_cd(c.search_vector,q.q)) AS rank FROM chunks c WHERE c.document_id=d.id AND c.search_vector@@q.q) r
	WHERE d.project_id=$1 AND d.kind='decision' AND d.status IN ('active','draft') AND d.id::text<>$2 AND r.rank IS NOT NULL
	AND NOT EXISTS (SELECT 1 FROM document_links l WHERE (l.source_id::text=$2 AND l.target_id=d.id) OR (l.target_id::text=$2 AND l.source_id=d.id))
	ORDER BY r.rank DESC,d.updated_at DESC,d.id LIMIT 30`, projectID, excludeID, query)
	if err != nil {
		return nil, err
	}
	out := []SimilarDocument{}
	for _, c := range candidates {
		score, shared := decisionSimilarity(title, content, c.Title, c.Content)
		if score < similarDecisionThreshold {
			continue
		}
		s := SimilarDocument{DocumentID: c.ID, Title: c.Title, Kind: "decision", Status: c.Status, Revision: c.Revision, UpdatedAt: c.UpdatedAt,
			Similarity: float64(int(score*1000+0.5)) / 1000, SharedTerms: shared, SuggestedRelation: relationRelated,
			Citation: fmt.Sprintf("kb://%s/documents/%s?revision=%d", projectID, c.ID, c.Revision)}
		s.Suggestion = "Similar decision on a related topic. Read it; if both stay valid, link_documents relation=related. If they conflict, confirm with the user which one holds."
		if score >= supersedeThreshold {
			s.SuggestedRelation = relationSupersedes
			s.Suggestion = "Likely the same topic. If the new decision replaces it, link_documents(source_id=new, target_id=this, relation=supersedes) after the user confirms; if both should stay, explain the difference or use related."
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Similarity > out[j].Similarity })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// attachSimilarDecisions annotates a freshly saved decision with suggestions.
func attachSimilarDecisions(ctx context.Context, tx *sql.Tx, d Document) (Document, error) {
	if d.Kind != "decision" || d.Status == "archived" {
		return d, nil
	}
	similar, err := similarDecisions(ctx, tx, d.ProjectID, d.ID, d.Title, d.Content, maxSimilarDecisions)
	if err != nil {
		return d, err
	}
	if len(similar) > 0 {
		d.SimilarDecisions = similar
	}
	return d, nil
}

// createDocumentChecked is create_document plus duplicate/conflict detection.
func createDocumentChecked(ctx context.Context, tx *sql.Tx, a Identity, in DocumentInput) (any, error) {
	out, err := createDocument(ctx, tx, a, in)
	if err != nil {
		return nil, err
	}
	return attachSimilarDecisions(ctx, tx, out.(Document))
}

type SimilarDecisionsInput struct {
	ProjectID  string `json:"project_id"`
	DocumentID string `json:"document_id" jsonschema:"An existing decision document"`
	Limit      int    `json:"limit,omitempty" jsonschema:"1–20, default 5"`
}

func findSimilarDecisions(ctx context.Context, tx *sql.Tx, a Identity, in SimilarDecisionsInput) (any, error) {
	d, err := getDocument(ctx, tx, a, DocumentRef{ProjectID: in.ProjectID, DocumentID: in.DocumentID})
	if err != nil {
		return nil, err
	}
	if d.Kind != "decision" {
		return nil, problem("INVALID_INPUT", "find_similar_decisions compares decision documents only.")
	}
	if in.Limit == 0 {
		in.Limit = maxSimilarDecisions
	}
	if in.Limit < 1 || in.Limit > 20 {
		return nil, problem("INVALID_INPUT", "limit must be 1–20.")
	}
	similar, err := similarDecisions(ctx, tx, d.ProjectID, d.ID, d.Title, d.Content, in.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"document_id": d.ID, "similar_decisions": similar,
		"guidance": "Suggestions only; nothing was linked. Already linked documents are excluded. Confirm supersedes or conflicts with the user before link_documents."}, nil
}
