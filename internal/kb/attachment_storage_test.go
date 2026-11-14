package kb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type checkedObjects struct {
	memoryObjects
	err   error
	calls int
}

func (m *checkedObjects) Check(ctx context.Context) error {
	m.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 8*time.Second {
		panic("storage probe must have a bounded deadline")
	}
	return m.err
}

func TestAttachmentStorageStatusSessionAndRedaction(t *testing.T) {
	s := testStore(t)
	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	get := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/attachments/storage", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "kb_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	objects := &checkedObjects{}
	s.Objects = objects
	if w := get(""); w.Code != 401 || objects.calls != 0 {
		t.Fatal("anonymous storage probe allowed")
	}
	admin := loginAs(t, s, "admin@example.com")
	inviteAs(t, s, admin, "reader@example.com")
	cookie, err := s.Login(context.Background(), "reader@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		objects ObjectStorage
		scanner MalwareScanner
	}{
		{"not_configured", nil, nil},
		{"connected", objects, &fakeScanner{}},
		{"unavailable", &checkedObjects{err: errors.New("private endpoint and secret credentials")}, &fakeScanner{err: errors.New("private clamd address")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.Objects, s.Scanner = tc.objects, tc.scanner
			w := get(cookie)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var body struct {
				Data attachmentStorageStatus `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.State != tc.name || body.Data.Scanner != tc.name || body.Data.CheckedAt.IsZero() {
				t.Fatalf("unexpected status: %+v", body.Data)
			}
			if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "secret") {
				t.Fatal("storage details leaked")
			}
		})
	}
	s.Scanner = nil
	if objects.calls != 1 {
		t.Fatal("bucket access was not checked")
	}
	for range 9 {
		if w := get(cookie); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if w := get(cookie); w.Code != 429 {
		t.Fatal("missing probe rate limit", w.Code)
	}
}

func TestRailwayBucketCheckIsReadOnlyAndSigned(t *testing.T) {
	for _, status := range []int{200, 403, 404} {
		obj, err := NewRailwayBucket(BucketConfig{Endpoint: "https://storage.example.test", Bucket: "test-bucket", Region: "auto", AccessKeyID: "test-access", SecretAccessKey: "test-secret"})
		if err != nil {
			t.Fatal(err)
		}
		b := obj.(*railwayBucket)
		calls := 0
		b.client = s3ClientWithTransport(b.client, bucketRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != "HEAD" || r.URL.Path != "/" || r.URL.RawQuery != "" || !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
				t.Fatal("expected signed bucket HEAD without listing or mutation")
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}))
		err = b.Check(context.Background())
		if (err == nil) != (status == 200) || calls != 1 {
			t.Fatal("incorrect bucket check result", status, calls)
		}
	}
}
