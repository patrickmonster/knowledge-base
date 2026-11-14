package kb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// maxSearchProjects bounds one cross-project request so a single query cannot
// fan out over an unbounded number of projects.
const maxSearchProjects = 50

type CrossSearchInput struct {
	ProjectIDs []string `json:"project_ids,omitempty" jsonschema:"Projects to search (at most 50). Omit to search every project you can access."`
	Query      string   `json:"query"`
	Limit      int      `json:"limit,omitempty"`
	Status     string   `json:"status,omitempty"`
	Mode       string   `json:"mode,omitempty" jsonschema:"hybrid (default when configured), semantic, or lexical"`
}

type SearchedProject struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type CrossSearchResult struct {
	Hits            []SearchHit       `json:"hits"`
	Retrieval       string            `json:"retrieval"`
	Warning         string            `json:"warning,omitempty"`
	UnindexedChunks int               `json:"unindexed_chunks"`
	Projects        []SearchedProject `json:"projects"`
	// Omitted reports accessible projects left out because of maxSearchProjects.
	Omitted         int               `json:"omitted_projects,omitempty"`
}

// projectScope renders already-validated project IDs as a Postgres array
// literal, passed as text and cast to uuid[] in SQL.
func projectScope(ids []string) (string, error) {
	for _, id := range ids {
		if err := validID(id); err != nil {
			return "", err
		}
	}
	return "{" + strings.Join(ids, ",") + "}", nil
}

func lexicalHits(ctx context.Context, tx *sql.Tx, scope, rawQuery, status string, limit int) ([]SearchHit, error) {
	query := QueryText(rawQuery)
	if query == "" {
		return []SearchHit{}, nil
	}
	hits, err := many[SearchHit](ctx, tx, `WITH matched AS (SELECT d.id AS document_id,d.project_id,d.title,d.kind,d.status,d.revision,d.source_url,d.updated_by,d.updated_at,
	c.position,c.content AS excerpt,ts_rank_cd(c.search_vector,to_tsquery('simple',$2)) AS score,
	row_number() OVER(PARTITION BY d.id ORDER BY ts_rank_cd(c.search_vector,to_tsquery('simple',$2)) DESC,c.position) AS n
	FROM documents d JOIN chunks c ON c.document_id=d.id WHERE d.project_id=ANY($1::text::uuid[]) AND d.status=$3 AND c.search_vector@@to_tsquery('simple',$2))
	SELECT to_jsonb(matched)-'n' FROM matched WHERE n=1 ORDER BY score DESC,updated_at DESC,document_id LIMIT $4`, scope, query, status, limit)
	for i := range hits {
		hits[i].Citation = fmt.Sprintf("kb://%s/documents/%s?revision=%d#chunk-%d", hits[i].ProjectID, hits[i].DocumentID, hits[i].Revision, hits[i].Position)
	}
	return hits, err
}

// searchAcrossProjects searches every requested project the actor is a member
// of. A project-scoped key only ever reaches its own project. Requesting a
// project without membership fails as NOT_FOUND, like single-project calls,
// so the response never reveals which project IDs exist.
func (s *Store) searchAcrossProjects(ctx context.Context, tx *sql.Tx, actor Identity, in CrossSearchInput) (CrossSearchResult, error) {
	out := CrossSearchResult{Hits: []SearchHit{}, Projects: []SearchedProject{}}
	if len(in.ProjectIDs) > maxSearchProjects {
		return out, problem("INVALID_INPUT", fmt.Sprintf("Search at most %d projects at once.", maxSearchProjects))
	}
	requested := map[string]bool{}
	for _, id := range in.ProjectIDs {
		if err := validID(id); err != nil {
			return out, err
		}
		if actor.ProjectID != "" && id != actor.ProjectID {
			return out, problem("FORBIDDEN", "This key is restricted to another project.")
		}
		requested[id] = true
	}
	// FOR SHARE OF m keeps membership stable for the rest of the transaction,
	// matching projectAccess.
	accessible, err := many[SearchedProject](ctx, tx, `SELECT jsonb_build_object('id',p.id,'slug',p.slug,'name',p.name,'role',m.role)
	FROM projects p JOIN project_members m ON m.project_id=p.id
	WHERE m.user_id=$1 AND ($2='' OR p.id=NULLIF($2,'')::uuid) ORDER BY p.created_at,p.id FOR SHARE OF m`, actor.User.ID, actor.ProjectID)
	if err != nil {
		return out, err
	}
	for _, p := range accessible {
		if len(requested) > 0 && !requested[p.ID] {
			continue
		}
		if len(out.Projects) == maxSearchProjects {
			out.Omitted++
			continue
		}
		out.Projects = append(out.Projects, p)
	}
	if len(requested) > 0 && len(out.Projects) != len(requested) {
		return out, notFound(sql.ErrNoRows)
	}
	ids := make([]string, len(out.Projects))
	byID := map[string]SearchedProject{}
	for i, p := range out.Projects {
		ids[i] = p.ID
		byID[p.ID] = p
	}
	result, err := s.searchProjects(ctx, tx, ids, SearchInput{Query: in.Query, Limit: in.Limit, Status: in.Status, Mode: in.Mode})
	if err != nil {
		return out, err
	}
	// Links never cross projects, so superseding documents are always within
	// the searched scope the caller can read.
	if result.Hits, err = demoteSuperseded(ctx, tx, result.Hits); err != nil {
		return out, err
	}
	for i := range result.Hits {
		p := byID[result.Hits[i].ProjectID]
		result.Hits[i].ProjectSlug, result.Hits[i].ProjectName = p.Slug, p.Name
	}
	out.Hits, out.Retrieval, out.Warning, out.UnindexedChunks = result.Hits, result.Retrieval, result.Warning, result.UnindexedChunks
	if out.Omitted > 0 {
		note := fmt.Sprintf("Only the %d oldest accessible projects were searched; pass project_ids to choose others.", maxSearchProjects)
		if out.Warning != "" {
			note = out.Warning + " " + note
		}
		out.Warning = note
	}
	return out, nil
}
