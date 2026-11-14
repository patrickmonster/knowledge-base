package kb

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	if _, err = s.RevokeKey(ctx, owner, keyID.KeyID); err != nil {
		t.Fatal(err)
	}
	if w := put(f, key, ""); w.Code != 401 {
		t.Fatal(w.Code)
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
