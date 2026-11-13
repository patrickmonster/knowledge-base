package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVectorProjection(t *testing.T) {
	ctx := context.Background()
	points, variance, err := projectVectors(ctx, [][]float64{{1, 0, 0}, {0, 1, 0}, {-1, 0, 0}, {0, -1, 0}})
	if err != nil || math.Abs(variance-1) > 1e-8 {
		t.Fatalf("planar variance lost: %v %v", variance, err)
	}
	for i := range points {
		for j := range points {
			distance := math.Hypot(points[i][0]-points[j][0], points[i][1]-points[j][1])
			want := 0.0
			if i != j {
				want = math.Sqrt(2)
			}
			if (i+2)%4 == j {
				want = 2
			}
			if math.Abs(distance-want) > 1e-6 {
				t.Fatalf("planar geometry changed: %d %d %v", i, j, distance)
			}
		}
	}
	for _, vectors := range [][][]float64{nil, {{1, 2}}, {{1, 2}, {2, 4}, {1, 2}}} {
		p, v, err := projectVectors(ctx, vectors)
		if err != nil || v != 0 {
			t.Fatalf("degenerate projection: %v %v", v, err)
		}
		for _, xy := range p {
			if math.Abs(xy[0])+math.Abs(xy[1]) > 1e-10 {
				t.Fatal("identical vectors should overlap")
			}
		}
	}
	_, _, err = projectVectors(ctx, [][]float64{{0, 0}})
	denied(t, err, "INVALID_VECTOR")
	_, _, err = projectVectors(ctx, [][]float64{{math.Inf(1)}})
	denied(t, err, "INVALID_VECTOR")
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = projectVectors(cancelCtx, [][]float64{{1, 0}, {0, 1}})
	if err == nil {
		t.Fatal("projection ignored cancellation")
	}
}

