package kb

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Project export and import (프로젝트 단위 내보내기·가져오기).
//
// Export is a backup aid and a migration path: JSON holds the project, every
// document (optionally with all revisions), links, to-dos, work areas and
// issues; Markdown is a zip of readable documents plus export.json for
// re-import. Import (JSON or that zip) creates documents, links and to-dos in
// an existing project. Work areas and issues reference users of the source
// server, so they are exported for reference but not imported.
//
// To-dos keep their board cell, order, category, progress and due date. What
// ties a to-do to the source server stays behind: the assignee (a user there),
// linked task runs and pull requests (task runs are not exported) and linked
// issues (not imported). The export still shows those for reference.

// MaxImportBytes bounds the uploaded JSON or zip; maxImportUnzipped bounds
// export.json inside a zip (zip bomb guard).
const (
	exportFormat       = "knowledge-base-export"
	exportVersion      = 1
	maxExportDocuments = 5000
	MaxImportBytes     = 32 << 20
	maxImportUnzipped  = 64 << 20
)

type ProjectExport struct {
	Format     string           `json:"format"`
	Version    int              `json:"version"`
	ExportedAt string           `json:"exported_at"`
	Source     string           `json:"source,omitempty"`
	Project    ExportedProject  `json:"project"`
	Documents  []ExportDocument `json:"documents"`
	Links      []ExportLink     `json:"links"`
	Todos      []Todo           `json:"todos"`
	WorkAreas  []WorkArea       `json:"work_areas"`
	Issues     []Issue          `json:"issues"`
}

type ExportedProject struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ExportDocument struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Kind          string           `json:"kind"`
	Content       string           `json:"content"`
	Tags          []string         `json:"tags"`
	Status        string           `json:"status"`
	SourceURL     *string          `json:"source_url"`
	Revision      int              `json:"revision"`
	CreatedAt     string           `json:"created_at"`
	UpdatedAt     string           `json:"updated_at"`
	CreatedByName string           `json:"created_by_name,omitempty"`
	UpdatedByName string           `json:"updated_by_name,omitempty"`
	Revisions     []ExportRevision `json:"revisions,omitempty"`
}

type ExportRevision struct {
	DocumentID string          `json:"document_id,omitempty"`
	Revision   int             `json:"revision"`
	Reason     string          `json:"reason"`
	ActorName  string          `json:"actor_name"`
	CreatedAt  string          `json:"created_at"`
	Snapshot   json.RawMessage `json:"snapshot"`
}

