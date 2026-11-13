package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
)

const vectorMapLimit = 500

type VectorMapInput struct {
	ProjectID string `json:"project_id"`
	Status    string `json:"status,omitempty"`
}
type VectorPoint struct {
	ID         string  `json:"id"`
	DocumentID string  `json:"document_id"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind"`
	Position   int     `json:"position"`
	Revision   int     `json:"revision"`
	Excerpt    string  `json:"excerpt"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
}
type VectorMap struct {
	Points            []VectorPoint `json:"points"`
	Model             string        `json:"model"`
	Eligible          int           `json:"eligible"`
	Limit             int           `json:"limit"`
	ExplainedVariance float64       `json:"explained_variance"`
	EmbeddingStatus   any           `json:"embedding_status"`
}

// Only projected coordinates leave the server. Each request rechecks membership
// and is audited, including requests by global administrators.
func (s *Store) VectorMap(ctx context.Context, actor Identity, in VectorMapInput) (any, error) {
	return s.Perform(ctx, actor, "view_vector_map", in.ProjectID, false, false, func(tx *sql.Tx) (any, error) {
		status, err := s.embeddingStatus(ctx, tx, actor, in.ProjectID)
		if err != nil {
			return nil, err
		}
		if in.Status == "" {
			in.Status = "active"
		}
		if !validStatus(in.Status) {
			return nil, problem("INVALID_INPUT", "Unknown document status.")
		}
		out := VectorMap{Points: []VectorPoint{}, Limit: vectorMapLimit, EmbeddingStatus: status}
		if s.Embedder == nil {
			return out, nil
		}
		out.Model = s.Embedder.Model()
		// Count and selected rows share one SQL snapshot, even while indexing runs.
		rows, err := tx.QueryContext(ctx, `SELECT c.id,d.id,d.title,d.kind,c.position,d.revision,left(c.content,240),c.embedding::text,count(*) OVER()
		FROM chunks c JOIN documents d ON d.id=c.document_id
		WHERE d.project_id=$1 AND d.status=$2 AND c.embedding_status='ready' AND c.embedding_model=$3 AND c.embedding IS NOT NULL
		ORDER BY d.updated_at DESC,d.id,c.position LIMIT $4`, in.ProjectID, in.Status, out.Model, vectorMapLimit)
		if err != nil {
			return nil, err
		}
		vectors := [][]float64{}
		for rows.Next() {
			var p VectorPoint
			var raw string
			if err = rows.Scan(&p.ID, &p.DocumentID, &p.Title, &p.Kind, &p.Position, &p.Revision, &p.Excerpt, &raw, &out.Eligible); err != nil {
				rows.Close()
				return nil, err
			}
			var vector []float64
			if err = json.Unmarshal([]byte(raw), &vector); err != nil {
				rows.Close()
				return nil, err
			}
			out.Points = append(out.Points, p)
			vectors = append(vectors, vector)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		coords, variance, err := projectVectors(ctx, vectors)
		if err != nil {
			return nil, err
		}
		for i := range out.Points {
			out.Points[i].X = coords[i][0]
			out.Points[i].Y = coords[i][1]
		}
		out.ExplainedVariance = variance
		return out, nil
	})
}

type VectorNeighborInput struct {
	ProjectID string `json:"project_id"`
	ChunkID   string `json:"chunk_id"`
	Status    string `json:"status,omitempty"`
}

func (s *Store) VectorNeighbors(ctx context.Context, actor Identity, in VectorNeighborInput) (any, error) {
	return s.Perform(ctx, actor, "view_vector_neighbors", in.ProjectID, false, false, func(tx *sql.Tx) (any, error) {
		if _, err := projectAccess(ctx, tx, actor.User.ID, in.ProjectID, false); err != nil {
			return nil, err
		}
		if err := validID(in.ChunkID); err != nil {
			return nil, err
		}
		if in.Status == "" {
			in.Status = "active"
		}
		if !validStatus(in.Status) {
			return nil, problem("INVALID_INPUT", "Unknown document status.")
		}
		if s.Embedder == nil {
			return nil, problem("EMBEDDING_DISABLED", "Local embeddings are disabled.")
		}
		// Both selected content and neighbors use one snapshot; document edits may
		// replace chunk IDs, in which case the client must refresh the map.
		out, err := one[map[string]any](ctx, tx, `WITH scoped AS MATERIALIZED (
		 SELECT c.id,c.document_id,d.title,d.kind,c.position,d.revision,c.content,c.embedding
		 FROM chunks c JOIN documents d ON d.id=c.document_id
		 WHERE d.project_id=$1 AND d.status=$3 AND c.embedding_status='ready' AND c.embedding_model=$4 AND c.embedding IS NOT NULL
		), selected AS (SELECT * FROM scoped WHERE id=$2), neighbors AS (
		 SELECT c.id,c.document_id,c.title,c.kind,c.position,c.revision,left(c.content,240) AS excerpt,
		 1-(c.embedding OPERATOR(public.<=>) s.embedding) AS similarity
		 FROM scoped c CROSS JOIN selected s WHERE c.id<>s.id
		 ORDER BY c.embedding OPERATOR(public.<=>) s.embedding,c.id LIMIT 5
		) SELECT jsonb_build_object('selected',to_jsonb(s)-'embedding','neighbors',COALESCE((SELECT jsonb_agg(n) FROM neighbors n),'[]'::jsonb))
		FROM selected s`, in.ProjectID, in.ChunkID, in.Status, s.Embedder.Model())
		return out, notFound(err)
	})
}

// Matrix-free two-component PCA of normalized embeddings. Normalization makes
// the geometry consistent with cosine retrieval. No random layout or jitter is
// added: identical vectors legitimately overlap. Projection is sample-relative.
func projectVectors(ctx context.Context, vectors [][]float64) ([][2]float64, float64, error) {
	n := len(vectors)
	coords := make([][2]float64, n)
	if n == 0 {
		return coords, 0, nil
	}
	d := len(vectors[0])
	if d == 0 {
		return nil, 0, problem("INVALID_VECTOR", "Empty vector.")
	}
	mean := make([]float64, d)
	data := make([][]float64, n)
	for i, v := range vectors {
		if len(v) != d {
			return nil, 0, problem("INVALID_VECTOR", "Inconsistent dimensions.")
		}
		var norm float64
		for _, x := range v {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, 0, problem("INVALID_VECTOR", "Non-finite vector.")
			}
			norm += x * x
		}
		if norm <= 0 {
			return nil, 0, problem("INVALID_VECTOR", "Zero vector.")
		}
		norm = math.Sqrt(norm)
		data[i] = make([]float64, d)
		for j, x := range v {
			data[i][j] = x / norm
			mean[j] += data[i][j] / float64(n)
		}
	}
	diag := make([]float64, d)
	var total float64
	for _, v := range data {
		for j := range v {
			v[j] -= mean[j]
			diag[j] += v[j] * v[j]
			total += v[j] * v[j]
		}
	}
	if total < 1e-12 {
		return coords, 0, nil
	}
	var axes [][]float64
	var captured float64
	for component := 0; component < 2; component++ {
		seed := 0
		for j := range diag {
			if diag[j] > diag[seed] {
				seed = j
			}
		}
		if diag[seed] < 1e-12 {
			break
		}
		axis := make([]float64, d)
		// A dense deterministic start avoids getting stuck on a coordinate block
		// whose diagonal variance is large but whose eigenvalue is not maximal.
		for j := range axis {
			axis[j] = math.Sin(float64(j+1)*1.23456789 + float64(component)*2.3456789)
		}
		orthogonalize := func(v []float64) {
			for _, prior := range axes {
				dot := 0.0
				for j := range v {
					dot += v[j] * prior[j]
				}
				for j := range v {
					v[j] -= dot * prior[j]
				}
			}
		}
		orthogonalize(axis)
		for iteration := 0; iteration < 64; iteration++ {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			next := make([]float64, d)
			for _, v := range data {
				dot := 0.0
				for j, x := range v {
					dot += x * axis[j]
				}
				for j, x := range v {
					next[j] += x * dot
				}
			}
			orthogonalize(next)
			norm := 0.0
			for _, x := range next {
				norm += x * x
			}
			if norm < 1e-24 {
				break
			}
			norm = math.Sqrt(norm)
			delta := 0.0
			for j := range next {
				next[j] /= norm
				delta += (next[j] - axis[j]) * (next[j] - axis[j])
			}
			axis = next
			if delta < 1e-14 {
				break
			}
		}
		pivot := 0
		for j := range axis {
			if math.Abs(axis[j]) > math.Abs(axis[pivot]) {
				pivot = j
			}
		}
		if axis[pivot] < 0 {
			for j := range axis {
				axis[j] = -axis[j]
			}
		}
		energy := 0.0
		for i, v := range data {
			for j, x := range v {
				coords[i][component] += x * axis[j]
			}
			energy += coords[i][component] * coords[i][component]
		}
		for j := range diag {
			diag[j] = math.Max(0, diag[j]-energy*axis[j]*axis[j])
		}
		captured += energy
		axes = append(axes, axis)
	}
	return coords, math.Min(1, captured/total), nil
}
