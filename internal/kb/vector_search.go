package kb

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Store) searchKnowledge(ctx context.Context, tx *sql.Tx, actor Identity, in SearchInput) (SearchResult, error) {
	// Authorization precedes even query embedding, so unauthorized requests never invoke the model.
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, false); err != nil {
		return SearchResult{Hits: []SearchHit{}}, err
	}
	return s.searchProjects(ctx, tx, []string{in.ProjectID}, in)
}

// searchProjects searches projects the caller has already been authorized
// for. in.ProjectID is ignored; scope comes from projectIDs only.
func (s *Store) searchProjects(ctx context.Context, tx *sql.Tx, projectIDs []string, in SearchInput) (SearchResult, error) {
	out := SearchResult{Hits: []SearchHit{}}
	limit, err := pagination(in.Limit, 0)
	if err != nil {
		return out, err
	}
	if err = bounded("query", in.Query, 1, 1000); err != nil {
		return out, err
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if !validStatus(in.Status) {
		return out, problem("INVALID_INPUT", "Unknown status.")
	}
	if in.Mode == "" {
		in.Mode = "hybrid"
		if s.Embedder == nil {
			in.Mode = "lexical"
		}
	}
	if in.Mode != "hybrid" && in.Mode != "semantic" && in.Mode != "lexical" {
		return out, problem("INVALID_INPUT", "Choose hybrid, semantic, or lexical search.")
	}
	out.Retrieval = in.Mode
	out.timing.Mode = in.Mode
	scope, err := projectScope(projectIDs)
	if err != nil || len(projectIDs) == 0 {
		return out, err
	}
	// Glossary synonyms widen both keyword and embedding queries. The original
	// query was validated above; expansion only appends known terms.
	query := in.Query
	if !in.NoSynonyms {
		if out.ExpandedTerms, err = glossaryExpansion(ctx, tx, scope, in.Query); err != nil {
			return out, err
		}
		if len(out.ExpandedTerms) > 0 {
			query += " " + strings.Join(out.ExpandedTerms, " ")
		}
	}
	issueLimit := min(limit, maxIssueHits)
	if in.Mode == "lexical" {
		start := time.Now()
		out.Hits, err = lexicalHits(ctx, tx, scope, query, in.Status, limit)
		out.timing.Lexical = since(start)
		if err == nil && in.includeIssues {
			out.Issues, err = issueLexicalHits(ctx, tx, scope, query, issueLimit)
		}
		return out, err
	}
	if s.Embedder == nil {
		return out, problem("EMBEDDING_DISABLED", "Configure Ollama to use semantic or hybrid search.")
	}
	// One pass counts both the chunks still waiting for vectors and the ready
	// chunks the exact vector scan below compares, which search_timings keeps
	// next to the scan time.
	err = tx.QueryRowContext(ctx, `SELECT
	count(*) FILTER (WHERE c.embedding IS NULL OR c.embedding_status<>'ready' OR c.embedding_model IS DISTINCT FROM $3),
	count(*) FILTER (WHERE c.embedding IS NOT NULL AND c.embedding_status='ready' AND c.embedding_model=$3)
	FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.project_id=ANY($1::text::uuid[]) AND d.status=$2`,
		scope, in.Status, s.Embedder.Model()).Scan(&out.UnindexedChunks, &out.timing.ScannedChunks)
	if err != nil {
		return out, err
	}
	if out.UnindexedChunks > 0 {
		out.Warning = "Some document chunks are still pending or failed embedding; semantic results are incomplete. Check get_embedding_status."
	}
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	start := time.Now()
	vectors, err := s.Embedder.Embed(queryCtx, []string{query})
	out.timing.Embed = since(start)
	cancel()
	if err == nil {
		err = validateVectors(vectors, 1)
	}
	if err != nil {
		if in.Mode == "semantic" {
			return out, err
		}
		out.Retrieval = "lexical-fallback"
		out.Warning = "Local embedding failed (" + errorCode(err) + "). Results use keyword search only."
		out.timing.ScannedChunks = 0
		start = time.Now()
		out.Hits, err = lexicalHits(ctx, tx, scope, query, in.Status, limit)
		out.timing.Lexical = since(start)
		if err == nil && in.includeIssues {
			out.Issues, err = issueLexicalHits(ctx, tx, scope, query, issueLimit)
		}
		return out, err
	}
	var issueSemantic []IssueSearchHit
	if in.includeIssues {
		if out.UnindexedIssueChunks, err = unindexedIssueChunks(ctx, tx, scope, s.Embedder.Model()); err != nil {
			return out, err
		}
		if issueSemantic, err = issueVectorHits(ctx, tx, scope, s.Embedder.Model(), vectorLiteral(vectors[0]), max(issueLimit, min(20, issueLimit*3))); err != nil {
			return out, err
		}
	}
	start = time.Now()
	semantic, err := many[SearchHit](ctx, tx, `WITH scoped AS MATERIALIZED (
	SELECT d.id AS document_id,d.project_id,d.title,d.kind,d.status,d.revision,d.source_url,d.updated_by,d.updated_at,
	c.position,c.content AS excerpt,c.embedding FROM documents d JOIN chunks c ON c.document_id=d.id
	WHERE d.project_id=ANY($1::text::uuid[]) AND d.status=$2 AND c.embedding_status='ready' AND c.embedding IS NOT NULL AND c.embedding_model=$3
	), ranked AS (
	SELECT document_id,project_id,title,kind,status,revision,source_url,updated_by,updated_at,position,excerpt,
	1-(embedding OPERATOR(public.<=>) $4::public.vector) AS score,
	row_number() OVER(PARTITION BY document_id ORDER BY embedding OPERATOR(public.<=>) $4::public.vector,position) AS n FROM scoped
	) SELECT to_jsonb(ranked)-'n' FROM ranked WHERE n=1 ORDER BY score DESC,document_id LIMIT $5`, scope, in.Status, s.Embedder.Model(), vectorLiteral(vectors[0]), max(limit, min(50, limit*3)))
	out.timing.Vector = since(start)
	if err != nil {
		return out, err
	}
	for i := range semantic {
		h := &semantic[i]
		h.Citation = fmt.Sprintf("kb://%s/documents/%s?revision=%d#chunk-%d", h.ProjectID, h.DocumentID, h.Revision, h.Position)
	}
	if in.Mode == "semantic" {
		out.Hits = semantic[:min(limit, len(semantic))]
		if in.includeIssues {
			out.Issues = issueSemantic[:min(issueLimit, len(issueSemantic))]
		}
		return out, nil
	}
	start = time.Now()
	lexical, err := lexicalHits(ctx, tx, scope, query, in.Status, max(limit, min(50, limit*3)))
	out.timing.Lexical = since(start)
	if err != nil {
		return out, err
	}
	out.Hits = fuseRanks(lexical, semantic, limit)
	if in.includeIssues {
		issueLexical, err := issueLexicalHits(ctx, tx, scope, query, max(issueLimit, min(20, issueLimit*3)))
		if err != nil {
			return out, err
		}
		out.Issues = fuseIssueRanks(issueLexical, issueSemantic, issueLimit)
	}
	return out, nil
}

// historyRankOffset pushes history hits this many places down each ranked list
// before fusion. History documents are long point-in-time records that mention
// many topics, make up most of a project's documents and grow with every
// finished task, so without an offset they crowd current knowledge (reference,
// brief, decision, glossary) out of the top results and the top results shift
// whenever a new history is saved. A history still ranks first when nothing
// else matches within this many places.
const historyRankOffset = 10

func kindRankOffset(kind string) int {
	if kind == "history" {
		return historyRankOffset
	}
	return 0
}

// RRF combines order rather than incomparable cosine and tsvector score magnitudes.
func fuseRanks(lexical, semantic []SearchHit, limit int) []SearchHit {
	type entry struct {
		hit   SearchHit
		score float64
	}
	merged := map[string]*entry{}
	for _, list := range [][]SearchHit{semantic, lexical} {
		for rank, hit := range list {
			e := merged[hit.DocumentID]
			if e == nil {
				e = &entry{hit: hit}
				merged[hit.DocumentID] = e
			}
			e.score += 1.0 / float64(60+rank+kindRankOffset(hit.Kind)+1)
		}
	}
	out := make([]SearchHit, 0, len(merged))
	for _, e := range merged {
		e.hit.Score = e.score
		out = append(out, e.hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].DocumentID < out[j].DocumentID
		}
		return out[i].Score > out[j].Score
	})
	return out[:min(limit, len(out))]
}