type ExportLink struct {
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
	Relation  string `json:"relation"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

type ImportResult struct {
	ProjectID        string   `json:"project_id"`
	SourceProject    string   `json:"source_project"`
	DocumentsCreated int      `json:"documents_created"`
	DocumentsSkipped int      `json:"documents_skipped"`
	LinksCreated     int      `json:"links_created"`
	LinksSkipped     int      `json:"links_skipped"`
	TodosCreated     int      `json:"todos_created"`
	TodosSkipped     int      `json:"todos_skipped"`
	WorkAreasIgnored int      `json:"work_areas_ignored"`
	IssuesIgnored    int      `json:"issues_ignored"`
	RevisionsIgnored int      `json:"revisions_ignored"`
	Warnings         []string `json:"warnings"`
}

func (s *Store) ExportProject(ctx context.Context, id Identity, projectID string, revisions bool) (ProjectExport, error) {
	// Documents, revisions, links and issues are read by separate queries;
	// REPEATABLE READ makes them one consistent point-in-time view.
	ctx = withSnapshot(auditInput(ctx, map[string]any{"format": "export"}))
	out, err := s.Perform(ctx, id, "export_project", projectID, false, false, func(tx *sql.Tx) (any, error) {
		return loadProjectExport(ctx, tx, id, projectID, revisions)
	})
	if err != nil {
		return ProjectExport{}, err
	}
	return out.(ProjectExport), nil
}

func loadProjectExport(ctx context.Context, tx *sql.Tx, id Identity, projectID string, revisions bool) (ProjectExport, error) {
	p, err := projectAccess(ctx, tx, id.User.ID, projectID, false)
	if err != nil {
		return ProjectExport{}, err
	}
	out := ProjectExport{Format: exportFormat, Version: exportVersion, ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Project: ExportedProject{ID: p.ID, Slug: p.Slug, Name: p.Name, Description: p.Description}}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM documents WHERE project_id=$1`, projectID).Scan(&count); err != nil {
		return out, err
	}
	if count > maxExportDocuments {
		return out, problem("INVALID_INPUT", fmt.Sprintf("Projects with more than %d documents cannot be exported in one file.", maxExportDocuments))
	}
	if out.Documents, err = many[ExportDocument](ctx, tx, `SELECT to_jsonb(d)||jsonb_build_object('created_by_name',uc.name,'updated_by_name',uu.name)
		FROM documents d JOIN users uc ON uc.id=d.created_by JOIN users uu ON uu.id=d.updated_by
		WHERE d.project_id=$1 ORDER BY d.created_at,d.id`, projectID); err != nil {
		return out, err
	}
	if revisions {
		rows, err := many[ExportRevision](ctx, tx, `SELECT jsonb_build_object('document_id',r.document_id,'revision',r.revision,'reason',r.reason,
			'actor_name',u.name,'created_at',r.created_at,'snapshot',r.snapshot)
			FROM revisions r JOIN documents d ON d.id=r.document_id JOIN users u ON u.id=r.actor_id
			WHERE d.project_id=$1 ORDER BY r.document_id,r.revision`, projectID)
		if err != nil {
			return out, err
		}
		by := map[string][]ExportRevision{}
		for _, r := range rows {
			doc := r.DocumentID
			r.DocumentID = ""
			by[doc] = append(by[doc], r)
		}
		for i := range out.Documents {
			out.Documents[i].Revisions = by[out.Documents[i].ID]
		}
	}
	if out.Links, err = many[ExportLink](ctx, tx, `SELECT jsonb_build_object('source_id',source_id,'target_id',target_id,'relation',relation,'note',note,'created_at',created_at)
		FROM document_links WHERE project_id=$1 ORDER BY created_at,source_id,target_id`, projectID); err != nil {
		return out, err
	}
	if out.Todos, err = many[Todo](ctx, tx, todoSelect+` WHERE t.project_id=$1 ORDER BY t.created_at,t.id`, projectID); err != nil {
		return out, err
	}
	if out.WorkAreas, err = many[WorkArea](ctx, tx, workAreaSelect+` WHERE w.project_id=$1 ORDER BY lower(w.name)`, projectID); err != nil {
		return out, err
	}
	if out.Issues, err = many[Issue](ctx, tx, issueSelect+` WHERE i.project_id=$1 ORDER BY i.created_at,i.id`, projectID); err != nil {
		return out, err
	}
	comments, err := many[IssueComment](ctx, tx, `SELECT to_jsonb(c)||jsonb_build_object('author_name',u.name)
		FROM issue_comments c JOIN issues i ON i.id=c.issue_id JOIN users u ON u.id=c.author_id
		WHERE i.project_id=$1 ORDER BY c.created_at,c.id`, projectID)
	if err != nil {
		return out, err
	}
	byIssue := map[string][]IssueComment{}
	for _, c := range comments {
		byIssue[c.IssueID] = append(byIssue[c.IssueID], c)
	}
	for i := range out.Issues {
		out.Issues[i].Comments = byIssue[out.Issues[i].ID]
	}
	return out, nil
}

