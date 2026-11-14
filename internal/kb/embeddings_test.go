package kb

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func unitVector(position int) []float32 {
	v := make([]float32, EmbeddingDimensions)
	v[position] = 1
	return v
}

type testEmbedder struct {
	model string
	calls atomic.Int32
	fn    func(context.Context, []string) ([][]float32, error)
}

func (e *testEmbedder) Model() string { return e.model }
func (e *testEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls.Add(1)
	if e.fn != nil {
		return e.fn(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		position := 1
		if strings.Contains(text, "환불") || strings.Contains(text, "돈을 돌려") {
			position = 0
		}
		out[i] = unitVector(position)
	}
	return out, nil
}

func TestOllamaEmbeddingClient(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/embed" {
			t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
		}
		var in struct {
			Model     string
			Input     []string
			Truncate  *bool
			KeepAlive string `json:"keep_alive"`
			Options   map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if in.Model != "bge-m3" || in.Truncate == nil || *in.Truncate || in.KeepAlive != "10m" || in.Options["num_thread"] != float64(3) {
			t.Errorf("invalid embedding request: %+v", in)
		}
		switch in.Input[0] {
		case "unavailable":
			w.WriteHeader(503)
		case "redirect":
			http.Redirect(w, r, "http://example.com", 307)
		case "dimension":
			json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0}}})
		case "count":
			json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{unitVector(0)}})
		}
	}))
	defer ts.Close()
	e, err := NewOllamaEmbedder(ts.URL, "bge-m3")
	if err != nil {
		t.Fatal(err)
	}
	e.NumThreads = 3
	vectors, err := e.Embed(context.Background(), []string{"안녕하세요"})
	if err != nil || len(vectors) != 1 || len(vectors[0]) != 1024 {
		t.Fatalf("embedding failed: %v", err)
	}
	for _, tc := range []struct{ input, code string }{{"unavailable", "EMBEDDING_UNAVAILABLE"}, {"redirect", "EMBEDDING_UNAVAILABLE"}, {"dimension", "EMBEDDING_DIMENSION"}, {"count", "EMBEDDING_RESPONSE"}} {
		_, err := e.Embed(context.Background(), []string{tc.input})
		denied(t, err, tc.code)
	}
	for _, v := range [][]float32{make([]float32, 1024), unitVector(0)} {
		if v[0] != 0 {
			v[0] = float32(math.Inf(1))
		}
		denied(t, validateVectors([][]float32{v}, 1), "EMBEDDING_RESPONSE")
	}
	if _, err := NewOllamaEmbedder(ts.URL, "cloud-model"); err == nil {
		t.Fatal("cloud model accepted")
	}
}

