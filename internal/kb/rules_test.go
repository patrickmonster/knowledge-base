package kb

import (
	"context"
	"strings"
	"testing"
)

func TestProjectRulesAndAdminRole(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	admin := inviteAs(t, s, owner, "rules-admin@example.com")
	editor := inviteAs(t, s, owner, "rules-editor@example.com")
	viewer := inviteAs(t, s, owner, "rules-viewer@example.com")
	outsider := inviteAs(t, s, owner, "rules-outsider@example.com")

	// Project rules can be set at creation from the web console.
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "ruled", Name: "ruled", Rules: []RuleText{
		{Category: "commit", Title: "Conventional commits", Body: "Use feat:/fix: prefixes."},
		{Title: "Tests first", Body: "Run go test before finishing."},
	}}).(Project)
	_, err := call(t, tools, owner, "create_project", CreateProjectInput{Slug: "bad-rule", Name: "x", Rules: []RuleText{{Category: "nope", Title: "t", Body: "b"}}})
	denied(t, err, "INVALID_INPUT")

	// MCP keys cannot set project rules, even at creation.
	keyOut, err := s.CreateKey(ctx, owner, KeyInput{Name: "agent", Access: "read_write"})
	if err != nil {
		t.Fatal(err)
	}
	ownerKey, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, ownerKey, "create_project", CreateProjectInput{Slug: "key-rules", Name: "x", Rules: []RuleText{{Title: "t", Body: "b"}}})
	denied(t, err, "FORBIDDEN")

	// The admin role is accepted and grants project rule management.
	for _, m := range []struct {
		id   Identity
		role string
	}{{admin, "admin"}, {editor, "editor"}, {viewer, "viewer"}} {
		mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: m.id.User.ID, Role: m.role})
	}
	// Admins cannot manage members; that stays with the owner.
	_, err = call(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "editor"})
	denied(t, err, "FORBIDDEN")

	set := mustCall(t, tools, editor, "list_rules", ProjectInput{ProjectID: p.ID}).(RuleSet)
	if len(set.ProjectRules) != 2 || set.ProjectRules[0].Category != "commit" || set.ProjectRules[1].Category != "work" || set.CanManageProjectRules {
		t.Fatalf("initial rules: %+v", set)
	}
	added := mustCall(t, tools, admin, "save_rule", SaveRuleInput{ProjectID: p.ID, Scope: "project", Category: "work", Title: "Korean docs", Body: "Write docs in Korean."}).(Rule)
	if added.Revision != 1 || added.UserID != "" {
		t.Fatalf("added: %+v", added)
	}
	for _, who := range []Identity{editor, viewer, ownerKey} {
		_, err = call(t, tools, who, "save_rule", SaveRuleInput{ProjectID: p.ID, Scope: "project", Title: "x", Body: "y"})
		denied(t, err, "FORBIDDEN")
		_, err = call(t, tools, who, "delete_rule", RuleRef{ProjectID: p.ID, RuleID: added.ID})
		denied(t, err, "FORBIDDEN")
	}
	_, err = call(t, tools, outsider, "list_rules", ProjectInput{ProjectID: p.ID})
	denied(t, err, "NOT_FOUND")

	// Updates need the current revision.
	upd := SaveRuleInput{ProjectID: p.ID, Scope: "project", RuleID: added.ID, ExpectedRevision: 1, Title: "Korean docs", Body: "Write docs and commits in Korean."}
	changed := mustCall(t, tools, owner, "save_rule", upd).(Rule)
	if changed.Revision != 2 || changed.UpdatedBy != owner.User.ID {
		t.Fatalf("changed: %+v", changed)
	}
	_, err = call(t, tools, admin, "save_rule", upd)
	denied(t, err, "CONFLICT")
	upd.Scope, upd.ExpectedRevision = "personal", 2
	_, err = call(t, tools, admin, "save_rule", upd)
	denied(t, err, "INVALID_INPUT")

	// Personal rules: any member, including viewers and writable keys, and private to their author.
	mine := mustCall(t, tools, viewer, "save_rule", SaveRuleInput{ProjectID: p.ID, Scope: "personal", Category: "commit", Title: "Short subjects", Body: "Keep subjects under 50 chars."}).(Rule)
	if mine.UserID != viewer.User.ID {
		t.Fatalf("personal owner: %+v", mine)
	}
	mustCall(t, tools, ownerKey, "save_rule", SaveRuleInput{ProjectID: p.ID, Scope: "personal", Title: "Owner pref", Body: "Prefer small PRs."})
	for _, who := range []Identity{editor, owner, admin} {
		_, err = call(t, tools, who, "save_rule", SaveRuleInput{ProjectID: p.ID, Scope: "personal", RuleID: mine.ID, ExpectedRevision: 1, Title: "x", Body: "y"})
		denied(t, err, "NOT_FOUND")
		_, err = call(t, tools, who, "delete_rule", RuleRef{ProjectID: p.ID, RuleID: mine.ID})
		denied(t, err, "NOT_FOUND")
	}
	viewerSet := mustCall(t, tools, viewer, "list_rules", ProjectInput{ProjectID: p.ID}).(RuleSet)
	if len(viewerSet.ProjectRules) != 3 || len(viewerSet.PersonalRules) != 1 || viewerSet.PersonalRules[0].ID != mine.ID {
		t.Fatalf("viewer rules: %+v", viewerSet)
	}
	ownerSet := mustCall(t, tools, owner, "list_rules", ProjectInput{ProjectID: p.ID}).(RuleSet)
	if len(ownerSet.PersonalRules) != 1 || ownerSet.PersonalRules[0].Title != "Owner pref" || !ownerSet.CanManageProjectRules {
		t.Fatalf("owner rules: %+v", ownerSet)
	}

	// Admins have editor access: they can write documents and own work areas.
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Kind: "reference", Title: "admin doc", Content: "본문"})
	if err := projectEditor(ctx, s.DB, p.ID, admin.User.ID, "owner_id"); err != nil {
		t.Fatalf("admin should count as editor: %v", err)
	}

	// Task context carries the caller's rules.
	tc := mustCall(t, tools, admin, "get_task_context", ContextInput{ProjectID: p.ID, Task: "commit rules"}).(TaskContext)
	if tc.Rules == nil || len(tc.Rules.ProjectRules) != 3 || len(tc.Rules.PersonalRules) != 0 || tc.Rules.Guidance == "" {
		t.Fatalf("task rules: %+v", tc.Rules)
	}

	mustCall(t, tools, viewer, "delete_rule", RuleRef{ProjectID: p.ID, RuleID: mine.ID})
	mustCall(t, tools, admin, "delete_rule", RuleRef{ProjectID: p.ID, RuleID: added.ID})
	_, err = call(t, tools, admin, "delete_rule", RuleRef{ProjectID: p.ID, RuleID: added.ID})
	denied(t, err, "NOT_FOUND")

	// Ownership can be handed to an admin, and the previous owner may stay admin.
	out := mustCall(t, tools, owner, "transfer_project_owner", TransferOwnerInput{ProjectID: p.ID, UserID: admin.User.ID, ConfirmSlug: "ruled", PreviousOwnerRole: "admin"}).(map[string]any)
	if out["new_owner_previous_role"] != "admin" || out["previous_owner_role"] != "admin" {
		t.Fatalf("transfer: %v", out)
	}
	set = mustCall(t, tools, owner, "list_rules", ProjectInput{ProjectID: p.ID}).(RuleSet)
	if !set.CanManageProjectRules {
		t.Fatal("previous owner kept as admin should manage project rules")
	}

	// Audit records the rule ID and scope without rule text.
	var details string
	if err := s.DB.QueryRow(`SELECT details::text FROM audit_events WHERE action='save_rule' AND outcome='success' ORDER BY id LIMIT 1`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, "Korean") {
		t.Fatalf("audit copied rule text: %s", details)
	}
}

