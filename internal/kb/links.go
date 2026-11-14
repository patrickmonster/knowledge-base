package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Document link relations. Links are current-state metadata: they never create
// a document revision and never change chunks or vectors.
const (
	// relationSupersedes: source (newer) replaces target (older) of the same kind.
	// While the source is active, the target is ranked below current knowledge.
	relationSupersedes = "supersedes"
	// relationImplements: source history carried out target decision or brief.
	relationImplements = "implements"
	// relationRelated: undirected; stored once in either direction.
	relationRelated = "related"
)

type LinkInput struct {
	ProjectID string `json:"project_id"`
	SourceID  string `json:"source_id" jsonschema:"supersedes: the newer document; implements: the history; related: either document"`
	TargetID  string `json:"target_id" jsonschema:"supersedes: the older replaced document; implements: the decision or brief carried out; related: the other document"`
	Relation  string `json:"relation" jsonschema:"supersedes, implements, or related"`
	Note      string `json:"note,omitempty" jsonschema:"Optional reason, up to 500 characters"`
}

type DocumentLink struct {
	ProjectID string `json:"project_id"`
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
	Relation  string `json:"relation"`
	Note      string `json:"note"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

type LinkResult struct {
	Link    DocumentLink `json:"link"`
	Created bool         `json:"created"`
}

// LinkedDocument is the other end of a link, seen from one document.
type LinkedDocument struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Revision   int    `json:"revision"`
	Note       string `json:"note"`
	CreatedAt  string `json:"created_at"`
}

// DocumentLinks groups links by meaning from the viewpoint of one document.
type DocumentLinks struct {
	Supersedes    []LinkedDocument `json:"supersedes"`
	SupersededBy  []LinkedDocument `json:"superseded_by"`
	Implements    []LinkedDocument `json:"implements"`
	ImplementedBy []LinkedDocument `json:"implemented_by"`
	Related       []LinkedDocument `json:"related"`
}

// SupersedingDocument identifies an active document that replaces a search hit.
type SupersedingDocument struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
}

func validRelation(s string) bool {
	return s == relationSupersedes || s == relationImplements || s == relationRelated
}

func linkDocuments(ctx context.Context, tx *sql.Tx, actor Identity, in LinkInput) (any, error) {
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, true); err != nil {
		return nil, err
	}
	if err := validID(in.SourceID); err != nil {
		return nil, err
	}
	if err := validID(in.TargetID); err != nil {
		return nil, err
	}
	if !validRelation(in.Relation) {
		return nil, problem("INVALID_INPUT", "relation must be supersedes, implements, or related.")
	}
	in.Note = strings.TrimSpace(in.Note)
	if err := bounded("note", in.Note, 0, 500); err != nil {
		return nil, err
	}
	if in.SourceID == in.TargetID {
		return nil, problem("INVALID_INPUT", "A document cannot link to itself.")
	}
	// One link writer per project at a time keeps the supersedes cycle check exact.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('document_links:'||$1::text,0))`, in.ProjectID); err != nil {
		return nil, err
	}
	kinds := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,kind FROM documents WHERE project_id=$1 AND id::text IN ($2,$3) ORDER BY id FOR SHARE`, in.ProjectID, in.SourceID, in.TargetID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			rows.Close()
			return nil, err
		}
		kinds[id] = kind
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(kinds) != 2 {
		return nil, problem("NOT_FOUND", "Both documents must exist in this project.")
	}
	source, target := kinds[in.SourceID], kinds[in.TargetID]
	switch in.Relation {
	case relationSupersedes:
		if source != target || source == "history" {
			return nil, problem("INVALID_INPUT", "supersedes links two documents of the same kind (not history): the newer source replaces the older target.")
		}
		var cycle bool
		err := tx.QueryRowContext(ctx, `WITH RECURSIVE chain(id) AS (
		SELECT target_id FROM document_links WHERE source_id=$1 AND relation='supersedes'
		UNION SELECT l.target_id FROM document_links l JOIN chain c ON l.source_id=c.id AND l.relation='supersedes'
		) SELECT EXISTS(SELECT 1 FROM chain WHERE id=$2)`, in.TargetID, in.SourceID).Scan(&cycle)
		if err != nil {
			return nil, err
		}
		if cycle {
			return nil, problem("CONFLICT", "The target already supersedes the source directly or indirectly.")
		}
	case relationImplements:
		if source != "history" || (target != "decision" && target != "brief") {
			return nil, problem("INVALID_INPUT", "implements links a history (source) to the decision or brief it carried out (target).")
		}
	case relationRelated:
		existing, err := one[DocumentLink](ctx, tx, `SELECT to_jsonb(l) FROM document_links l WHERE relation='related' AND source_id=$1 AND target_id=$2`, in.TargetID, in.SourceID)
		if err == nil {
			return LinkResult{Link: existing}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	link, err := one[DocumentLink](ctx, tx, `WITH inserted AS (INSERT INTO document_links(project_id,source_id,target_id,relation,note,created_by)
	VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(source_id,relation,target_id) DO NOTHING RETURNING *) SELECT to_jsonb(inserted) FROM inserted`,
		in.ProjectID, in.SourceID, in.TargetID, in.Relation, in.Note, actor.User.ID)
	if err == nil {
		return LinkResult{Link: link, Created: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Already linked: retries are idempotent and keep the original note.
	link, err = one[DocumentLink](ctx, tx, `SELECT to_jsonb(l) FROM document_links l WHERE source_id=$1 AND relation=$2 AND target_id=$3`, in.SourceID, in.Relation, in.TargetID)
	return LinkResult{Link: link}, err
}

func unlinkDocuments(ctx context.Context, tx *sql.Tx, actor Identity, in LinkInput) (any, error) {
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, true); err != nil {
		return nil, err
	}
	if err := validID(in.SourceID); err != nil {
		return nil, err
	}
	if err := validID(in.TargetID); err != nil {
		return nil, err
	}
	if !validRelation(in.Relation) {
		return nil, problem("INVALID_INPUT", "relation must be supersedes, implements, or related.")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM document_links WHERE project_id=$1 AND relation=$4
	AND ((source_id=$2 AND target_id=$3) OR ($4='related' AND source_id=$3 AND target_id=$2))`, in.ProjectID, in.SourceID, in.TargetID, in.Relation)
	if err != nil {
		return nil, err
	}
	removed, err := result.RowsAffected()
	return map[string]any{"removed": removed > 0}, err
}