func TestEmbeddingStorageSearchAndPermissions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := &testEmbedder{model: "test-local"}
	s.Embedder = e
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	viewer := inviteAs(t, s, admin, "vector-viewer@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "vectors", Name: "벡터"}).(Project)
	other := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "secret", Name: "비공개"}).(Project)
	mustCall(t, tools, admin, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	d := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 정책", Kind: "decision", Content: "구매 후 7일 이내 환불 가능합니다."}).(Document)
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "계정", Kind: "brief", Content: "비밀번호 변경 방법"})
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: other.ID, Title: "환불 비밀", Kind: "brief", Content: "비공개 환불 정보"})
	before := mustCall(t, tools, viewer, "get_embedding_status", ProjectInput{ProjectID: p.ID}).(map[string]any)
	if before["pending"] != float64(2) {
		t.Fatalf("chunks not queued: %v", before)
	}
	if n, err := s.ProcessEmbeddingBatch(ctx); err != nil || n != 3 {
		t.Fatalf("embedding batch: n=%d err=%v", n, err)
	}
	var stored int
	if err := s.DB.QueryRow("SELECT count(*) FROM chunks WHERE embedding_status='ready' AND public.vector_dims(embedding)=1024").Scan(&stored); err != nil || stored != 3 {
		t.Fatalf("vectors not stored: %d %v", stored, err)
	}
	result := mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "돈을 돌려 받고 싶어요", Mode: "semantic", Limit: 1}).(SearchResult)
	if len(result.Hits) != 1 || result.Hits[0].DocumentID != d.ID || result.Retrieval != "semantic" || result.UnindexedChunks != 0 {
		t.Fatalf("semantic retrieval: %+v", result)
	}
	lexical := mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "돈을 돌려 받고 싶어요", Mode: "lexical"}).(SearchResult)
	if len(lexical.Hits) != 0 {
		t.Fatal("test did not distinguish semantic from lexical retrieval")
	}
	calls := e.calls.Load()
	_, err := call(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: other.ID, Query: "환불", Mode: "semantic"})
	denied(t, err, "NOT_FOUND")
	_, err = call(t, tools, viewer, "get_embedding_status", ProjectInput{ProjectID: other.ID})
	denied(t, err, "NOT_FOUND")
	if e.calls.Load() != calls {
		t.Fatal("unauthorized request reached model")
	}
	_, err = call(t, tools, viewer, "reindex_project_embeddings", ProjectInput{ProjectID: p.ID})
	denied(t, err, "FORBIDDEN")
	keyOut, err := s.CreateKey(ctx, admin, KeyInput{Name: "scoped", Access: "read_only", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(t, tools, key, "search_knowledge", SearchInput{ProjectID: other.ID, Query: "환불", Mode: "semantic"})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, key, "reindex_project_embeddings", ProjectInput{ProjectID: p.ID})
	denied(t, err, "FORBIDDEN")
	if e.calls.Load() != calls {
		t.Fatal("scoped key invoked model outside scope")
	}
	s.Embedder = &testEmbedder{model: "new-local"}
	result = mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불", Mode: "semantic"}).(SearchResult)
	if len(result.Hits) != 0 || result.UnindexedChunks != 2 || result.Warning == "" {
		t.Fatalf("mixed model vectors: %+v", result)
	}
	if _, err = s.ProcessEmbeddingBatch(ctx); err != nil {
		t.Fatal(err)
	}
	archived := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 보관 자료", Kind: "history", Content: "과거 환불 정책", Status: "archived"}).(Document)
	if _, err = s.ProcessEmbeddingBatch(ctx); err != nil {
		t.Fatal(err)
	}
	result = mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불", Mode: "semantic"}).(SearchResult)
	for _, hit := range result.Hits {
		if hit.DocumentID == archived.ID {
			t.Fatal("archived vector leaked into active search")
		}
	}
	result = mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불", Mode: "semantic", Status: "archived"}).(SearchResult)
	if len(result.Hits) != 1 || result.Hits[0].DocumentID != archived.ID {
		t.Fatal("explicit archived semantic search failed")
	}
	result = mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불", Mode: "hybrid"}).(SearchResult)
	if result.Retrieval != "hybrid" || len(result.Hits) != 2 || result.Hits[0].DocumentID != d.ID {
		t.Fatalf("hybrid retrieval failed: %+v", result)
	}
	s.Embedder = &testEmbedder{model: "new-local", fn: func(context.Context, []string) ([][]float32, error) {
		return nil, problem("EMBEDDING_UNAVAILABLE", "test")
	}}
	result = mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불"}).(SearchResult)
	if result.Retrieval != "lexical-fallback" || result.Warning == "" || len(result.Hits) != 1 {
		t.Fatalf("silent fallback: %+v", result)
	}
	_, err = call(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "환불", Mode: "semantic"})
	denied(t, err, "EMBEDDING_UNAVAILABLE")
}

func TestEmbeddingEditLeaseAndRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "leases", Name: "임베딩 작업"}).(Project)
	d := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불", Kind: "brief", Content: "이전 내용"}).(Document)
	started, release := make(chan struct{}), make(chan struct{})
	s.Embedder = &testEmbedder{model: "local", fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return [][]float32{unitVector(0)}, nil
	}}
	workerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.ProcessEmbeddingBatch(workerCtx); done <- err }()
	select {
	case <-started:
	case <-workerCtx.Done():
		t.Fatal("worker did not start")
	}
	if n, err := s.ProcessEmbeddingBatch(ctx); err != nil || n != 0 {
		t.Fatalf("lease claimed twice: %d %v", n, err)
	}
	content := "새로운 내용"
	mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: 1, Reason: "작업 중 수정", Patch: DocumentPatch{Content: &content}})
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var ready int
	if err := s.DB.QueryRow("SELECT count(*) FROM chunks WHERE embedding IS NOT NULL").Scan(&ready); err != nil || ready != 0 {
		t.Fatalf("stale vector persisted: %d %v", ready, err)
	}
	s.Embedder = &testEmbedder{model: "local", fn: func(context.Context, []string) ([][]float32, error) {
		return nil, problem("EMBEDDING_UNAVAILABLE", "test failure")
	}}
	_, err := s.ProcessEmbeddingBatch(ctx)
	denied(t, err, "EMBEDDING_UNAVAILABLE")
	if n, err := s.ProcessEmbeddingBatch(ctx); err != nil || n != 0 {
		t.Fatalf("retry ignored backoff: %d %v", n, err)
	}
	if _, err := s.DB.Exec("UPDATE chunks SET embedding_status='processing',embedding_attempts=5,embedding_lease_until=now()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ProcessEmbeddingBatch(ctx); err != nil || n != 0 {
		t.Fatalf("exhausted lease repeated: %d %v", n, err)
	}
	status := mustCall(t, tools, admin, "get_embedding_status", ProjectInput{ProjectID: p.ID}).(map[string]any)
	if status["failed"] != float64(1) || status["retries_exhausted"] != float64(1) {
		t.Fatalf("stuck lease: %v", status)
	}
	mustCall(t, tools, admin, "reindex_project_embeddings", ProjectInput{ProjectID: p.ID})
	s.Embedder = &testEmbedder{model: "local"}
	if n, err := s.ProcessEmbeddingBatch(ctx); err != nil || n != 1 {
		t.Fatalf("reindex recovery failed: %d %v", n, err)
	}
}

// Optional local-model check. No cloud API or real project data is used.
func TestLiveOllamaStorageAndRetrieval(t *testing.T) {
	endpoint := os.Getenv("TEST_OLLAMA_URL")
	if endpoint == "" {
		t.Skip("set TEST_OLLAMA_URL to test a real local bge-m3 model")
	}
	s := testStore(t)
	e, err := NewOllamaEmbedder(endpoint, "bge-m3")
	if err != nil {
		t.Fatal(err)
	}
	s.Embedder = e
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "live-model", Name: "로컬 모델 검증"}).(Project)
	d := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 정책", Kind: "decision", Content: "고객은 구매 후 7일 이내 상품을 반품하면 결제 금액 전액을 환불받을 수 있습니다."}).(Document)
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "배포 절차", Kind: "history", Content: "새 버전을 배포하기 전에 자동 테스트를 실행하고 서버 상태를 확인합니다."})
	if n, err := s.ProcessEmbeddingBatch(context.Background()); err != nil || n != 2 {
		t.Fatalf("real local embedding failed: %d %v", n, err)
	}
	result := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "물건을 돌려주면 돈을 받을 수 있나요?", Mode: "semantic", Limit: 1}).(SearchResult)
	if len(result.Hits) != 1 || result.Hits[0].DocumentID != d.ID {
		t.Fatalf("real model retrieval failed: %+v", result)
	}
	t.Logf("local bge-m3: stored 2 documents as %d-dimensional vectors; Korean paraphrase retrieved refund policy (cosine %.4f)", EmbeddingDimensions, result.Hits[0].Score)
}

func TestParseEmbeddingThreads(t *testing.T) {
	for input, want := range map[string]int{"": DefaultEmbeddingThreads, " 8 ": 8, "0": 0, "64": 64} {
		if got, err := ParseEmbeddingThreads(input); err != nil || got != want {
			t.Errorf("ParseEmbeddingThreads(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"-1", "65", "four", "1.5"} {
		if _, err := ParseEmbeddingThreads(input); err == nil {
			t.Errorf("ParseEmbeddingThreads(%q) accepted", input)
		}
	}
}

func TestOllamaEmbeddingOmitsZeroThreads(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if _, ok := in["options"]; ok {
			t.Errorf("options sent with automatic threads: %v", in["options"])
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{unitVector(0)}})
	}))
	defer ts.Close()
	e, err := NewOllamaEmbedder(ts.URL, "bge-m3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"자동"}); err != nil {
		t.Fatal(err)
	}
}
