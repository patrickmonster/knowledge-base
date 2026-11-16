package kb

import (
	"context"
	"database/sql"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Upstream projects: a downstream project follows an upstream project that
// holds shared source-of-truth knowledge (design mockups, product planning,
// API specs). One upstream serves many downstreams.
//
//   - The downstream's owner or admin requests the link; the upstream's owner
//     or admin approves it in the web console. Approval grants read access to
//     outsiders, so like owner rules it is a person's decision, not a key's.
//   - Members of an approved downstream read the upstream without joining it
//     (projectReadAccess) and get its current active knowledge in start_task
//     as upstream_sources.
//   - They cannot edit the upstream. Problems go to the upstream as issues
//     (create_issue with from_project_id), so the fix is made once, upstream,
//     and every downstream sees it on its next task.
//
// Only direct links count: an upstream's own upstreams are not followed.

// upstreamReaderRole is the Role of a project read through an upstream link
// rather than membership. It is never a write role: projectAccess(write) only
// ever returns member roles.
const upstreamReaderRole = "upstream_reader"

const maxUpstreams = 5

type ProjectUpstream struct {
	ProjectID           string  `json:"project_id"`
	ProjectSlug         string  `json:"project_slug"`
	ProjectName         string  `json:"project_name"`
	UpstreamProjectID   string  `json:"upstream_project_id"`
	UpstreamProjectSlug string  `json:"upstream_project_slug"`
	UpstreamProjectName string  `json:"upstream_project_name"`
	Status              string  `json:"status"`
	RequestedBy         *string `json:"requested_by"`
	RequestedByName     *string `json:"requested_by_name"`
	RequestedAt         string  `json:"requested_at"`
	ApprovedBy          *string `json:"approved_by"`
	ApprovedByName      *string `json:"approved_by_name"`
	ApprovedAt          *string `json:"approved_at"`
}

type ProjectUpstreams struct {
	ProjectID string `json:"project_id"`
	// Upstreams are the projects this project follows (read-only sources).
	Upstreams []ProjectUpstream `json:"upstreams"`
	// Downstreams are the projects following this project, pending requests
	// included. Only members of this project see them.
	Downstreams []ProjectUpstream `json:"downstreams"`
	Guidance    string            `json:"guidance"`
}

type UpstreamRequestInput struct {
	ProjectID         string `json:"project_id" jsonschema:"The downstream project that will follow the upstream; you must be its owner or admin"`
	UpstreamProjectID string `json:"upstream_project_id" jsonschema:"The upstream project holding the shared knowledge (design, planning, specs)"`
}

type UpstreamApproveInput struct {
	ProjectID           string `json:"project_id" jsonschema:"The upstream project; you must be its owner or admin"`
	DownstreamProjectID string `json:"downstream_project_id"`
}

type UpstreamRemoveInput struct {
	ProjectID      string `json:"project_id" jsonschema:"Your side of the link (downstream or upstream); you must be its owner or admin"`
	OtherProjectID string `json:"other_project_id" jsonschema:"The other side of the link"`
}

const upstreamSelect = `SELECT jsonb_build_object('project_id',l.project_id,'project_slug',d.slug,'project_name',d.name,
	'upstream_project_id',l.upstream_project_id,'upstream_project_slug',u.slug,'upstream_project_name',u.name,'status',l.status,
	'requested_by',l.requested_by,'requested_by_name',ur.name,'requested_at',l.requested_at,
	'approved_by',l.approved_by,'approved_by_name',ua.name,'approved_at',l.approved_at)
	FROM project_upstreams l JOIN projects d ON d.id=l.project_id JOIN projects u ON u.id=l.upstream_project_id
	LEFT JOIN users ur ON ur.id=l.requested_by LEFT JOIN users ua ON ua.id=l.approved_by`

const upstreamGuidance = "upstreams are read-only sources of truth for this project: start_task returns their current active knowledge as upstream_sources. Do not edit upstream documents from this project's work; report problems with create_issue(project_id=<upstream>, from_project_id=<this project>). Linking needs the upstream owner's or admin's approval in the web console."

func registerUpstreamTools(t Tools, s *Store) {
	add(t, s, "list_project_upstreams", "List the upstream projects this project follows (read-only sources of truth such as design or planning) and, for members, the downstream projects following it, pending requests included.", false, func(in ProjectInput) string { return in.ProjectID }, listProjectUpstreams)
	add(t, s, "request_project_upstream", "Owner or admin of the downstream: ask to follow an upstream project (design, planning, specs). The link stays pending until the upstream's owner or admin approves it in the web console; then this project's members can read the upstream and start_task returns its knowledge as upstream_sources. Only when the user asks.", true, func(in UpstreamRequestInput) string { return in.ProjectID }, requestProjectUpstream)
	add(t, s, "approve_project_upstream", "Web console only: the upstream's owner or admin approves a pending downstream, giving its members read access.", true, func(in UpstreamApproveInput) string { return in.ProjectID }, approveProjectUpstream)
	tool := t["approve_project_upstream"]
	tool.Register = func(*mcp.Server, Identity) {}
	t["approve_project_upstream"] = tool
	add(t, s, "remove_project_upstream", "Owner or admin of either side: remove an upstream link or reject a pending request. The downstream's members lose read access to the upstream at once.", true, func(in UpstreamRemoveInput) string { return in.ProjectID }, removeProjectUpstream)
}

// projectManager is projectAccess(write) limited to the owner and admins.
func projectManager(ctx context.Context, q Querier, userID, projectID string) (Project, error) {
	p, err := projectAccess(ctx, q, userID, projectID, true)
	if err != nil {
		return p, err
	}
	if p.Role != "owner" && p.Role != "admin" {
		return p, problem("FORBIDDEN", "Only the project owner or an admin can manage upstream links.")
	}
	return p, nil
}

func listProjectUpstreams(ctx context.Context, tx *sql.Tx, a Identity, in ProjectInput) (any, error) {
	p, err := projectReadAccess(ctx, tx, a.User.ID, in.ProjectID)
	if err != nil {
		return nil, err
	}
	out := ProjectUpstreams{ProjectID: in.ProjectID, Upstreams: []ProjectUpstream{}, Downstreams: []ProjectUpstream{}, Guidance: upstreamGuidance}
	if p.Role == upstreamReaderRole {
		// A reader sees only the links it reads through, not who else follows.
		out.Downstreams, err = many[ProjectUpstream](ctx, tx, upstreamSelect+` JOIN project_members m ON m.project_id=l.project_id AND m.user_id=$2
			WHERE l.upstream_project_id=$1 AND l.status='approved' ORDER BY d.name,d.id`, in.ProjectID, a.User.ID)
		return out, err
	}
	if out.Upstreams, err = many[ProjectUpstream](ctx, tx, upstreamSelect+` WHERE l.project_id=$1 ORDER BY u.name,u.id`, in.ProjectID); err != nil {
		return nil, err
	}
	out.Downstreams, err = many[ProjectUpstream](ctx, tx, upstreamSelect+` WHERE l.upstream_project_id=$1 ORDER BY l.status DESC,d.name,d.id`, in.ProjectID)
	return out, err
}

func requestProjectUpstream(ctx context.Context, tx *sql.Tx, a Identity, in UpstreamRequestInput) (any, error) {
	if _, err := projectManager(ctx, tx, a.User.ID, in.ProjectID); err != nil {
		return nil, err
	}
	if err := validID(in.UpstreamProjectID); err != nil {
		return nil, err
	}
	if in.UpstreamProjectID == in.ProjectID {
		return nil, problem("INVALID_INPUT", "A project cannot follow itself.")
	}
	// The upstream is named by ID, which its owner shares with the downstream.
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, in.UpstreamProjectID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, notFound(sql.ErrNoRows)
	}
	// Lock both projects' links in a fixed order so two opposite requests
	// cannot both pass the cycle check.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM projects WHERE id=ANY($1::uuid[]) ORDER BY id FOR NO KEY UPDATE`, "{"+in.ProjectID+","+in.UpstreamProjectID+"}"); err != nil {
		return nil, err
	}
	var reverse bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_upstreams WHERE project_id=$1 AND upstream_project_id=$2)`, in.UpstreamProjectID, in.ProjectID).Scan(&reverse); err != nil {
		return nil, err
	}
	if reverse {
		return nil, problem("CONFLICT", "That project already follows this one; two projects cannot follow each other.")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_upstreams WHERE project_id=$1 AND upstream_project_id<>$2`, in.ProjectID, in.UpstreamProjectID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= maxUpstreams {
		return nil, problem("INVALID_INPUT", "A project can follow at most 5 upstream projects.")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_upstreams(project_id,upstream_project_id,requested_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`,
		in.ProjectID, in.UpstreamProjectID, a.User.ID); err != nil {
		return nil, err
	}
	// A person who also manages the upstream approves their own request in the
	// web console in one step; a key never approves.
	if a.KeyID == "" {
		if _, err := projectManager(ctx, tx, a.User.ID, in.UpstreamProjectID); err == nil {
			if err := approveLink(ctx, tx, a, in.UpstreamProjectID, in.ProjectID); err != nil {
				return nil, err
			}
		}
	}
	return one[ProjectUpstream](ctx, tx, upstreamSelect+` WHERE l.project_id=$1 AND l.upstream_project_id=$2`, in.ProjectID, in.UpstreamProjectID)
}

func approveProjectUpstream(ctx context.Context, tx *sql.Tx, a Identity, in UpstreamApproveInput) (any, error) {
	if a.KeyID != "" {
		return nil, problem("FORBIDDEN", "Approve upstream links in the web console.")
	}
	if _, err := projectManager(ctx, tx, a.User.ID, in.ProjectID); err != nil {
		return nil, err
	}
	if err := validID(in.DownstreamProjectID); err != nil {
		return nil, err
	}
	if err := approveLink(ctx, tx, a, in.ProjectID, in.DownstreamProjectID); err != nil {
		return nil, err
	}
	return one[ProjectUpstream](ctx, tx, upstreamSelect+` WHERE l.project_id=$1 AND l.upstream_project_id=$2`, in.DownstreamProjectID, in.ProjectID)
}

func approveLink(ctx context.Context, tx *sql.Tx, a Identity, upstreamID, downstreamID string) error {
	res, err := tx.ExecContext(ctx, `UPDATE project_upstreams SET status='approved',approved_by=$3,approved_at=$4
		WHERE project_id=$1 AND upstream_project_id=$2 AND status='pending'`, downstreamID, upstreamID, a.User.ID, time.Now())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		var status string
		err := tx.QueryRowContext(ctx, `SELECT status FROM project_upstreams WHERE project_id=$1 AND upstream_project_id=$2`, downstreamID, upstreamID).Scan(&status)
		if err != nil {
			return notFound(err)
		}
	}
	return nil
}

func removeProjectUpstream(ctx context.Context, tx *sql.Tx, a Identity, in UpstreamRemoveInput) (any, error) {
	if _, err := projectManager(ctx, tx, a.User.ID, in.ProjectID); err != nil {
		return nil, err
	}
	if err := validID(in.OtherProjectID); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM project_upstreams WHERE (project_id=$1 AND upstream_project_id=$2) OR (project_id=$2 AND upstream_project_id=$1)`, in.ProjectID, in.OtherProjectID)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, notFound(sql.ErrNoRows)
	}
	return map[string]any{"project_id": in.ProjectID, "other_project_id": in.OtherProjectID, "removed": true}, nil
}

