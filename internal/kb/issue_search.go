package kb

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Questions and issues join search_knowledge as a separate result list
// (issues), next to document hits. Their title, body, answer and resolution
// are split into issue_chunks with the same chunk size, keyword index and
// embedding queue as documents. Closed items (withdrawn or no longer needed)
// are left out of results; open, answered and resolved items are returned with
// their status so the caller can tell an open question from a settled one.

// maxIssueHits caps the issue list so questions never crowd out documents.
const maxIssueHits = 5

// IssueSearchHit is a question or issue matching a search query.
type IssueSearchHit struct {
	IssueID   string  `json:"issue_id"`
	ProjectID string  `json:"project_id"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	Status    string  `json:"status"`
	Blocking  bool    `json:"blocking"`
	UpdatedAt string  `json:"updated_at"`
	Position  int     `json:"position"`
	Excerpt   string  `json:"excerpt"`
	Score     float64 `json:"score"`
	Citation  string  `json:"citation"`

	// Set only by search_across_projects.
	ProjectSlug string `json:"project_slug,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
}

// issueIndexText is the searchable text of a question: its body, then the
// current answer and resolution under their own headings. The title and tags
// are indexed separately (keyword weight A, embedding prefix).
func issueIndexText(body, answer, resolution string) string {
	parts := []string{strings.TrimSpace(body)}
	if a := strings.TrimSpace(answer); a != "" {
		parts = append(parts, "## 답변\n\n"+a)
	}
	if r := strings.TrimSpace(resolution); r != "" {
		parts = append(parts, "## 해결\n\n"+r)
	}
	return IndexableText("issue", strings.Join(parts, "\n\n"))
}

// rebuildIssueChunks replaces a question's chunks from its current text. New
// chunks default to pending, so the embedding worker regenerates their vectors.
// Callers hold the issue row (insert or update) in the same transaction.
func rebuildIssueChunks(ctx context.Context, tx *sql.Tx, issueID string) error {
	var title, body, answer, resolution string
	var tags []string
	if err := tx.QueryRowContext(ctx, `SELECT title,body,answer,resolution,to_jsonb(tags)::text FROM issues WHERE id=$1`, issueID).
		Scan(&title, &body, &answer, &resolution, jsonText{&tags}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_chunks WHERE issue_id=$1`, issueID); err != nil {
		return err
	}
	heading := strings.Join(Lexemes(title+" "+strings.Join(tags, " ")), " ")
	for position, content := range SplitChunks(issueIndexText(body, answer, resolution)) {
		_, err := tx.ExecContext(ctx, `INSERT INTO issue_chunks(id,issue_id,position,content,search_vector)
		VALUES($1,$2,$3,$4,setweight(to_tsvector('simple',$5),'A')||setweight(to_tsvector('simple',$6),'B'))`,
			uuid.NewString(), issueID, position, content, heading, strings.Join(Lexemes(content), " "))
		if err != nil {
			return err
		}
	}
	return nil
}

// chunkUnindexedIssues chunks up to 50 questions that have no chunks, which
// are those saved before issue chunks existed. Every question has a non-empty
// body, so a chunked question always has at least one chunk.
func (s *Store) chunkUnindexedIssues(ctx context.Context) (int, error) {
	count := 0
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM issues i WHERE NOT EXISTS (SELECT 1 FROM issue_chunks c WHERE c.issue_id=i.id)
		ORDER BY id LIMIT 50 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := rebuildIssueChunks(ctx, tx, id); err != nil {
				return err
			}
		}
		count = len(ids)
		return nil
	})
	return count, err
}

const issueHitColumns = `i.id AS issue_id,i.project_id,i.kind,i.title,i.status,i.blocking,i.updated_at,c.position,c.content AS excerpt`

// issueVectorHits ranks questions by their closest ready chunk.
func issueVectorHits(ctx context.Context, tx *sql.Tx, scope, model, vector string, limit int) ([]IssueSearchHit, error) {
	hits, err := many[IssueSearchHit](ctx, tx, `WITH ranked AS (
	SELECT `+issueHitColumns+`,1-(c.embedding OPERATOR(public.<=>) $3::public.vector) AS score,
	row_number() OVER(PARTITION BY i.id ORDER BY c.embedding OPERATOR(public.<=>) $3::public.vector,c.position) AS n
	FROM issues i JOIN issue_chunks c ON c.issue_id=i.id
	WHERE i.project_id=ANY($1::text::uuid[]) AND i.status<>'closed' AND c.embedding_status='ready' AND c.embedding IS NOT NULL AND c.embedding_model=$2
	) SELECT to_jsonb(ranked)-'n' FROM ranked WHERE n=1 ORDER BY score DESC,issue_id LIMIT $4`, scope, model, vector, limit)
	return withIssueCitations(hits), err
}

// issueLexicalHits ranks questions by keyword match of their best chunk.
func issueLexicalHits(ctx context.Context, tx *sql.Tx, scope, rawQuery string, limit int) ([]IssueSearchHit, error) {
	query := QueryText(rawQuery)
	if query == "" {
		return []IssueSearchHit{}, nil
	}
	hits, err := many[IssueSearchHit](ctx, tx, `WITH matched AS (
	SELECT `+issueHitColumns+`,ts_rank_cd(c.search_vector,to_tsquery('simple',$2)) AS score,
	row_number() OVER(PARTITION BY i.id ORDER BY ts_rank_cd(c.search_vector,to_tsquery('simple',$2)) DESC,c.position) AS n
	FROM issues i JOIN issue_chunks c ON c.issue_id=i.id
	WHERE i.project_id=ANY($1::text::uuid[]) AND i.status<>'closed' AND c.search_vector@@to_tsquery('simple',$2)
	) SELECT to_jsonb(matched)-'n' FROM matched WHERE n=1 ORDER BY score DESC,updated_at DESC,issue_id LIMIT $3`, scope, query, limit)
	return withIssueCitations(hits), err
}

func withIssueCitations(hits []IssueSearchHit) []IssueSearchHit {
	for i := range hits {
		hits[i].Citation = "issue://" + hits[i].ProjectID + "/" + hits[i].IssueID
	}
	return hits
}

// unindexedIssueChunks counts issue chunks in scope still waiting for vectors.
func unindexedIssueChunks(ctx context.Context, tx *sql.Tx, scope, model string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM issue_chunks c JOIN issues i ON i.id=c.issue_id
	WHERE i.project_id=ANY($1::text::uuid[]) AND i.status<>'closed'
	AND (c.embedding IS NULL OR c.embedding_status<>'ready' OR c.embedding_model IS DISTINCT FROM $2)`, scope, model).Scan(&n)
	return n, err
}

// fuseIssueRanks combines keyword and vector order with the same reciprocal
// rank fusion as document hits.
func fuseIssueRanks(lexical, semantic []IssueSearchHit, limit int) []IssueSearchHit {
	merged := map[string]*IssueSearchHit{}
	scores := map[string]float64{}
	for _, list := range [][]IssueSearchHit{semantic, lexical} {
		for rank, hit := range list {
			if merged[hit.IssueID] == nil {
				h := hit
				merged[hit.IssueID] = &h
			}
			scores[hit.IssueID] += 1.0 / float64(60+rank+1)
		}
	}
	out := make([]IssueSearchHit, 0, len(merged))
	for id, h := range merged {
		h.Score = scores[id]
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].IssueID < out[j].IssueID
		}
		return out[i].Score > out[j].Score
	})
	return out[:min(limit, len(out))]
}
