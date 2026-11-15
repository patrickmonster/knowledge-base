package kb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

type memoryObjects struct {
	mu      sync.Mutex
	files   map[string][]byte
	failPut bool
	puts    int
}

func (m *memoryObjects) Check(context.Context) error { return nil }

func (m *memoryObjects) Put(_ context.Context, key, _ string, r io.ReadSeeker, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts++
	b, e := io.ReadAll(r)
	m.files[key] = b
	if m.failPut {
		return fmt.Errorf("lost response after write")
	}
	return e
}
func (m *memoryObjects) DownloadURL(_ context.Context, key, _ string) (string, error) {
	return "https://files.example.com/" + key + "?signature=private", nil
}
func (m *memoryObjects) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[key]
	if !ok {
		return nil, fmt.Errorf("missing object")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memoryObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, key)
	return nil
}
func preparedAttachment(t *testing.T, tools Tools, a Identity, pid string, body string) Attachment {
	t.Helper()
	out := mustCall(t, tools, a, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: pid, Filename: "기획 문서.pdf", SizeBytes: int64(len(body))}).(map[string]any)
	return out["attachment"].(Attachment)
}
func TestAttachmentsPermissionsLifecycleAndRecovery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := &memoryObjects{files: map[string][]byte{}}
	s.Objects = m
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "owner@example.com")
	viewer := inviteAs(t, s, admin, "viewer@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "files", Name: "Files"}).(Project)
	other := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "other", Name: "Other"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	body := "%PDF-1.7\ncontent"
	f := preparedAttachment(t, tools, owner, p.ID, body)
	in := AttachmentInput{p.ID, f.ID}
	for _, actor := range []Identity{admin, viewer} {
		_, err := s.UploadAttachment(ctx, actor, in, strings.NewReader(body))
		if actor.User.ID == admin.User.ID {
			denied(t, err, "NOT_FOUND")
		} else {
			denied(t, err, "FORBIDDEN")
		}
	}
	_, err := call(t, tools, viewer, "get_attachment_download", in)
	denied(t, err, "CONFLICT")
	out, err := s.UploadAttachment(ctx, owner, in, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	stored := out.(Attachment)
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	if stored.Status != "ready" || stored.SHA256 == nil || *stored.SHA256 != sum {
		t.Fatalf("bad stored metadata: %+v", stored)
	}
	_, err = s.UploadAttachment(ctx, owner, in, strings.NewReader(body))
	denied(t, err, "CONFLICT")
	if m.puts != 1 {
		t.Fatal("file was overwritten")
	}
	mustCall(t, tools, viewer, "get_attachment_download", in)
	_, err = call(t, tools, owner, "get_attachment_download", AttachmentInput{other.ID, f.ID})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, viewer, "delete_attachment", in)
	denied(t, err, "FORBIDDEN")
	doc := mustCall(t, tools, owner, "create_document", DocumentInput{ProjectID: other.ID, Title: "Other", Kind: "brief", Content: "private"}).(Document)
	_, err = call(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, DocumentID: doc.ID, Filename: "file.pdf", SizeBytes: 3})
	denied(t, err, "NOT_FOUND")
	linked := mustCall(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: other.ID, DocumentID: doc.ID, Filename: "file.pdf", SizeBytes: 3}).(map[string]any)["attachment"].(Attachment)
	if linked.DocumentRevision == nil || *linked.DocumentRevision != 1 {
		t.Fatal("missing document version")
	}
	for _, filename := range []string{"../bad", "bad\r\nname", "", " path.pdf"} {
		_, err = call(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: filename, SizeBytes: 3})
		denied(t, err, "INVALID_INPUT")
	}
	_, err = call(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: "large.pdf", SizeBytes: MaxAttachmentBytes + 1})
	denied(t, err, "INVALID_INPUT")
	// A lost response after bucket PUT keeps a journal entry for safe cleanup.
	m.failPut = true
	failed := preparedAttachment(t, tools, owner, p.ID, body)
	_, err = s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, failed.ID}, strings.NewReader(body))
	denied(t, err, "STORAGE_UNAVAILABLE")
	_, err = call(t, tools, owner, "get_attachment_download", AttachmentInput{p.ID, failed.ID})
	denied(t, err, "CONFLICT")
	if _, err = s.DB.Exec("UPDATE attachments SET updated_at=now()-interval '2 hours' WHERE id=$1", failed.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupAttachments(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.files[attachmentKey(p.ID, failed.ID)]; ok {
		t.Fatal("failed object leaked")
	}
	if _, ok := m.files[attachmentKey(p.ID, f.ID)]; !ok {
		t.Fatal("ready object was deleted")
	}
	mustCall(t, tools, owner, "delete_attachment", in)
	_, err = call(t, tools, owner, "get_attachment_download", in)
	denied(t, err, "NOT_FOUND")
	if err = s.CleanupAttachments(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.files) != 0 {
		t.Fatal("deleted file still present")
	}
	var leaked bool
	if err = s.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM audit_events WHERE details::text LIKE '%signature%' OR details::text LIKE '%기획 문서%')").Scan(&leaked); err != nil || leaked {
		t.Fatalf("audit leak: %v", err)
	}
}

