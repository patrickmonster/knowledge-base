package kb

import (
	"context"
	"database/sql"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TransferOwnerInput hands a project to another active member. The caller
// retypes the project slug so a wrong project cannot be transferred by mistake.
type TransferOwnerInput struct {
	ProjectID         string `json:"project_id"`
	UserID            string `json:"user_id" jsonschema:"Current admin, editor, or viewer who becomes the owner"`
	ConfirmSlug       string `json:"confirm_slug" jsonschema:"The project slug, retyped to confirm"`
	PreviousOwnerRole string `json:"previous_owner_role,omitempty" jsonschema:"Role kept by the previous owner: admin, editor (default), or viewer"`
	WorkAreas         string `json:"work_areas,omitempty" jsonschema:"Work areas the previous owner owns: keep (default unless the previous owner becomes viewer) or transfer to the new owner. A viewer cannot own areas, so viewer always transfers"`
}

// transferProjectOwner moves ownership in one transaction: projects.owner_id
// and the owner member row change together, so there is always exactly one
// owner. It is a human decision: MCP keys (AI clients) are refused.
func transferProjectOwner(ctx context.Context, tx *sql.Tx, actor Identity, in TransferOwnerInput) (any, error) {
	if actor.KeyID != "" {
		return nil, problem("FORBIDDEN", "Ownership transfer requires the signed-in owner in the web console.")
	}
	if in.PreviousOwnerRole == "" {
		in.PreviousOwnerRole = "editor"
	}
	if in.PreviousOwnerRole != "admin" && in.PreviousOwnerRole != "editor" && in.PreviousOwnerRole != "viewer" {
		return nil, problem("INVALID_INPUT", "previous_owner_role must be admin, editor, or viewer.")
	}
	// Viewers cannot own work areas (projectEditor), so a previous owner who
	// stays as viewer hands their areas over; otherwise they keep them unless asked.
	if in.WorkAreas == "" {
		in.WorkAreas = "keep"
		if in.PreviousOwnerRole == "viewer" {
			in.WorkAreas = "transfer"
		}
	}
	if in.WorkAreas != "keep" && in.WorkAreas != "transfer" {
		return nil, problem("INVALID_INPUT", "work_areas must be keep or transfer.")
	}
	if in.WorkAreas == "keep" && in.PreviousOwnerRole == "viewer" {
		return nil, problem("INVALID_INPUT", "A viewer cannot own work areas: use work_areas transfer, or stay as admin or editor.")
	}
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, true); err != nil {
		return nil, err
	}
	if err := validID(in.UserID); err != nil {
		return nil, err
	}
	// Lock the project row so concurrent transfers or member changes serialize.
	var owner, slug string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id,slug FROM projects WHERE id=$1 FOR UPDATE`, in.ProjectID).Scan(&owner, &slug); err != nil {
		return nil, notFound(err)
	}
	if owner != actor.User.ID {
		return nil, problem("FORBIDDEN", "Only the project owner can transfer ownership.")
	}
	if in.ConfirmSlug != slug {
		return nil, problem("INVALID_INPUT", "confirm_slug must match the project slug.")
	}
	if in.UserID == actor.User.ID {
		return nil, problem("INVALID_INPUT", "You already own this project.")
	}
	var role string
	var disabled bool
	err := tx.QueryRowContext(ctx, `SELECT m.role,u.disabled FROM project_members m JOIN users u ON u.id=m.user_id
		WHERE m.project_id=$1 AND m.user_id=$2 FOR UPDATE OF m`, in.ProjectID, in.UserID).Scan(&role, &disabled)
	if err == sql.ErrNoRows || (err == nil && disabled) {
		return nil, problem("NOT_FOUND", "The new owner must be an active member of this project. Add them as admin, editor, or viewer first.")
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE projects SET owner_id=$2 WHERE id=$1`, in.ProjectID, in.UserID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_members SET role='owner' WHERE project_id=$1 AND user_id=$2`, in.ProjectID, in.UserID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_members SET role=$3 WHERE project_id=$1 AND user_id=$2`, in.ProjectID, actor.User.ID, in.PreviousOwnerRole); err != nil {
		return nil, err
	}
	var moved int64
	if in.WorkAreas == "transfer" {
		result, err := tx.ExecContext(ctx, `UPDATE work_areas SET owner_id=$3,updated_at=clock_timestamp() WHERE project_id=$1 AND owner_id=$2`, in.ProjectID, actor.User.ID, in.UserID)
		if err != nil {
			return nil, err
		}
		if moved, err = result.RowsAffected(); err != nil {
			return nil, err
		}
	}
	return map[string]any{"project_id": in.ProjectID, "owner_id": in.UserID, "previous_owner_id": actor.User.ID,
		"previous_owner_role": in.PreviousOwnerRole, "new_owner_previous_role": role,
		"work_areas": in.WorkAreas, "work_areas_moved": moved}, nil
}

func registerOwnershipTools(t Tools, s *Store) {
	add(t, s, "transfer_project_owner", "Web console only: the owner hands the project to an active member; the previous owner becomes admin, editor or viewer and keeps or hands over their work areas.", true, func(in TransferOwnerInput) string { return in.ProjectID }, transferProjectOwner)
	// Hidden from MCP clients; the web console calls it through /api/tools.
	tool := t["transfer_project_owner"]
	tool.Register = func(*mcp.Server, Identity) {}
	t["transfer_project_owner"] = tool
}