// documentLinks returns the current links of one document, grouped by meaning.
// The caller must already have checked project access.
func documentLinks(ctx context.Context, tx *sql.Tx, projectID, documentID string) (DocumentLinks, error) {
	out := DocumentLinks{Supersedes: []LinkedDocument{}, SupersededBy: []LinkedDocument{}, Implements: []LinkedDocument{}, ImplementedBy: []LinkedDocument{}, Related: []LinkedDocument{}}
	type row struct {
		Outgoing bool   `json:"outgoing"`
		Relation string `json:"relation"`
		LinkedDocument
	}
	rows, err := many[row](ctx, tx, `SELECT jsonb_build_object('outgoing',l.source_id=$2,'relation',l.relation,'document_id',d.id,'title',d.title,'kind',d.kind,
	'status',d.status,'revision',d.revision,'note',l.note,'created_at',l.created_at)
	FROM document_links l JOIN documents d ON d.id=CASE WHEN l.source_id=$2 THEN l.target_id ELSE l.source_id END
	WHERE l.project_id=$1 AND (l.source_id=$2 OR l.target_id=$2) ORDER BY l.created_at,d.id`, projectID, documentID)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		switch {
		case r.Relation == relationSupersedes && r.Outgoing:
			out.Supersedes = append(out.Supersedes, r.LinkedDocument)
		case r.Relation == relationSupersedes:
			out.SupersededBy = append(out.SupersededBy, r.LinkedDocument)
		case r.Relation == relationImplements && r.Outgoing:
			out.Implements = append(out.Implements, r.LinkedDocument)
		case r.Relation == relationImplements:
			out.ImplementedBy = append(out.ImplementedBy, r.LinkedDocument)
		default:
			out.Related = append(out.Related, r.LinkedDocument)
		}
	}
	return out, nil
}

