package kb

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReviewDocument(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	viewer := inviteAs(t, s, admin, "review-viewer@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "review", Name: "review"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	draft := func(title string) Document {
		return mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: title, Kind: "decision", Status: "draft", Content: title + " 본문"}).(Document)
	}
	approve := ReviewInput{ProjectID: p.ID, Decision: "approve", Reason: "근거 확인함"}

	d := draft("승인 대상")
	approve.DocumentID, approve.ExpectedRevision = d.ID, d.Revision

	// AI clients (MCP keys) cannot review, even with write access.
	keyOut, err := s.CreateKey(ctx, admin, KeyInput{Name: "agent", Access: "read_write", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "review_document", approve)
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, viewer, "review_document", approve)
	denied(t, err, "FORBIDDEN")

	for _, bad := range []ReviewInput{
		{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: d.Revision, Decision: "approve", Reason: "   "},
		{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: d.Revision, Decision: "publish", Reason: "x"},
	} {
		_, err = call(t, tools, admin, "review_document", bad)
		denied(t, err, "INVALID_INPUT")
	}
	stale := approve
	stale.ExpectedRevision = d.Revision + 1
	_, err = call(t, tools, admin, "review_document", stale)
	denied(t, err, "REVISION_CONFLICT")

	out := mustCall(t, tools, admin, "review_document", approve).(Document)
	if out.Status != "active" || out.Revision != d.Revision+1 {
		t.Fatalf("approve result: %+v", out)
	}
	var reason string
	if err = s.DB.QueryRow(`SELECT reason FROM revisions WHERE document_id=$1 AND revision=$2`, d.ID, out.Revision).Scan(&reason); err != nil || reason != "검토 승인: 근거 확인함" {
		t.Fatalf("review reason not recorded: %q %v", reason, err)
	}
	// A reviewed (non-draft) document cannot be reviewed again.
	approve.ExpectedRevision = out.Revision
	_, err = call(t, tools, admin, "review_document", approve)
	denied(t, err, "CONFLICT")

	r := draft("반려 대상")
	rejected := mustCall(t, tools, admin, "review_document", ReviewInput{ProjectID: p.ID, DocumentID: r.ID, ExpectedRevision: r.Revision, Decision: "reject", Reason: "근거 부족"}).(Document)
	if rejected.Status != "archived" {
		t.Fatalf("reject result: %+v", rejected)
	}
	queue := mustCall(t, tools, admin, "list_documents", ListInput{ProjectID: p.ID, Status: "draft"}).([]map[string]any)
	if len(queue) != 0 {
		t.Fatalf("reviewed drafts still queued: %v", queue)
	}

	var details string
	if err = s.DB.QueryRow(`SELECT details::text FROM audit_events WHERE action='review_document' AND outcome='success' ORDER BY id DESC LIMIT 1`).Scan(&details); err != nil || !strings.Contains(details, "reject") || strings.Contains(details, "근거 부족") {
		t.Fatalf("audit should record the decision but not the reason text: %q %v", details, err)
	}

	// The tool is hidden from MCP clients.
	st, ct := mcp.NewInMemoryTransports()
	ss, err := tools.MCP(admin).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "review-client", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "review_document" {
			t.Fatal("review_document must not be exposed over MCP")
		}
	}
}