func TestAttachmentHTTPAndConcurrentSingleUse(t *testing.T) {
	s := testStore(t)
	s.Objects = &memoryObjects{files: map[string][]byte{}}
	tools := NewTools(s)
	ctx := context.Background()
	owner := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "http-files", Name: "HTTP"}).(Project)
	keyResult, err := s.CreateKey(ctx, owner, KeyInput{Name: "uploader", Access: "read_write", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key := keyResult.(map[string]any)["token"].(string)
	keyID, err := s.APIKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	put := func(f Attachment, token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "http://localhost:8080/api/attachments/"+f.ID+"/content?project_id="+p.ID, strings.NewReader("abc"))
		r.Header.Set("Content-Type", "application/octet-stream")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	f := preparedAttachment(t, tools, keyID, p.ID, "abc")
	if w := put(f, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := put(f, key, "https://evil.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := put(f, key, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := put(f, key, ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	f = preparedAttachment(t, tools, owner, p.ID, "abc")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, f.ID}, strings.NewReader("abc"))
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for e := range errs {
		if e == nil {
			successes++
		} else {
			denied(t, e, "CONFLICT")
		}
	}
	if successes != 1 {
		t.Fatal(successes)
	}
	f = preparedAttachment(t, tools, owner, p.ID, "abc")
	_, err = s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, f.ID}, strings.NewReader("abcd"))
	denied(t, err, "INVALID_INPUT")
	readResult, err := s.CreateKey(ctx, owner, KeyInput{Name: "readonly", Access: "read_only", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	readonly := readResult.(map[string]any)["token"].(string)
	f = preparedAttachment(t, tools, owner, p.ID, "abc")
	if w := put(f, readonly, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}

	// One-time upload links for MCP clients that cannot read their own Bearer key.
	putLink := func(u, auth, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", u, strings.NewReader("abc"))
		r.Header.Set("Content-Type", "application/octet-stream")
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	prepareLink := func() string {
		out := mustCall(t, tools, keyID, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: "summary.xlsx", SizeBytes: 3}).(map[string]any)
		link, _ := out["upload_url"].(string)
		if !strings.HasPrefix(link, "http://localhost:8080/api/attachments/") || !strings.Contains(link, "&upload_token=kbu_") {
			t.Fatalf("missing upload link: %v", out)
		}
		return link
	}
	if _, ok := mustCall(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: "web.txt", SizeBytes: 3}).(map[string]any)["upload_url"]; ok {
		t.Fatal("web session callers must not receive upload links")
	}
	link, other := prepareLink(), prepareLink()
	tampered := link[:len(link)-1] + map[bool]string{true: "A", false: "B"}[link[len(link)-1] != 'A']
	if w := putLink(tampered, "", ""); w.Code != 401 {
		t.Fatal("tampered link", w.Code)
	}
	otherToken := other[strings.Index(other, "&upload_token="):]
	if w := putLink(link[:strings.Index(link, "&upload_token=")]+otherToken, "", ""); w.Code != 401 {
		t.Fatal("token reused for another file", w.Code)
	}
	if w := putLink(link, "Bearer "+key, ""); w.Code != 400 {
		t.Fatal("mixed credentials", w.Code)
	}
	if w := putLink(link, "", "https://evil.example"); w.Code != 403 {
		t.Fatal("foreign origin", w.Code)
	}
	if w := putLink(link, "", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := putLink(link, "", ""); w.Code != 401 {
		t.Fatal("link reused", w.Code)
	}
	expired := prepareLink()
	if _, err = s.DB.Exec("UPDATE attachments SET created_at=now()-interval '61 minutes' WHERE upload_token_hash IS NOT NULL AND id::text=split_part(split_part($1,'/api/attachments/',2),'/',1)", expired); err != nil {
		t.Fatal(err)
	}
	if w := putLink(expired, "", ""); w.Code != 401 {
		t.Fatal("expired link", w.Code)
	}
	revokedLink := prepareLink()
	if _, err = s.RevokeKey(ctx, owner, keyID.KeyID); err != nil {
		t.Fatal(err)
	}
	if w := put(f, key, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := putLink(revokedLink, "", ""); w.Code != 401 {
		t.Fatal("revoked key link", w.Code)
	}
	var leaked bool
	if err = s.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM audit_events WHERE details::text LIKE '%kbu\\_%')").Scan(&leaked); err != nil || leaked {
		t.Fatalf("upload token leaked into audit log: %v", err)
	}
}

func TestAttachmentChunkUploadThroughMCP(t *testing.T) {
	s := testStore(t)
	m := &memoryObjects{files: map[string][]byte{}}
	s.Objects = m
	tools := NewTools(s)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "owner@example.com")
	editor := inviteAs(t, s, admin, "editor@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "chunks", Name: "Chunks"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: editor.User.ID, Role: "editor"})
	keyResult, err := s.CreateKey(ctx, owner, KeyInput{Name: "sandbox", Access: "read_write", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := s.APIKey(ctx, keyResult.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()

	body := bytes.Repeat([]byte("Lorely 기획서 chunk test. "), MaxAttachmentChunkBytes/16)
	body = body[:MaxAttachmentChunkBytes+1000] // two chunks: full + remainder
	prepared := mustCall(t, tools, keyID, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: "기획서.md", SizeBytes: int64(len(body))}).(map[string]any)
	f := prepared["attachment"].(Attachment)
	link := prepared["upload_url"].(string)
	chunk := func(actor Identity, offset int64, data []byte) (any, error) {
		return call(t, tools, actor, "upload_attachment_chunk", UploadAttachmentChunkInput{ProjectID: p.ID, AttachmentID: f.ID, Offset: offset, DataBase64: base64.StdEncoding.EncodeToString(data)})
	}
	first, second := body[:MaxAttachmentChunkBytes], body[MaxAttachmentChunkBytes:]

	// Invalid input, wrong uploader and out-of-order chunks are rejected.
	_, err = call(t, tools, keyID, "upload_attachment_chunk", UploadAttachmentChunkInput{ProjectID: p.ID, AttachmentID: f.ID, DataBase64: "not base64!"})
	denied(t, err, "INVALID_INPUT")
	_, err = chunk(keyID, 0, append(append([]byte{}, first...), 'x'))
	denied(t, err, "INVALID_INPUT")
	_, err = chunk(editor, 0, first)
	denied(t, err, "FORBIDDEN")
	_, err = chunk(keyID, 10, second)
	denied(t, err, "INVALID_INPUT")

	out, err := chunk(keyID, 0, first)
	if err != nil {
		t.Fatal(err)
	}
	progress := out.(chunkProgress)
	if progress.Complete || progress.NextOffset != int64(len(first)) || progress.Attachment != nil {
		t.Fatalf("bad progress: %+v", progress)
	}
	// Identical retry is safe; different data at a received offset is not.
	if out, err = chunk(keyID, 0, first); err != nil || out.(chunkProgress).NextOffset != int64(len(first)) {
		t.Fatalf("retry: %v %+v", err, out)
	}
	_, err = chunk(keyID, 0, bytes.Repeat([]byte("z"), len(first)))
	denied(t, err, "CONFLICT")
	// The one-time PUT link is retired once chunks start.
	r := httptest.NewRequest("PUT", link, strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("upload link still active after chunks began", w.Code)
	}
	// Overflowing the reservation is rejected.
	_, err = chunk(keyID, int64(len(first)), append(append([]byte{}, second...), 'x'))
	denied(t, err, "INVALID_INPUT")

	out, err = chunk(keyID, int64(len(first)), second)
	if err != nil {
		t.Fatal(err)
	}
	progress = out.(chunkProgress)
	sum := fmt.Sprintf("%x", sha256.Sum256(body))
	if !progress.Complete || progress.Attachment == nil || progress.Attachment.Status != "ready" || *progress.Attachment.SHA256 != sum {
		t.Fatalf("not stored: %+v", progress)
	}
	if !bytes.Equal(m.files[attachmentKey(p.ID, f.ID)], body) {
		t.Fatal("assembled object differs from the original")
	}
	var leftover int
	if err = s.DB.QueryRow("SELECT count(*) FROM attachment_chunks").Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("chunks not removed: %d %v", leftover, err)
	}
	// Retrying the last chunk after a lost response returns the stored file without a second PUT.
	if out, err = chunk(keyID, int64(len(first)), second); err != nil || !out.(chunkProgress).Complete || m.puts != 1 {
		t.Fatalf("final retry: %v %+v puts=%d", err, out, m.puts)
	}
	_, err = chunk(keyID, 0, first)
	denied(t, err, "CONFLICT")

	// Read-only keys cannot upload; abandoned chunks go with the expired reservation.
	readResult, err := s.CreateKey(ctx, owner, KeyInput{Name: "ro", Access: "read_only", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	readonly, err := s.APIKey(ctx, readResult.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	f = preparedAttachment(t, tools, owner, p.ID, "abcdef")
	_, err = chunk(readonly, 0, []byte("abc"))
	denied(t, err, "FORBIDDEN")
	if _, err = chunk(owner, 0, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE attachments SET created_at=now()-interval '2 hours',updated_at=now()-interval '2 hours' WHERE id=$1", f.ID); err != nil {
		t.Fatal(err)
	}
	_, err = chunk(owner, 3, []byte("def"))
	denied(t, err, "CONFLICT")
	if err = s.CleanupAttachments(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow("SELECT count(*) FROM attachment_chunks").Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("abandoned chunks kept: %d %v", leftover, err)
	}
	var leaked bool
	if err = s.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM audit_events WHERE details::text LIKE '%data_base64%' OR details::text LIKE '%Lorely%')").Scan(&leaked); err != nil || leaked {
		t.Fatalf("chunk data leaked into audit log: %v", err)
	}
}

type bucketRoundTrip func(*http.Request) (*http.Response, error)

func (f bucketRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func s3ClientWithTransport(client *s3.Client, transport http.RoundTripper) *s3.Client {
	return s3.New(client.Options(), func(o *s3.Options) { o.HTTPClient = &http.Client{Transport: transport} })
}
func TestRailwayBucketConfigurationAndSigning(t *testing.T) {
	base := BucketConfig{Endpoint: "https://t3.storageapi.dev", Bucket: "test-bucket", Region: "auto", AccessKeyID: "test-access", SecretAccessKey: "test-secret"}
	for _, endpoint := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/path", "https://example.com?key=x"} {
		c := base
		c.Endpoint = endpoint
		if _, err := NewRailwayBucket(c); err == nil {
			t.Fatal("accepted invalid endpoint")
		}
	}
	if _, err := NewRailwayBucket(BucketConfig{Bucket: "partial"}); err == nil {
		t.Fatal("accepted incomplete credentials")
	}
	if obj, err := NewRailwayBucket(BucketConfig{}); obj != nil || err != nil {
		t.Fatal("optional bucket")
	}
	for _, pathStyle := range []bool{false, true} {
		c := base
		c.ForcePathStyle = pathStyle
		obj, err := NewRailwayBucket(c)
		if err != nil {
			t.Fatal(err)
		}
		b := obj.(*railwayBucket)
		b.client = s3ClientWithTransport(b.client, bucketRoundTrip(func(r *http.Request) (*http.Response, error) {
			if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment;") {
				t.Fatal("unsigned or inline object")
			}
			body, _ := io.ReadAll(r.Body)
			if !bytes.Equal(body, []byte("abc")) {
				t.Fatalf("wrong upload payload: %q", body)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}))
		if err = b.Put(context.Background(), "attachments/a/b", "기획.pdf", strings.NewReader("abc"), 3); err != nil {
			t.Fatal(err)
		}
		link, err := b.DownloadURL(context.Background(), "attachments/a/b", "기획.pdf")
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(link)
		wantHost := "test-bucket.t3.storageapi.dev"
		if pathStyle {
			wantHost = "t3.storageapi.dev"
		}
		if u.Host != wantHost || u.Query().Get("X-Amz-Expires") != "300" || !strings.HasPrefix(u.Query().Get("response-content-disposition"), "attachment;") {
			t.Fatal("incorrect signed download")
		}
	}
}

// Explicit opt-in only. Writes one synthetic object, downloads it, then removes it.
// Does not access the application's database or any pre-existing bucket object.
func TestRailwayBucketLive(t *testing.T) {
	if os.Getenv("TEST_RAILWAY_BUCKET_LIVE") != "true" {
		t.Skip("set TEST_RAILWAY_BUCKET_LIVE=true with a bucket connection to verify real storage")
	}
	b, err := NewRailwayBucket(BucketConfig{Endpoint: os.Getenv("BUCKET_ENDPOINT"), Bucket: os.Getenv("BUCKET_NAME"), Region: os.Getenv("BUCKET_REGION"), AccessKeyID: os.Getenv("BUCKET_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("BUCKET_SECRET_ACCESS_KEY"), ForcePathStyle: os.Getenv("BUCKET_FORCE_PATH_STYLE") == "true"})
	if err != nil || b == nil {
		t.Fatal("live bucket configuration unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	key := "verification/" + uuid.NewString()
	body := "Railway attachment storage verification. Synthetic test data only."
	defer func() {
		c, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if b.Delete(c, key) != nil {
			t.Error("synthetic object cleanup failed; key:", key)
		}
	}()
	if b.Put(ctx, key, "첨부-검증.txt", strings.NewReader(body), int64(len(body))) != nil {
		t.Fatal("live bucket upload failed")
	}
	link, err := b.DownloadURL(ctx, key, "첨부-검증.txt")
	if err != nil {
		t.Fatal("live download signing failed")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		t.Fatal("invalid download request")
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("live download request failed")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(data) != body || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatal("live download content or headers did not match")
	}
	if b.Delete(ctx, key) != nil {
		t.Fatal("live object deletion failed")
	}
	response, err = client.Do(req)
	if err != nil {
		t.Fatal("deletion verification request failed")
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("deleted object was not confirmed absent")
	}
}

func docxBytes(t *testing.T, text string) []byte {
	t.Helper()
	b, err := os.ReadFile(docxWith(t, `<w:p><w:r><w:t>`+text+`</w:t></w:r></w:p>`))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAttachmentTextExtractionIndexesDocument(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Objects = &memoryObjects{files: map[string][]byte{}}
	s.Extractor = InProcessExtractor{}
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "owner@example.com")
	viewer := inviteAs(t, s, admin, "viewer@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "extract", Name: "Extract"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	upload := func(name string, body []byte) Attachment {
		t.Helper()
		out := mustCall(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: name, SizeBytes: int64(len(body))}).(map[string]any)
		stored, err := s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, out["id"].(string)}, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return stored.(Attachment)
	}

	f := upload("환불 규정.docx", docxBytes(t, "결제 후 칠일 이내 전액 환불"))
	if f.Status != "ready" || f.TextStatus != "extracted" || f.TextDocumentID == nil || f.TextError != nil {
		t.Fatalf("bad extraction metadata: %+v", f)
	}
	doc := mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: *f.TextDocumentID}).(Document)
	if doc.Kind != "artifact" || doc.Status != "active" || doc.Title != "첨부 본문: 환불 규정.docx" || doc.CreatedBy != owner.User.ID ||
		!strings.Contains(doc.Content, "결제 후 칠일 이내 전액 환불") || !strings.Contains(doc.Content, f.ID) {
		t.Fatalf("bad text document: %+v", doc)
	}
	hits := mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "전액 환불", Mode: "lexical"}).(SearchResult)
	if len(hits.Hits) == 0 || hits.Hits[0].DocumentID != doc.ID {
		t.Fatalf("extracted text not searchable: %+v", hits)
	}
	listed := mustCall(t, tools, viewer, "list_attachments", ListAttachmentsInput{ProjectID: p.ID}).([]Attachment)
	if len(listed) != 1 || listed[0].TextDocumentID == nil || *listed[0].TextDocumentID != doc.ID {
		t.Fatalf("list misses text link: %+v", listed)
	}

	// Broken and unsupported files are stored; only the index is skipped.
	broken := upload("깨진.xlsx", []byte("PK not a zip"))
	if broken.Status != "ready" || broken.TextStatus != "failed" || broken.TextError == nil || *broken.TextError != ExtractInvalidFile || broken.TextDocumentID != nil {
		t.Fatalf("broken file: %+v", broken)
	}
	plain := upload("notes.txt", []byte("hello"))
	if plain.Status != "ready" || plain.TextStatus != "none" || plain.TextDocumentID != nil {
		t.Fatalf("plain file: %+v", plain)
	}
	empty := upload("빈 문서.docx", docxBytes(t, ""))
	if empty.TextStatus != "empty" || empty.TextDocumentID != nil {
		t.Fatalf("empty file: %+v", empty)
	}

	// Deleting the file archives its text so search stops returning it.
	mustCall(t, tools, owner, "delete_attachment", AttachmentInput{p.ID, f.ID})
	doc = mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: doc.ID}).(Document)
	if doc.Status != "archived" || doc.Revision != 2 {
		t.Fatalf("text document not archived: %+v", doc)
	}
	hits = mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "전액 환불", Mode: "lexical"}).(SearchResult)
	for _, h := range hits.Hits {
		if h.DocumentID == doc.ID {
			t.Fatal("archived text still returned by default search")
		}
	}
	if err := s.CleanupAttachments(ctx); err != nil {
		t.Fatal(err)
	}

	// Extraction disabled keeps the old behavior.
	s.Extractor = nil
	off := upload("끔.docx", docxBytes(t, "색인 안 함"))
	if off.TextStatus != "none" || off.TextDocumentID != nil {
		t.Fatalf("disabled extraction: %+v", off)
	}
}
