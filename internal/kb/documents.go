package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func indexDocument(ctx context.Context, tx *sql.Tx, d Document, reason string) error {
	// New chunk defaults enqueue local embedding generation in the same transaction.
	// The worker writes vectors only after this document revision commits.
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO revisions(document_id,revision,snapshot,reason,actor_id) VALUES($1,$2,$3,$4,$5)`, d.ID, d.Revision, raw, reason, d.UpdatedBy); err != nil {
		return err
	}
	return rebuildChunks(ctx, tx, d)
}

// rebuildChunks replaces a document's chunks from its current content. New chunks
// default to pending, so the embedding worker regenerates their vectors.
func rebuildChunks(ctx context.Context, tx *sql.Tx, d Document) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks WHERE document_id=$1", d.ID); err != nil {
		return err
	}
	title := strings.Join(Lexemes(d.Title+" "+strings.Join(d.Tags, " ")), " ")
	for position, content := range SplitChunks(IndexableText(d.Kind, d.Content)) {
		_, err := tx.ExecContext(ctx, `INSERT INTO chunks(id,document_id,position,content,search_vector)
		VALUES($1,$2,$3,$4,setweight(to_tsvector('simple',$5),'A')||setweight(to_tsvector('simple',$6),'B'))`, uuid.NewString(), d.ID, position, content, title, strings.Join(Lexemes(content), " "))
		if err != nil {
			return err
		}
	}
	return nil
}
func createDocument(ctx context.Context, tx *sql.Tx, actor Identity, in DocumentInput) (any, error) {
	if err := validateDocument(&in); err != nil {
		return nil, err
	}
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, true); err != nil {
		return nil, err
	}
	d, err := one[Document](ctx, tx, `WITH inserted AS (INSERT INTO documents(id,project_id,title,kind,content,tags,status,source_url,created_by,updated_by)
	VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$9) RETURNING *) SELECT to_jsonb(inserted) FROM inserted`,
		uuid.NewString(), in.ProjectID, in.Title, in.Kind, in.Content, in.Tags, in.Status, in.SourceURL, actor.User.ID)
	if err != nil {
		return nil, err
	}
	return d, indexDocument(ctx, tx, d, "Document created")
}

type DocumentRef struct {
	ProjectID  string `json:"project_id"`
	DocumentID string `json:"document_id"`
	Revision   int    `json:"revision,omitempty"`
}

func getDocument(ctx context.Context, tx *sql.Tx, actor Identity, in DocumentRef) (Document, error) {
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, false); err != nil {
		return Document{}, err
	}
	if err := validID(in.DocumentID); err != nil {
		return Document{}, err
	}
	d, err := one[Document](ctx, tx, "SELECT to_jsonb(d) FROM documents d WHERE id=$1 AND project_id=$2", in.DocumentID, in.ProjectID)
	if err != nil {
		return d, notFound(err)
	}
	if in.Revision == 0 {
		return d, nil
	}
	d, err = one[Document](ctx, tx, "SELECT snapshot FROM revisions WHERE document_id=$1 AND revision=$2", in.DocumentID, in.Revision)
	return d, notFound(err)
}

type UpdateInput struct {
	ProjectID        string        `json:"project_id"`
	DocumentID       string        `json:"document_id"`
	ExpectedRevision int           `json:"expected_revision"`
	Reason           string        `json:"reason"`
	Patch            DocumentPatch `json:"patch"`
}

func updateDocument(ctx context.Context, tx *sql.Tx, actor Identity, in UpdateInput) (any, error) {
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, true); err != nil {
		return nil, err
	}
	if err := validID(in.DocumentID); err != nil {
		return nil, err
	}
	if err := bounded("reason", in.Reason, 1, 1000); err != nil {
		return nil, err
	}
	d, err := one[Document](ctx, tx, "SELECT to_jsonb(d) FROM documents d WHERE id=$1 AND project_id=$2 FOR UPDATE", in.DocumentID, in.ProjectID)
	if err != nil {
		return nil, notFound(err)
	}
	if d.Revision != in.ExpectedRevision {
		return nil, problem("REVISION_CONFLICT", fmt.Sprintf("Current revision is %d. Read it, merge your change, then retry.", d.Revision))
	}
	p := in.Patch
	if p.Title == nil && p.Kind == nil && p.Content == nil && p.Tags == nil && p.Status == nil && p.SourceURL == nil {
		return nil, problem("INVALID_INPUT", "Provide at least one changed field.")
	}
	input := DocumentInput{ProjectID: d.ProjectID, Title: d.Title, Kind: d.Kind, Content: d.Content, Tags: d.Tags, Status: d.Status}
	if d.SourceURL != nil {
		input.SourceURL = *d.SourceURL
	}
	if p.Title != nil {
		input.Title = *p.Title
	}
	if p.Kind != nil {
		input.Kind = *p.Kind
	}
	if p.Content != nil {
		input.Content = *p.Content
	}
	if p.Tags != nil {
		input.Tags = *p.Tags
	}
	if p.Status != nil {
		input.Status = *p.Status
	}
	if p.SourceURL != nil {
		input.SourceURL = *p.SourceURL
	}
	if err := validateDocument(&input); err != nil {
		return nil, err
	}
	d, err = one[Document](ctx, tx, `WITH changed AS (UPDATE documents SET title=$1,kind=$2,content=$3,tags=$4,status=$5,source_url=NULLIF($6,''),
	revision=revision+1,updated_by=$7,updated_at=clock_timestamp() WHERE id=$8 RETURNING *) SELECT to_jsonb(changed) FROM changed`, input.Title, input.Kind, input.Content, input.Tags, input.Status, input.SourceURL, actor.User.ID, d.ID)
	if err != nil {
		return nil, err
	}
	return d, indexDocument(ctx, tx, d, in.Reason)
}

type ListInput struct {
	ProjectID     string   `json:"project_id"`
	Limit         int      `json:"limit,omitempty"`
	Offset        int      `json:"offset,omitempty"`
	Status        string   `json:"status,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Tags          []string `json:"tags,omitempty" jsonschema:"Documents must have every tag (case-insensitive); at most 10"`
	UpdatedAfter  string   `json:"updated_after,omitempty" jsonschema:"Last updated at or after: YYYY-MM-DD (Asia/Seoul start of day) or RFC3339"`
	UpdatedBefore string   `json:"updated_before,omitempty" jsonschema:"Last updated on or before: YYYY-MM-DD (Asia/Seoul, inclusive) or RFC3339 (exclusive)"`
	Author        string   `json:"author,omitempty" jsonschema:"User ID that created or last updated the document"`
	StaleOnly     bool     `json:"stale_only,omitempty" jsonschema:"Only active reference/brief documents that need re-checking"`
	StaleDays     int      `json:"stale_days,omitempty" jsonschema:"Days without updates before active reference/brief documents need re-checking; default 90, 1–3650"`
}

