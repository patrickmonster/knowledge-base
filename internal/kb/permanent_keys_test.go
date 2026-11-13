package kb

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestPermanentKeyMigrationAndLifetime(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	// Recreate the pre-upgrade constraint and migrate with an existing finite key.
	if _, err := s.DB.Exec(`ALTER TABLE api_keys ALTER COLUMN expires_at SET NOT NULL; DELETE FROM schema_migrations WHERE version=4`); err != nil {
		t.Fatal(err)
	}
	out, err := s.CreateKey(ctx, admin, KeyInput{Name: "existing", Access: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	finite := out.(map[string]any)
	var before, after time.Time
	if err := s.DB.QueryRow("SELECT expires_at FROM api_keys WHERE id=$1", finite["id"]).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(before); remaining < 30*24*time.Hour-time.Minute || remaining > 30*24*time.Hour+time.Minute {
		t.Fatal("omitted days must still default to 30 days")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT expires_at FROM api_keys WHERE id=$1", finite["id"]).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !before.Equal(after) {
		t.Fatal("migration changed an existing expiration")
	}
	for _, in := range []KeyInput{
		{Name: "negative", Access: "read_only", Days: -1},
		{Name: "too-long", Access: "read_only", Days: 91},
		{Name: "ambiguous", Access: "read_only", NeverExpires: true, Days: 30},
	} {
		_, err := s.CreateKey(ctx, admin, in)
		denied(t, err, "INVALID_INPUT")
	}
	out, err = s.CreateKey(ctx, admin, KeyInput{Name: "permanent", Access: "read_only", NeverExpires: true})
	if err != nil {
		t.Fatal(err)
	}
	key := out.(map[string]any)
	if key["expires_in_days"] != nil || key["never_expires"] != true {
		t.Fatal("permanent expiry response is ambiguous")
	}
	var expiry sql.NullTime
	if err := s.DB.QueryRow("SELECT expires_at FROM api_keys WHERE id=$1", key["id"]).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if expiry.Valid {
		t.Fatal("permanent key has an expiration")
	}
	listed, err := s.ListKeys(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range listed.([]map[string]any) {
		if row["id"] == key["id"] {
			found = true
			if row["expires_at"] != nil {
				t.Fatal("key list must expose null expiry")
			}
		}
	}
	if !found {
		t.Fatal("permanent key missing from list")
	}
	stats, err := s.Stats(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if stats.(map[string]any)["active_keys"] != float64(2) {
		t.Fatal("active key count excludes permanent keys")
	}
	id, err := s.APIKey(ctx, key["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, NewTools(s), id, "whoami", struct{}{})
	if _, err := s.RevokeKey(ctx, admin, key["id"].(string)); err != nil {
		t.Fatal(err)
	}
	_, err = s.APIKey(ctx, key["token"].(string))
	denied(t, err, "UNAUTHORIZED")
	_, err = call(t, NewTools(s), id, "whoami", struct{}{})
	denied(t, err, "UNAUTHORIZED")
}

func TestPermanentKeyPermissionsAndAccountChanges(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "permanent@example.com")
	tools := NewTools(s)
	project := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "permanent-scope", Name: "Scope"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "other-scope", Name: "Other"}).(Project)
	for _, p := range []Project{project, other} {
		mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: member.User.ID, Role: "editor"})
	}
	create := func() (map[string]any, Identity) {
		t.Helper()
		out, err := s.CreateKey(ctx, member, KeyInput{Name: "permanent scoped", Access: "read_only", ProjectID: project.ID, NeverExpires: true})
		if err != nil {
			t.Fatal(err)
		}
		key := out.(map[string]any)
		id, err := s.APIKey(ctx, key["token"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return key, id
	}
	key, id := create()
	mustCall(t, tools, id, "list_documents", ListInput{ProjectID: project.ID})
	_, err := call(t, tools, id, "list_documents", ListInput{ProjectID: other.ID})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, id, "create_document", DocumentInput{ProjectID: project.ID, Title: "blocked", Kind: "reference", Content: "blocked"})
	denied(t, err, "FORBIDDEN")
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: project.ID, UserID: member.User.ID, Role: "remove"})
	if _, err := call(t, tools, id, "list_documents", ListInput{ProjectID: project.ID}); err == nil {
		t.Fatal("permanent key bypassed removed membership")
	}
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: project.ID, UserID: member.User.ID, Role: "editor"})
	if _, err := s.UpdateUser(ctx, admin, UserUpdate{UserID: member.User.ID, Disabled: true, GlobalRole: "member"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.APIKey(ctx, key["token"].(string))
	denied(t, err, "UNAUTHORIZED")
	_, err = call(t, tools, id, "whoami", struct{}{})
	denied(t, err, "UNAUTHORIZED")
	if _, err := s.UpdateUser(ctx, admin, UserUpdate{UserID: member.User.ID, GlobalRole: "member"}); err != nil {
		t.Fatal(err)
	}
	member = loginAs(t, s, "permanent@example.com")
	key, id = create()
	if _, err := s.ChangePassword(ctx, member, testPassword, "another-test-only-password-123"); err != nil {
		t.Fatal(err)
	}
	_, err = s.APIKey(ctx, key["token"].(string))
	denied(t, err, "UNAUTHORIZED")
	_, err = call(t, tools, id, "whoami", struct{}{})
	denied(t, err, "UNAUTHORIZED")
}
