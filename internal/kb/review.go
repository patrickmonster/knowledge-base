package kb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReviewInput approves (draft → active) or rejects (draft → archived) a draft.
type ReviewInput struct {
	ProjectID        string `json:"project_id"`
	DocumentID       string `json:"document_id"`
	ExpectedRevision int    `json:"expected_revision"`
	Decision         string `json:"decision" jsonschema:"approve or reject"`
	Reason           string `json:"reason"`
}

var reviewStatus = map[string]string{"approve": "active", "reject": "archived"}
var reviewLabel = map[string]string{"approve": "검토 승인", "reject": "검토 반려"}

// reviewDocument promotes or rejects a draft in one step and records the
// reviewer's reason in the revision history. It is a human decision: MCP keys
// (AI clients) are refused so drafts are never activated by the author's agent.
func reviewDocument(ctx context.Context, tx *sql.Tx, actor Identity, in ReviewInput) (any, error) {
	if actor.KeyID != "" {
		return nil, problem("FORBIDDEN", "Draft review requires a signed-in person in the web console.")
	}
	status, ok := reviewStatus[in.Decision]
	if !ok {
		return nil, problem("INVALID_INPUT", "decision must be approve or reject.")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if err := bounded("reason", in.Reason, 1, 900); err != nil {
		return nil, err
	}
	if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, true); err != nil {
		return nil, err
	}
	if err := validID(in.DocumentID); err != nil {
		return nil, err
	}
	var current string
	var revision int
	err := tx.QueryRowContext(ctx, `SELECT status,revision FROM documents WHERE id=$1 AND project_id=$2 FOR UPDATE`, in.DocumentID, in.ProjectID).Scan(&current, &revision)
	if err != nil {
		return nil, notFound(err)
	}
	if revision != in.ExpectedRevision {
		return nil, problem("REVISION_CONFLICT", fmt.Sprintf("Current revision is %d. Reload the draft and review it again.", revision))
	}
	if current != "draft" {
		return nil, problem("CONFLICT", "Only draft documents can be reviewed; this document is "+current+".")
	}
	return updateDocument(ctx, tx, actor, UpdateInput{ProjectID: in.ProjectID, DocumentID: in.DocumentID, ExpectedRevision: revision,
		Reason: reviewLabel[in.Decision] + ": " + in.Reason, Patch: DocumentPatch{Status: &status}})
}

func registerReviewTools(t Tools, s *Store) {
	add(t, s, "review_document", "Web console only: approve (draft → active) or reject (draft → archived) a draft with a reason.", true, func(in ReviewInput) string { return in.ProjectID }, reviewDocument)
	// Hidden from MCP clients; the web console calls it through /api/tools.
	tool := t["review_document"]
	tool.Register = func(*mcp.Server, Identity) {}
	t["review_document"] = tool
}