// DefaultStaleDays is how long an active reference or brief may go without an
// update before list_documents marks it needs_recheck.
const DefaultStaleDays = 90

// staleKinds hold facts that drift (references, project briefs). Decisions and
// histories record a point in time and are never marked stale.
var staleKinds = []string{"brief", "reference"}

// parseListTime accepts a date (Asia/Seoul) or an RFC3339 timestamp. Dates used
// as an upper bound include the whole day.
func parseListTime(label, value string, upper bool) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return &t, nil
	}
	t, err := time.ParseInLocation("2006-01-02", value, seoul)
	if err != nil {
		return nil, problem("INVALID_INPUT", label+" must be YYYY-MM-DD or an RFC3339 timestamp.")
	}
	if upper {
		t = t.AddDate(0, 0, 1)
	}
	return &t, nil
}

func listDocuments(ctx context.Context, tx *sql.Tx, actor Identity, in ListInput) ([]map[string]any, error) {
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, false); err != nil {
		return nil, err
	}
	limit, err := pagination(in.Limit, in.Offset)
	if err != nil {
		return nil, err
	}
	if in.Status != "" && !validStatus(in.Status) {
		return nil, problem("INVALID_INPUT", "Unknown status.")
	}
	if in.Kind != "" && !validKind(in.Kind) {
		return nil, problem("INVALID_INPUT", "Unknown kind.")
	}
	tags := normalizeTags(in.Tags)
	if len(tags) > 10 {
		return nil, problem("INVALID_INPUT", "Filter by at most 10 tags.")
	}
	for i, tag := range tags {
		if utf8.RuneCountInString(tag) > 50 {
			return nil, problem("INVALID_INPUT", "Each tag filter must have at most 50 characters.")
		}
		tags[i] = strings.ToLower(tag)
	}
	if in.Author != "" {
		if err := validID(in.Author); err != nil {
			return nil, problem("INVALID_INPUT", "author must be a user ID (canonical lowercase UUID).")
		}
	}
	after, err := parseListTime("updated_after", in.UpdatedAfter, false)
	if err != nil {
		return nil, err
	}
	before, err := parseListTime("updated_before", in.UpdatedBefore, true)
	if err != nil {
		return nil, err
	}
	if after != nil && before != nil && !after.Before(*before) {
		return nil, problem("INVALID_INPUT", "updated_after must be earlier than updated_before.")
	}
	staleDays := in.StaleDays
	if staleDays == 0 {
		staleDays = DefaultStaleDays
	}
	if staleDays < 1 || staleDays > 3650 {
		return nil, problem("INVALID_INPUT", "stale_days must be 1–3650.")
	}
	return many[map[string]any](ctx, tx, `WITH listed AS (
		SELECT d.*, d.kind=ANY($11::text[]) AND d.status='active' AND d.updated_at < now()-make_interval(days=>$10::int) AS needs_recheck
		FROM documents d WHERE d.project_id=$1
		AND ($4='' OR d.status=$4) AND ($5='' OR d.kind=$5)
		AND NOT EXISTS (SELECT 1 FROM unnest($6::text[]) want WHERE NOT EXISTS (SELECT 1 FROM unnest(d.tags) have WHERE lower(have)=want))
		AND ($7::timestamptz IS NULL OR d.updated_at >= $7::timestamptz)
		AND ($8::timestamptz IS NULL OR d.updated_at < $8::timestamptz)
		AND ($9='' OR d.created_by::text=$9 OR d.updated_by::text=$9))
	SELECT (to_jsonb(l)-'content')||jsonb_build_object('days_since_update',floor(extract(epoch FROM now()-l.updated_at)/86400)::int,'stale_after_days',$10::int)
	FROM listed l WHERE NOT $12::bool OR l.needs_recheck
	ORDER BY l.updated_at DESC,l.id LIMIT $2 OFFSET $3`,
		in.ProjectID, limit, in.Offset, in.Status, in.Kind, tags, after, before, in.Author, staleDays, staleKinds, in.StaleOnly)
}

