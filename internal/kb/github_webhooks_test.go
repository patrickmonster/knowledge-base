package kb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func signWebhook(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestValidWebhookSignature(t *testing.T) {
	body := []byte(`{"zen":"x"}`)
	good := signWebhook("s3cret", body)
	if !validWebhookSignature([]byte("s3cret"), body, good) {
		t.Fatal("valid signature rejected")
	}
	for _, bad := range []string{"", "sha1=" + good[7:], signWebhook("other", body), good[:len(good)-2], "sha256=zz"} {
		if validWebhookSignature([]byte("s3cret"), body, bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
	if validWebhookSignature([]byte("s3cret"), []byte(`{"zen":"y"}`), good) {
		t.Fatal("signature must cover the body")
	}
}

func TestGitHubWebhooks(t *testing.T) {
	s := testStore(t)
	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	tools := app.Tools
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	editor := inviteAs(t, s, admin, "hook-editor@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "hooks", Name: "hooks"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})

	// Only the owner manages webhooks.
	_, err = call(t, tools, editor, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, Repository: "o/r"})
	denied(t, err, "FORBIDDEN")
	saved := mustCall(t, tools, admin, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, Repository: "https://github.com/O/R", Tags: []string{"외부"}}).(GitHubWebhookSaved)
	if saved.Secret == "" || saved.WebhookID != saved.Webhook.ID || saved.PayloadURL != "http://localhost:8080/api/github/webhooks/"+saved.WebhookID ||
		saved.Webhook.Repository != "o/r" || !saved.Webhook.SecretSet {
		t.Fatalf("created: %+v", saved)
	}
	_, err = call(t, tools, admin, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, Repository: "o/r"})
	denied(t, err, "CONFLICT")
	list := mustCall(t, tools, editor, "list_github_webhooks", ProjectInput{ProjectID: p.ID}).(GitHubWebhookList)
	if len(list.Webhooks) != 1 || !list.Webhooks[0].SecretSet {
		t.Fatalf("list: %+v", list)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), saved.Secret) {
		t.Fatal("list must not expose the secret")
	}
	// The owner can show the connection info again; editors cannot.
	shown := mustCall(t, tools, admin, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, WebhookID: saved.WebhookID, ShowSecret: true}).(GitHubWebhookSaved)
	if shown.Secret != saved.Secret || shown.WebhookID != saved.WebhookID || shown.PayloadURL != saved.PayloadURL {
		t.Fatalf("show secret: %+v", shown)
	}
	_, err = call(t, tools, editor, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, WebhookID: saved.WebhookID, ShowSecret: true})
	denied(t, err, "FORBIDDEN")
	hookID, secret := saved.Webhook.ID, saved.Secret

	deliver := func(event, delivery, body, sig string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest("POST", "/api/github/webhooks/"+hookID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-Hub-Signature-256", sig)
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	send := func(event, delivery, body string) (int, map[string]any) {
		return deliver(event, delivery, body, signWebhook(secret, []byte(body)))
	}

	ping := `{"zen":"hi","repository":{"full_name":"o/r"}}`
	if code, _ := deliver("ping", "d-0", ping, signWebhook("wrong", []byte(ping))); code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", code)
	}
	if code, out := send("ping", "d-1", ping); code != 200 || out["data"].(map[string]any)["outcome"] != "processed" {
		t.Fatalf("ping: %d %v", code, out)
	}
	if code, _ := send("ping", "d-2", `{"repository":{"full_name":"o/other"}}`); code != 400 {
		t.Fatalf("repository mismatch: %d", code)
	}

	issue := func(action, state, updated, title string) string {
		b, _ := json.Marshal(map[string]any{"action": action, "repository": map[string]any{"full_name": "O/r"}, "issue": map[string]any{
			"number": 3, "title": title, "body": "본문", "state": state, "html_url": "https://github.com/o/r/issues/3",
			"user": map[string]any{"login": "octo"}, "labels": []any{map[string]any{"name": "bug"}}, "created_at": "2026-11-15T00:00:00Z", "updated_at": updated}})
		return string(b)
	}
	if code, out := send("issues", "d-3", issue("opened", "open", "2026-11-15T01:00:00Z", "첫 제목")); code != 200 || !strings.Contains(out["data"].(map[string]any)["detail"].(string), "o/r#3 created") {
		t.Fatalf("issue opened: %d %v", code, out)
	}
	// A redelivery is dropped.
	if _, out := send("issues", "d-3", issue("opened", "open", "2026-11-15T01:00:00Z", "첫 제목")); out["data"].(map[string]any)["outcome"] != "duplicate" {
		t.Fatalf("duplicate: %v", out)
	}
	send("issues", "d-4", issue("closed", "closed", "2026-11-15T03:00:00Z", "고친 제목"))
	// An older event arriving late does not roll the issue back.
	send("issues", "d-5", issue("edited", "open", "2026-11-15T02:00:00Z", "늦은 제목"))
	var title, status, createdBy string
	var tags []string
	if err := s.DB.QueryRowContext(ctx, `SELECT title,status,created_by,to_jsonb(tags)::text FROM issues WHERE project_id=$1 AND source_key='o/r#3'`, p.ID).
		Scan(&title, &status, &createdBy, jsonText{&tags}); err != nil {
		t.Fatal(err)
	}
	if title != "고친 제목" || status != "resolved" || createdBy != admin.User.ID || !strings.Contains(","+strings.Join(tags, ",")+",", ",github,") || !strings.Contains(strings.Join(tags, ","), "외부") || !strings.Contains(strings.Join(tags, ","), "bug") {
		t.Fatalf("issue state: %q %q %q %v", title, status, createdBy, tags)
	}

	// Pull request merge reaches the task history without calling GitHub.
	run := mustCall(t, tools, admin, "start_task", StartTaskInput{ProjectID: p.ID, RequestKey: "hook-1", Title: "웹훅 작업", Task: "웹훅 작업"}).(StartTaskResult).Run
	finish := finishFixture(p.ID, run.ID)
	finish.PullRequests = []string{"o/r#9"}
	completion := mustCall(t, tools, admin, "finish_task", finish).(TaskCompletion)
	merged := `{"action":"closed","repository":{"full_name":"o/r"},"pull_request":{"number":9,"state":"closed","merged":true,"merge_commit_sha":"9999999","merged_at":"2026-11-15T04:00:00Z"}}`
	if code, out := send("pull_request", "d-6", merged); code != 200 || out["data"].(map[string]any)["outcome"] != "processed" {
		t.Fatalf("pull request: %d %v", code, out)
	}
	doc := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: completion.Documents[0].ID}).(Document)
	if !strings.Contains(doc.Content, "o/r/pull/9: 병합") || !strings.Contains(doc.Content, "커밋 9999999") {
		t.Fatalf("history after webhook:\n%s", doc.Content)
	}
	if _, out := send("pull_request", "d-7", strings.Replace(merged, `"number":9`, `"number":10`, 1)); out["data"].(map[string]any)["outcome"] != "ignored" {
		t.Fatalf("untracked pull request: %v", out)
	}

	// The owner losing edit access stops issue processing; turning the webhook
	// off ignores deliveries; rotating invalidates the old secret.
	off := false
	mustCall(t, tools, admin, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, WebhookID: hookID, Enabled: &off})
	if _, out := send("ping", "d-8", ping); out["data"].(map[string]any)["outcome"] != "ignored" {
		t.Fatalf("disabled: %v", out)
	}
	on := true
	rotated := mustCall(t, tools, admin, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, WebhookID: hookID, Enabled: &on, RotateSecret: true}).(GitHubWebhookSaved)
	if rotated.Secret == "" || rotated.Secret == secret {
		t.Fatal("rotation must return a new secret")
	}
	if code, _ := send("ping", "d-9", ping); code != http.StatusUnauthorized {
		t.Fatalf("old secret after rotation: %d", code)
	}
	secret = rotated.Secret
	if code, _ := send("ping", "d-10", ping); code != 200 {
		t.Fatalf("new secret: %d", code)
	}
	list = mustCall(t, tools, admin, "list_github_webhooks", ProjectInput{ProjectID: p.ID}).(GitHubWebhookList)
	if len(list.Webhooks[0].Recent) == 0 || list.Webhooks[0].LastOutcome == nil || *list.Webhooks[0].LastOutcome != "processed" {
		t.Fatalf("delivery log: %+v", list.Webhooks[0])
	}
	// A webhook without a secret (created before migration 028) refuses deliveries.
	if _, err := s.DB.ExecContext(ctx, `UPDATE github_webhooks SET secret=NULL WHERE id=$1`, hookID); err != nil {
		t.Fatal(err)
	}
	if code, _ := send("ping", "d-12", ping); code != http.StatusUnauthorized {
		t.Fatalf("missing secret: %d", code)
	}
	_, err = call(t, tools, admin, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, WebhookID: hookID, ShowSecret: true})
	denied(t, err, "INVALID_INPUT")
	mustCall(t, tools, admin, "set_github_webhook", GitHubWebhookInput{ProjectID: p.ID, WebhookID: hookID, Remove: true})
	if code, _ := send("ping", "d-11", ping); code != http.StatusNotFound {
		t.Fatalf("removed: %d", code)
	}
}
