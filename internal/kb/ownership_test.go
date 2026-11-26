package kb

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTransferProjectOwner(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "owner-next@example.com")
	viewer := inviteAs(t, s, admin, "owner-viewer@example.com")
	outsider := inviteAs(t, s, admin, "owner-outsider@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "handover", Name: "handover"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	transfer := TransferOwnerInput{ProjectID: p.ID, UserID: editor.User.ID, ConfirmSlug: "handover"}
	// The owner's own area, and one owned by someone else that a transfer must never touch.
	mine := mustCall(t, tools, admin, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "인증", OwnerID: admin.User.ID}).(WorkArea)
	theirs := mustCall(t, tools, admin, "set_work_area", WorkAreaInput{ProjectID: p.ID, Name: "배포", OwnerID: editor.User.ID}).(WorkArea)
	areaOwnerOf := func(id string) string {
		t.Helper()
		var owner string
		if err := s.DB.QueryRow(`SELECT owner_id::text FROM work_areas WHERE id=$1`, id).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		return owner
	}

	// AI clients (MCP keys) cannot transfer, even the owner's write key.
	keyOut, err := s.CreateKey(ctx, admin, KeyInput{Name: "agent", Access: "read_write", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "transfer_project_owner", transfer)
	denied(t, err, "FORBIDDEN")
	// Non-owners cannot transfer.
	_, err = call(t, tools, editor, "transfer_project_owner", TransferOwnerInput{ProjectID: p.ID, UserID: editor.User.ID, ConfirmSlug: "handover"})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, outsider, "transfer_project_owner", TransferOwnerInput{ProjectID: p.ID, UserID: outsider.User.ID, ConfirmSlug: "handover"})
	denied(t, err, "NOT_FOUND")

	for _, bad := range []TransferOwnerInput{
		{ProjectID: p.ID, UserID: editor.User.ID, ConfirmSlug: "wrong"},
		{ProjectID: p.ID, UserID: admin.User.ID, ConfirmSlug: "handover"},
		{ProjectID: p.ID, UserID: editor.User.ID, ConfirmSlug: "handover", PreviousOwnerRole: "owner"},
		{ProjectID: p.ID, UserID: "not-a-uuid", ConfirmSlug: "handover"},
		{ProjectID: p.ID, UserID: editor.User.ID, ConfirmSlug: "handover", WorkAreas: "drop"},
		// A viewer cannot own work areas, so keeping them as viewer is refused.
		{ProjectID: p.ID, UserID: editor.User.ID, ConfirmSlug: "handover", PreviousOwnerRole: "viewer", WorkAreas: "keep"},
	} {
		_, err = call(t, tools, admin, "transfer_project_owner", bad)
		denied(t, err, "INVALID_INPUT")
	}
	// The new owner must already be an active member.
	_, err = call(t, tools, admin, "transfer_project_owner", TransferOwnerInput{ProjectID: p.ID, UserID: outsider.User.ID, ConfirmSlug: "handover"})
	denied(t, err, "NOT_FOUND")

	out := mustCall(t, tools, admin, "transfer_project_owner", transfer).(map[string]any)
	if out["owner_id"] != editor.User.ID || out["previous_owner_role"] != "editor" || out["new_owner_previous_role"] != "editor" {
		t.Fatalf("transfer result: %v", out)
	}
	// By default an owner who stays as editor keeps their areas.
	if out["work_areas"] != "keep" || out["work_areas_moved"] != int64(0) || areaOwnerOf(mine.ID) != admin.User.ID {
		t.Fatalf("default transfer should keep areas: %v owner %s", out, areaOwnerOf(mine.ID))
	}
	roles := map[string]string{}
	rows, err := s.DB.Query(`SELECT user_id::text,role FROM project_members WHERE project_id=$1`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, role string
		if err = rows.Scan(&id, &role); err != nil {
			t.Fatal(err)
		}
		roles[id] = role
	}
	rows.Close()
	if roles[editor.User.ID] != "owner" || roles[admin.User.ID] != "editor" || roles[viewer.User.ID] != "viewer" {
		t.Fatalf("member roles after transfer: %v", roles)
	}
	var owner string
	if err = s.DB.QueryRow(`SELECT owner_id::text FROM projects WHERE id=$1`, p.ID).Scan(&owner); err != nil || owner != editor.User.ID {
		t.Fatalf("projects.owner_id not moved: %q %v", owner, err)
	}

	// The previous owner lost owner powers; the new owner has them.
	_, err = call(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "editor"})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, admin, "transfer_project_owner", TransferOwnerInput{ProjectID: p.ID, UserID: admin.User.ID, ConfirmSlug: "handover"})
	denied(t, err, "FORBIDDEN")
	mustCall(t, tools, editor, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "editor"})
	// The new owner cannot be demoted or removed through set_project_member.
	_, err = call(t, tools, editor, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "remove"})
	denied(t, err, "FORBIDDEN")

	// Hand it back, leaving the second owner as a viewer: their areas follow the
	// new owner without being asked, and other members' areas stay put.
	back := mustCall(t, tools, editor, "transfer_project_owner", TransferOwnerInput{ProjectID: p.ID, UserID: admin.User.ID, ConfirmSlug: "handover", PreviousOwnerRole: "viewer"}).(map[string]any)
	if back["work_areas"] != "transfer" || back["work_areas_moved"] != int64(1) || areaOwnerOf(theirs.ID) != admin.User.ID || areaOwnerOf(mine.ID) != admin.User.ID {
		t.Fatalf("viewer transfer should hand areas over: %v", back)
	}
	var role string
	if err = s.DB.QueryRow(`SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, p.ID, editor.User.ID).Scan(&role); err != nil || role != "viewer" {
		t.Fatalf("previous owner role: %q %v", role, err)
	}

	var target, prevRole string
	if err = s.DB.QueryRow(`SELECT target_id::text,details->>'previous_owner_role' FROM audit_events WHERE action='transfer_project_owner' AND outcome='success' ORDER BY id DESC LIMIT 1`).Scan(&target, &prevRole); err != nil || target != admin.User.ID || prevRole != "viewer" {
		t.Fatalf("audit should record new owner and previous role: %q %q %v", target, prevRole, err)
	}

	// Asked explicitly, an owner who stays as editor hands every area they own over.
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	moved := mustCall(t, tools, admin, "transfer_project_owner", TransferOwnerInput{ProjectID: p.ID, UserID: editor.User.ID, ConfirmSlug: "handover", WorkAreas: "transfer"}).(map[string]any)
	if moved["work_areas_moved"] != int64(2) || areaOwnerOf(mine.ID) != editor.User.ID || areaOwnerOf(theirs.ID) != editor.User.ID {
		t.Fatalf("explicit transfer should move both areas: %v", moved)
	}
	var mode string
	if err = s.DB.QueryRow(`SELECT details->>'work_areas' FROM audit_events WHERE action='transfer_project_owner' AND outcome='success' ORDER BY id DESC LIMIT 1`).Scan(&mode); err != nil || mode != "transfer" {
		t.Fatalf("audit should record the work area choice: %q %v", mode, err)
	}

	// The tool is hidden from MCP clients.
	st, ct := mcp.NewInMemoryTransports()
	ss, err := tools.MCP(admin).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "owner-client", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "transfer_project_owner" {
			t.Fatal("transfer_project_owner must not be exposed over MCP")
		}
	}
}