type SearchInput struct {
	ProjectID string `json:"project_id"`
	Query     string `json:"query"`
	Limit     int    `json:"limit,omitempty"`
	Status    string `json:"status,omitempty"`
	Mode      string `json:"mode,omitempty" jsonschema:"hybrid (default when configured), semantic, or lexical"`
}

func searchLexical(ctx context.Context, tx *sql.Tx, actor Identity, in SearchInput) ([]SearchHit, error) {
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, false); err != nil {
		return nil, err
	}
	limit, err := pagination(in.Limit, 0)
	if err != nil {
		return nil, err
	}
	if err = bounded("query", in.Query, 1, 1000); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if !validStatus(in.Status) {
		return nil, problem("INVALID_INPUT", "Unknown status.")
	}
	query := QueryText(in.Query)
	if query == "" {
		return []SearchHit{}, nil
	}
	hits, err := many[SearchHit](ctx, tx, `WITH matched AS (SELECT d.id AS document_id,d.project_id,d.title,d.kind,d.status,d.revision,d.source_url,d.updated_by,d.updated_at,
	c.position,c.content AS excerpt,ts_rank_cd(c.search_vector,to_tsquery('simple',$2)) AS score,
	row_number() OVER(PARTITION BY d.id ORDER BY ts_rank_cd(c.search_vector,to_tsquery('simple',$2)) DESC,c.position) AS n
	FROM documents d JOIN chunks c ON c.document_id=d.id WHERE d.project_id=$1 AND d.status=$3 AND c.search_vector@@to_tsquery('simple',$2))
	SELECT to_jsonb(matched)-'n' FROM matched WHERE n=1 ORDER BY score DESC,updated_at DESC,document_id LIMIT $4`, in.ProjectID, query, in.Status, limit)
	for i := range hits {
		hits[i].Citation = fmt.Sprintf("kb://%s/documents/%s?revision=%d#chunk-%d", in.ProjectID, hits[i].DocumentID, hits[i].Revision, hits[i].Position)
	}
	return hits, err
}