// safeFileName keeps letters, digits, '-' and '_' so titles become portable
// file names inside the zip.
func safeFileName(title string, maxRunes int) string {
	var b strings.Builder
	dash := false
	n := 0
	for _, r := range title {
		if n >= maxRunes {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
			dash = false
			n++
		} else if !dash && b.Len() > 0 {
			b.WriteRune('-')
			dash = true
			n++
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "document"
	}
	return name
}

func frontMatterValue(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// renderExportMarkdown writes one Markdown file with JSON-quoted front matter
// (valid YAML) per document.
func renderExportMarkdown(d ExportDocument) string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, kv := range [][2]any{{"id", d.ID}, {"title", d.Title}, {"kind", d.Kind}, {"status", d.Status}, {"tags", d.Tags}, {"revision", d.Revision},
		{"created_at", d.CreatedAt}, {"updated_at", d.UpdatedAt}, {"source_url", d.SourceURL}} {
		fmt.Fprintf(&b, "%s: %s\n", kv[0], frontMatterValue(kv[1]))
	}
	b.WriteString("---\n\n")
	b.WriteString(d.Content)
	if !strings.HasSuffix(d.Content, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func renderIssueMarkdown(i Issue) string {
	name := func(p *string) string {
		if p == nil {
			return "—"
		}
		return *p
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n- 종류: %s\n- 상태: %s\n- 영역: %s\n- 담당자: %s\n- 작성: %s · %s\n\n## 내용\n\n%s\n", i.Title, i.Kind, i.Status, name(i.AreaName), name(i.AssigneeName), i.CreatedByName, i.CreatedAt, i.Body)
	if i.Answer != "" {
		fmt.Fprintf(&b, "\n## 답변 (%s)\n\n%s\n", name(i.AnsweredByName), i.Answer)
	}
	if i.Resolution != "" {
		fmt.Fprintf(&b, "\n## 해결 메모\n\n%s\n", i.Resolution)
	}
	for _, c := range i.Comments {
		fmt.Fprintf(&b, "\n---\n\n**%s** · %s\n\n%s\n", c.AuthorName, c.CreatedAt, c.Body)
	}
	return b.String()
}

// exportZip builds the Markdown export: README index, documents by kind,
// issues, and export.json (without revisions) for re-import.
func exportZip(data ProjectExport) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, content)
		return err
	}
	var index strings.Builder
	fmt.Fprintf(&index, "# %s (%s)\n\n내보낸 시각: %s\n\n%s\n\n| 종류 | 상태 | 제목 | 파일 |\n| --- | --- | --- | --- |\n", data.Project.Name, data.Project.Slug, data.ExportedAt, data.Project.Description)
	docs := append([]ExportDocument{}, data.Documents...)
	sort.SliceStable(docs, func(i, j int) bool { return docs[i].Kind < docs[j].Kind })
	used := map[string]bool{}
	for _, d := range docs {
		file := path.Join("documents", d.Kind, safeFileName(d.Title, 60)+"-"+d.ID[:8]+".md")
		for n := 2; used[file]; n++ {
			file = path.Join("documents", d.Kind, fmt.Sprintf("%s-%s-%d.md", safeFileName(d.Title, 60), d.ID[:8], n))
		}
		used[file] = true
		if err := write(file, renderExportMarkdown(d)); err != nil {
			return nil, err
		}
		fmt.Fprintf(&index, "| %s | %s | %s | [%s](%s) |\n", d.Kind, d.Status, strings.ReplaceAll(d.Title, "|", "\\|"), path.Base(file), file)
	}
	if len(data.Todos) > 0 {
		if err := write("todos.md", renderTodosMarkdown(data.Todos)); err != nil {
			return nil, err
		}
		fmt.Fprintf(&index, "\n## 할 일\n\n[todos.md](todos.md) — %d건\n", len(data.Todos))
	}
	if len(data.Issues) > 0 {
		index.WriteString("\n## 이슈 · 질의\n\n")
		for _, i := range data.Issues {
			file := path.Join("issues", i.ID[:8]+"-"+safeFileName(i.Title, 60)+".md")
			if err := write(file, renderIssueMarkdown(i)); err != nil {
				return nil, err
			}
			fmt.Fprintf(&index, "- [%s] [%s](%s)\n", i.Status, strings.ReplaceAll(i.Title, "]", "\\]"), file)
		}
	}
	index.WriteString("\n`export.json`은 이 프로젝트를 다른 서버나 프로젝트로 가져올 때 사용합니다(문서 버전 기록 제외).\n")
	if err := write("README.md", index.String()); err != nil {
		return nil, err
	}
	plain := data
	plain.Documents = make([]ExportDocument, len(data.Documents))
	for i, d := range data.Documents {
		d.Revisions = nil
		plain.Documents[i] = d
	}
	raw, err := json.MarshalIndent(plain, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := write("export.json", string(raw)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderTodosMarkdown lists the board for reading: one table per status, in
// board order (priority, then position).
func renderTodosMarkdown(todos []Todo) string {
	cell := func(s string) string {
		return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", "\\|")
	}
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	list := append([]Todo{}, todos...)
	sort.SliceStable(list, func(i, j int) bool {
		if rank[list[i].Priority] != rank[list[j].Priority] {
			return rank[list[i].Priority] < rank[list[j].Priority]
		}
		return list[i].Position < list[j].Position
	})
	var b strings.Builder
	b.WriteString("# 할 일\n")
	for _, status := range todoStatuses {
		wrote := false
		for _, t := range list {
			if t.Status != status {
				continue
			}
			if !wrote {
				fmt.Fprintf(&b, "\n## %s\n\n| 우선순위 | 제목 | 분류 | 진행률 | 담당자 | 마감일 | 내용 |\n| --- | --- | --- | --- | --- | --- | --- |\n", status)
				wrote = true
			}
			progress := ""
			if t.Progress != nil {
				progress = fmt.Sprintf("%d%%", *t.Progress)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", t.Priority, cell(t.Title), cell(t.Category), progress, cell(t.AssigneeName), t.DueDate, cell(truncateRunes(t.Body, 300)))
		}
	}
	return b.String()
}

// parseImport accepts export JSON or a Markdown export zip containing export.json.
func parseImport(body []byte, zipped bool) (ProjectExport, error) {
	var data ProjectExport
	if zipped {
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return data, problem("INVALID_INPUT", "The file is not a valid zip archive.")
		}
		var found *zip.File
		for _, f := range zr.File {
			if f.Name == "export.json" {
				found = f
				break
			}
		}
		if found == nil {
			return data, problem("INVALID_INPUT", "The zip has no export.json; use a Markdown export from this server.")
		}
		rc, err := found.Open()
		if err != nil {
			return data, problem("INVALID_INPUT", "export.json could not be read.")
		}
		body, err = io.ReadAll(io.LimitReader(rc, maxImportUnzipped+1))
		rc.Close()
		if err != nil {
			return data, problem("INVALID_INPUT", "export.json could not be read.")
		}
		if len(body) > maxImportUnzipped {
			return data, problem("INVALID_INPUT", "export.json is too large.")
		}
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return data, problem("INVALID_INPUT", "The file is not valid export JSON.")
	}
	if data.Format != exportFormat || data.Version != exportVersion {
		return data, problem("INVALID_INPUT", fmt.Sprintf("Unsupported export: expected format %s version %d.", exportFormat, exportVersion))
	}
	if len(data.Documents) > maxExportDocuments {
		return data, problem("INVALID_INPUT", fmt.Sprintf("At most %d documents can be imported at once.", maxExportDocuments))
	}
	if len(data.Todos) > maxProjectTodos {
		return data, problem("INVALID_INPUT", fmt.Sprintf("At most %d to-dos can be imported at once.", maxProjectTodos))
	}
	return data, nil
}

func (s *Store) ImportProject(ctx context.Context, id Identity, projectID string, data ProjectExport) (ImportResult, error) {
	ctx = auditInput(ctx, map[string]any{"format": "import"})
	out, err := s.Perform(ctx, id, "import_project", projectID, true, false, func(tx *sql.Tx) (any, error) {
		return importProject(ctx, tx, id, projectID, data)
	})
	if err != nil {
		return ImportResult{}, err
	}
	return out.(ImportResult), nil
}

func importProject(ctx context.Context, tx *sql.Tx, a Identity, projectID string, data ProjectExport) (ImportResult, error) {
	result := ImportResult{ProjectID: projectID, SourceProject: data.Project.Slug, Warnings: []string{}}
	if a.KeyID != "" {
		return result, problem("FORBIDDEN", "Import requires a signed-in person in the web console.")
	}
	p, err := projectAccess(ctx, tx, a.User.ID, projectID, true)
	if err != nil {
		return result, err
	}
	if p.OwnerID != a.User.ID {
		return result, problem("FORBIDDEN", "Only the project owner can import into a project.")
	}
	// Serialise imports and link writers of this project.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('document_links:'||$1::text,0))`, projectID); err != nil {
		return result, err
	}
	source := data.Project.Slug
	if source == "" {
		source = "unknown"
	}
	type mapped struct{ id, kind string }
	ids := map[string]mapped{}
	for n, d := range data.Documents {
		label := fmt.Sprintf("documents[%d]", n)
		if err := validID(d.ID); err != nil {
			return result, problem("INVALID_INPUT", label+": id must be a canonical lowercase UUID.")
		}
		if _, dup := ids[d.ID]; dup {
			return result, problem("INVALID_INPUT", label+": duplicate document id.")
		}
		in := DocumentInput{ProjectID: projectID, Title: d.Title, Kind: d.Kind, Content: d.Content, Tags: d.Tags, Status: d.Status}
		if d.SourceURL != nil {
			in.SourceURL = *d.SourceURL
		}
		if err := validateDocument(&in); err != nil {
			var known *Error
			msg := "invalid document"
			if errors.As(err, &known) {
				msg = known.Message
			}
			return result, problem("INVALID_INPUT", label+" ("+truncateRunes(d.Title, 60)+"): "+msg)
		}
		result.RevisionsIgnored += max(len(d.Revisions)-1, 0)
		// Already present: the original itself (same server) or an earlier import.
		var existing mapped
		err := tx.QueryRowContext(ctx, `SELECT id::text,kind FROM documents WHERE project_id=$1 AND (id=$2 OR imported_from=$2) LIMIT 1`, projectID, d.ID).Scan(&existing.id, &existing.kind)
		if err == nil {
			ids[d.ID] = existing
			result.DocumentsSkipped++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		doc, err := one[Document](ctx, tx, `WITH inserted AS (INSERT INTO documents(id,project_id,title,kind,content,tags,status,source_url,created_by,updated_by,imported_from)
			VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$9,$10) RETURNING *) SELECT to_jsonb(inserted) FROM inserted`,
			uuid.NewString(), projectID, in.Title, in.Kind, in.Content, in.Tags, in.Status, in.SourceURL, a.User.ID, d.ID)
		if err != nil {
			return result, err
		}
		reason := fmt.Sprintf("가져오기: %s 프로젝트 문서 %s (버전 %d, %s 내보냄)", source, d.ID, d.Revision, data.ExportedAt)
		if err := indexDocument(ctx, tx, doc, truncateRunes(reason, 900)); err != nil {
			return result, err
		}
		ids[d.ID] = mapped{doc.ID, doc.Kind}
		result.DocumentsCreated++
	}
	for _, l := range data.Links {
		src, okS := ids[l.SourceID]
		dst, okT := ids[l.TargetID]
		note := strings.TrimSpace(l.Note)
		valid := okS && okT && src.id != dst.id && validRelation(l.Relation) && bounded("note", note, 0, 500) == nil
		switch {
		case !valid:
		case l.Relation == relationSupersedes:
			valid = src.kind == dst.kind && src.kind != "history"
			if valid {
				var cycle bool
				if err := tx.QueryRowContext(ctx, `WITH RECURSIVE chain(id) AS (
					SELECT target_id FROM document_links WHERE source_id=$1 AND relation='supersedes'
					UNION SELECT l.target_id FROM document_links l JOIN chain c ON l.source_id=c.id AND l.relation='supersedes'
					) SELECT EXISTS(SELECT 1 FROM chain WHERE id=$2)`, dst.id, src.id).Scan(&cycle); err != nil {
					return result, err
				}
				valid = !cycle
			}
		case l.Relation == relationImplements:
			valid = src.kind == "history" && (dst.kind == "decision" || dst.kind == "brief")
		}
		if !valid {
			result.LinksSkipped++
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO document_links(project_id,source_id,target_id,relation,note,created_by)
			SELECT $1::uuid,$2::uuid,$3::uuid,$4::text,$5::text,$6::uuid
			WHERE NOT ($4::text='related' AND EXISTS(SELECT 1 FROM document_links WHERE relation='related' AND source_id=$3::uuid AND target_id=$2::uuid))
			ON CONFLICT DO NOTHING`, projectID, src.id, dst.id, l.Relation, note, a.User.ID)
		if err != nil {
			return result, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			result.LinksCreated++
		} else {
			result.LinksSkipped++
		}
	}
	if err := importTodos(ctx, tx, a, projectID, data.Todos, &result); err != nil {
		return result, err
	}
	result.WorkAreasIgnored = len(data.WorkAreas)
	result.IssuesIgnored = len(data.Issues)
	if result.WorkAreasIgnored+result.IssuesIgnored > 0 {
		result.Warnings = append(result.Warnings, "공통 작업 영역과 이슈·질의는 원본 서버의 사용자를 참조하므로 가져오지 않았습니다. 내보낸 파일에서 확인하세요.")
	}
	if result.RevisionsIgnored > 0 {
		result.Warnings = append(result.Warnings, "문서의 이전 버전 기록은 가져오지 않고 현재 내용만 새 문서의 첫 버전으로 저장했습니다.")
	}
	return result, nil
}

// importTodos creates the export's to-dos in the target project. A to-do
// already there (the original itself or an earlier import) is skipped, so
// changes made after the first import are never overwritten.
func importTodos(ctx context.Context, tx *sql.Tx, a Identity, projectID string, todos []Todo, result *ImportResult) error {
	if len(todos) == 0 {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM todos WHERE project_id=$1`, projectID).Scan(&count); err != nil {
		return err
	}
	seen := map[string]bool{}
	var unassigned, unlinked int
	for n, t := range todos {
		label := fmt.Sprintf("todos[%d]", n)
		if err := validID(t.ID); err != nil {
			return problem("INVALID_INPUT", label+": id must be a canonical lowercase UUID.")
		}
		if seen[t.ID] {
			return problem("INVALID_INPUT", label+": duplicate to-do id.")
		}
		seen[t.ID] = true
		in := SaveTodoInput{Title: t.Title, Body: t.Body, Status: t.Status, Priority: t.Priority, Category: t.Category, Progress: t.Progress, DueDate: t.DueDate}
		if err := validateTodo(&in); err != nil {
			var known *Error
			msg := "invalid to-do"
			if errors.As(err, &known) {
				msg = known.Message
			}
			return problem("INVALID_INPUT", label+" ("+truncateRunes(t.Title, 60)+"): "+msg)
		}
		if math.IsNaN(t.Position) || math.IsInf(t.Position, 0) {
			return problem("INVALID_INPUT", label+": position must be a finite number.")
		}
		completedAt := ""
		if in.Status == "completed" && t.CompletedAt != "" {
			at, err := time.Parse(time.RFC3339Nano, t.CompletedAt)
			if err != nil {
				return problem("INVALID_INPUT", label+": completed_at must be an RFC 3339 time.")
			}
			completedAt = at.UTC().Format(time.RFC3339Nano)
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM todos WHERE project_id=$1 AND (id=$2 OR imported_from=$2))`, projectID, t.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			result.TodosSkipped++
			continue
		}
		if count >= maxProjectTodos {
			return problem("QUOTA_EXCEEDED", fmt.Sprintf("A project can hold at most %d to-dos; the import would exceed it.", maxProjectTodos))
		}
		count++
		if _, err := tx.ExecContext(ctx, `INSERT INTO todos(id,project_id,title,body,status,priority,category,progress,due_date,position,created_by,updated_by,completed_at,imported_from)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::date,$10,$11,$11,
				CASE WHEN $5<>'completed' THEN NULL WHEN $12='' THEN now() ELSE $12::timestamptz END,$13)`,
			uuid.NewString(), projectID, in.Title, in.Body, in.Status, in.Priority, in.Category, in.Progress, in.DueDate, t.Position, a.User.ID, completedAt, t.ID); err != nil {
			return err
		}
		result.TodosCreated++
		if t.AssigneeID != "" {
			unassigned++
		}
		if t.TaskCount+t.OpenIssueCount+len(t.PullRequests) > 0 {
			unlinked++
		}
	}
	if unassigned > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("할 일 %d건은 담당자가 원본 서버의 사용자여서 담당자 없이 가져왔습니다.", unassigned))
	}
	if unlinked > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("할 일 %d건에 연결돼 있던 작업·PR·질의는 가져오지 않았습니다. 내보낸 파일에서 확인하세요.", unlinked))
	}
	return nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// --- HTTP ----------------------------------------------------------------------

// sessionIdentity authenticates a web console request whose response is not
// the usual JSON envelope (downloads) or whose body is not JSON (imports).
func (a *App) sessionIdentity(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	cookie, err := r.Cookie("kb_session")
	if err != nil {
		a.fail(w, problem("UNAUTHORIZED", "Please sign in."))
		return Identity{}, false
	}
	id, err := a.Store.Session(r.Context(), cookie.Value)
	if err != nil {
		a.fail(w, err)
		return Identity{}, false
	}
	if r.Method != http.MethodGet {
		if !a.origin(w, r) {
			return Identity{}, false
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(id.CSRF)) != 1 {
			a.fail(w, problem("FORBIDDEN", "Invalid CSRF token."))
			return Identity{}, false
		}
	}
	if err := a.Store.RateLimit(r.Context(), "web:"+id.User.ID, 240, 60); err != nil {
		a.fail(w, err)
		return Identity{}, false
	}
	return id, true
}

func attachmentHeader(w http.ResponseWriter, filename, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
}

func (a *App) projectExport(w http.ResponseWriter, r *http.Request) {
	id, ok := a.sessionIdentity(w, r)
	if !ok {
		return
	}
	if err := a.Store.RateLimit(r.Context(), "export:"+id.User.ID, 10, 60); err != nil {
		a.fail(w, err)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "markdown" {
		a.fail(w, problem("INVALID_INPUT", "format must be json or markdown."))
		return
	}
	data, err := a.Store.ExportProject(r.Context(), id, r.PathValue("id"), format == "json" && r.URL.Query().Get("revisions") != "false")
	if err != nil {
		a.fail(w, err)
		return
	}
	data.Source = a.Config.PublicURL
	stamp := time.Now().In(seoul).Format("20060102-1504")
	if format == "markdown" {
		raw, err := exportZip(data)
		if err != nil {
			a.fail(w, err)
			return
		}
		attachmentHeader(w, data.Project.Slug+"-"+stamp+".zip", "application/zip")
		w.Write(raw)
		return
	}
	attachmentHeader(w, data.Project.Slug+"-"+stamp+".json", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

func (a *App) projectImport(w http.ResponseWriter, r *http.Request) {
	id, ok := a.sessionIdentity(w, r)
	if !ok {
		return
	}
	if err := a.Store.RateLimit(r.Context(), "import:"+id.User.ID, 5, 300); err != nil {
		a.fail(w, err)
		return
	}
	media := strings.Split(r.Header.Get("Content-Type"), ";")[0]
	if media != "application/json" && media != "application/zip" {
		a.fail(w, problem("INVALID_INPUT", "Upload export JSON (application/json) or a Markdown export zip (application/zip)."))
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.fail(w, problem("INVALID_INPUT", fmt.Sprintf("The file must be at most %d MiB.", MaxImportBytes>>20)))
		return
	}
	data, err := parseImport(body, media == "application/zip")
	if err != nil {
		a.fail(w, err)
		return
	}
	out, err := a.Store.ImportProject(r.Context(), id, r.PathValue("id"), data)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.json(w, out)
}

func (a *App) agentsDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := a.sessionIdentity(w, r)
	if !ok {
		return
	}
	out, err := a.Tools["get_agents_template"].Call(r.Context(), id, json.RawMessage(`{"project_id":`+frontMatterValue(r.PathValue("id"))+`}`))
	if err != nil {
		a.fail(w, err)
		return
	}
	attachmentHeader(w, "AGENTS.md", "text/markdown; charset=utf-8")
	io.WriteString(w, out.(AgentsTemplate).Content)
}