// readerVia returns the downstream projects through which userID may read
// projectID: approved links from projects userID is a member of.
func readerVia(ctx context.Context, q Querier, userID, projectID string) ([]string, error) {
	return many[string](ctx, q, `SELECT to_jsonb(l.project_id::text) FROM project_upstreams l
		JOIN project_members m ON m.project_id=l.project_id AND m.user_id=$2
		WHERE l.upstream_project_id=$1 AND l.status='approved' ORDER BY l.project_id FOR SHARE OF l,m`, projectID, userID)
}

// projectReadAccess is projectAccess for reading: members get their role, and
// members of an approved downstream get upstreamReaderRole. Use it only for
// reads of documents, search, issues and attachments — never for writes.
func projectReadAccess(ctx context.Context, q Querier, userID, projectID string) (Project, error) {
	p, err := projectAccess(ctx, q, userID, projectID, false)
	if err == nil || errorCode(err) != "NOT_FOUND" {
		return p, err
	}
	via, verr := readerVia(ctx, q, userID, projectID)
	if verr != nil {
		return p, verr
	}
	if len(via) == 0 {
		return p, err
	}
	p, err = one[Project](ctx, q, `SELECT to_jsonb(p)||jsonb_build_object('role',$2::text) FROM projects p WHERE p.id=$1`, projectID, upstreamReaderRole)
	return p, notFound(err)
}

// approvedUpstreams are the projects projectID follows, for start_task.
func approvedUpstreams(ctx context.Context, q Querier, projectID string) ([]SearchedProject, error) {
	return many[SearchedProject](ctx, q, `SELECT jsonb_build_object('id',p.id,'slug',p.slug,'name',p.name,'role',$2::text)
		FROM project_upstreams l JOIN projects p ON p.id=l.upstream_project_id
		WHERE l.project_id=$1 AND l.status='approved' ORDER BY p.created_at,p.id`, projectID, upstreamReaderRole)
}