// demoteSuperseded annotates hits replaced by an active document and moves them
// after all current hits, keeping relative order within each group. A draft or
// archived replacement does not demote anything until it becomes active.
func demoteSuperseded(ctx context.Context, tx *sql.Tx, hits []SearchHit) ([]SearchHit, error) {
	if len(hits) == 0 {
		return hits, nil
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.DocumentID
	}
	type row struct {
		TargetID string `json:"target_id"`
		SupersedingDocument
	}
	rows, err := many[row](ctx, tx, `SELECT jsonb_build_object('target_id',l.target_id,'document_id',d.id,'title',d.title)
	FROM document_links l JOIN documents d ON d.id=l.source_id
	WHERE l.relation='supersedes' AND d.status='active' AND l.target_id::text=ANY($1::text[]) ORDER BY d.updated_at DESC,d.id`, ids)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return hits, nil
	}
	by := map[string][]SupersedingDocument{}
	for _, r := range rows {
		by[r.TargetID] = append(by[r.TargetID], r.SupersedingDocument)
	}
	for i := range hits {
		hits[i].SupersededBy = by[hits[i].DocumentID]
	}
	sort.SliceStable(hits, func(i, j int) bool { return len(hits[i].SupersededBy) == 0 && len(hits[j].SupersededBy) > 0 })
	return hits, nil
}

// searchCurrentKnowledge is searchKnowledge with superseded documents ranked
// after current knowledge. search_knowledge and task context use it.
func (s *Store) searchCurrentKnowledge(ctx context.Context, tx *sql.Tx, a Identity, in SearchInput) (SearchResult, error) {
	out, err := s.searchKnowledge(ctx, tx, a, in)
	if err != nil {
		return out, err
	}
	out.Hits, err = demoteSuperseded(ctx, tx, out.Hits)
	markRecheck(out.Hits, time.Now())
	return out, err
}

// recentDecisions lists the latest active decisions that no active document
// supersedes, as discovery pointers for task context.
func recentDecisions(ctx context.Context, tx *sql.Tx, projectID string, limit int) ([]map[string]any, error) {
	return many[map[string]any](ctx, tx, `SELECT to_jsonb(d)-'content' FROM documents d WHERE d.project_id=$1 AND d.status='active' AND d.kind='decision'
	AND NOT EXISTS(SELECT 1 FROM document_links l JOIN documents s ON s.id=l.source_id WHERE l.target_id=d.id AND l.relation='supersedes' AND s.status='active')
	ORDER BY d.updated_at DESC,d.id LIMIT $2`, projectID, limit)
}

// implementedDocuments validates finish_task's implements targets before the
// history exists and returns them for the history body.
func implementedDocuments(ctx context.Context, tx *sql.Tx, projectID string, ids []string) ([]Document, error) {
	if len(ids) > 20 {
		return nil, problem("INVALID_INPUT", "At most 20 implemented documents are allowed.")
	}
	out := []Document{}
	seen := map[string]bool{}
	for _, id := range ids {
		if err := validID(id); err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		d, err := one[Document](ctx, tx, "SELECT to_jsonb(d)-'content' FROM documents d WHERE id=$1 AND project_id=$2", id, projectID)
		if err != nil {
			return nil, notFound(err)
		}
		if d.Kind != "decision" && d.Kind != "brief" {
			return nil, problem("INVALID_INPUT", fmt.Sprintf("implements must reference decision or brief documents; %s is %s.", id, d.Kind))
		}
		out = append(out, d)
	}
	return out, nil
}

type LinksQuery struct {
	ProjectID  string `json:"project_id"`
	DocumentID string `json:"document_id"`
}

func registerLinkTools(t Tools, s *Store) {
	add(t, s, "link_documents", "Link two documents. relation: supersedes (newer source replaces older target of the same kind; once the source is active the target ranks below current knowledge in search), implements (history source carried out decision/brief target), related (undirected). Idempotent; no new revision.", true, func(in LinkInput) string { return in.ProjectID }, linkDocuments)
	add(t, s, "unlink_documents", "Remove a document link created with link_documents. related links match either direction.", true, func(in LinkInput) string { return in.ProjectID }, unlinkDocuments)
	add(t, s, "get_document_links", "List a document's links: supersedes, superseded_by, implements, implemented_by (histories that carried out a decision), related.", false, func(in LinksQuery) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in LinksQuery) (any, error) {
		if _, err := getDocument(ctx, tx, a, DocumentRef{ProjectID: in.ProjectID, DocumentID: in.DocumentID}); err != nil {
			return nil, err
		}
		return documentLinks(ctx, tx, in.ProjectID, in.DocumentID)
	})
}