func TestVectorMapPermissionsAndBounds(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Embedder = &testEmbedder{model: "viz-model"}
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "viz-member@example.com")
	tools := NewTools(s)
	p := mustCall(t, tools, member, "create_project", CreateProjectInput{Slug: "viz-private", Name: "private vectors"}).(Project)
	d := mustCall(t, tools, member, "create_document", DocumentInput{ProjectID: p.ID, Title: "환불 정책", Content: "환불 설명", Kind: "decision"}).(Document)
	pending, err := s.VectorMap(ctx, member, VectorMapInput{ProjectID: p.ID})
	if err != nil || len(pending.(VectorMap).Points) != 0 {
		t.Fatalf("pending map: %v %v", pending, err)
	}
	if _, err = s.ProcessEmbeddingBatch(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := s.VectorMap(ctx, member, VectorMapInput{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	first := out.(VectorMap)
	if len(first.Points) != 1 || first.Eligible != 1 || first.Points[0].DocumentID != d.ID {
		t.Fatalf("map: %+v", first)
	}
	_, err = s.VectorMap(ctx, admin, VectorMapInput{ProjectID: p.ID})
	denied(t, err, "NOT_FOUND")
	_, err = s.VectorNeighbors(ctx, admin, VectorNeighborInput{ProjectID: p.ID, ChunkID: first.Points[0].ID})
	denied(t, err, "NOT_FOUND")
	other := mustCall(t, tools, member, "create_project", CreateProjectInput{Slug: "viz-other", Name: "other"}).(Project)
	mustCall(t, tools, member, "create_document", DocumentInput{ProjectID: other.ID, Title: "비공개 환불", Content: "비공개 본문", Kind: "brief"})
	mustCall(t, tools, member, "create_document", DocumentInput{ProjectID: p.ID, Title: "보관 환불", Content: "보관 본문", Kind: "history", Status: "archived"})
	if _, err = s.ProcessEmbeddingBatch(ctx); err != nil {
		t.Fatal(err)
	}
	// More than the response limit: deterministic prefix and truthful total.
	_, err = s.DB.Exec(`INSERT INTO chunks(id,document_id,position,content,search_vector,embedding,embedding_model,embedding_status)
	SELECT gen_random_uuid(),$1,g,'검증용 조각',''::tsvector,$2::public.vector,'viz-model','ready' FROM generate_series(1,505) g`, d.ID, vectorLiteral(unitVector(0)))
	if err != nil {
		t.Fatal(err)
	}
	out, err = s.VectorMap(ctx, member, VectorMapInput{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(VectorMap)
	if len(m.Points) != 500 || m.Eligible != 506 || m.Limit != 500 {
		t.Fatalf("unbounded or misleading map: %d/%d", len(m.Points), m.Eligible)
	}
	for _, point := range m.Points {
		if point.DocumentID != d.ID {
			t.Fatal("other project or archived document in map")
		}
	}
	raw, _ := json.Marshal(m)
	if bytes.Contains(raw, []byte(`"embedding":`)) {
		t.Fatal("raw vectors exposed")
	}
	neighbors, err := s.VectorNeighbors(ctx, member, VectorNeighborInput{ProjectID: p.ID, ChunkID: first.Points[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range neighbors.(map[string]any)["neighbors"].([]any) {
		n := value.(map[string]any)
		if n["document_id"] != d.ID || math.Abs(n["similarity"].(float64)-1) > 1e-6 {
			t.Fatalf("incorrect scoped similarity: %v", n)
		}
	}
	_, err = s.VectorNeighbors(ctx, member, VectorNeighborInput{ProjectID: other.ID, ChunkID: first.Points[0].ID})
	denied(t, err, "NOT_FOUND")
	keyOut, err := s.CreateKey(ctx, member, KeyInput{Name: "viz-scoped", Access: "read_only", ProjectID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.APIKey(ctx, keyOut.(map[string]any)["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.VectorMap(ctx, key, VectorMapInput{ProjectID: p.ID})
	denied(t, err, "FORBIDDEN")
	// Ready vectors from another model must not be mixed into the projection.
	s.Embedder = &testEmbedder{model: "changed-model"}
	out, err = s.VectorMap(ctx, member, VectorMapInput{ProjectID: p.ID})
	if err != nil || out.(VectorMap).Eligible != 0 {
		t.Fatal("mixed embedding models")
	}
	s.Embedder = &testEmbedder{model: "viz-model"}
	content := "수정된 문서"
	mustCall(t, tools, member, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: 1, Reason: "stale selection", Patch: DocumentPatch{Content: &content}})
	_, err = s.VectorNeighbors(ctx, member, VectorNeighborInput{ProjectID: p.ID, ChunkID: first.Points[0].ID})
	denied(t, err, "NOT_FOUND")
	var audits int
	if err = s.DB.QueryRow("SELECT count(*) FROM audit_events WHERE action='view_vector_map' AND outcome='denied'").Scan(&audits); err != nil || audits < 2 {
		t.Fatalf("denials not audited: %d %v", audits, err)
	}
}

func TestVectorHTTPRequiresSessionAndCSRF(t *testing.T) {
	s := testStore(t)
	s.Embedder = &testEmbedder{model: "viz-model"}
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, NewTools(s), admin, "create_project", CreateProjectInput{Slug: "http-viz", Name: "http"}).(Project)
	raw, err := s.Login(context.Background(), "admin@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := s.Session(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/vectors/map", "/api/vectors/neighbors"} {
		for _, tc := range []struct {
			cookie, csrf bool
			want         int
		}{{false, false, 401}, {true, false, 403}, {true, true, 200}} {
			body := `{"project_id":"` + p.ID + `"}`
			if strings.HasSuffix(path, "neighbors") {
				body = `{"project_id":"` + p.ID + `","chunk_id":"` + p.ID + `"}`
				if tc.want == 200 {
					tc.want = 404
				}
			}
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "http://localhost:8080")
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: "kb_session", Value: raw})
			}
			if tc.csrf {
				r.Header.Set("X-CSRF-Token", identity.CSRF)
			}
			w := httptest.NewRecorder()
			app.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%s: want %d, got %d: %s", path, tc.want, w.Code, w.Body.String())
			}
		}
	}
}
