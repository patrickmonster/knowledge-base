package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const EmbeddingDimensions = 1024

type Embedder interface {
	Model() string
	Embed(context.Context, []string) ([][]float32, error)
}
type OllamaEmbedder struct {
	baseURL, model string
	client         *http.Client
}

func NewOllamaEmbedder(baseURL, model string) (*OllamaEmbedder, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("OLLAMA_BASE_URL must be an HTTP(S) origin")
	}
	if strings.TrimSpace(model) == "" || strings.Contains(strings.ToLower(model), "cloud") {
		return nil, fmt.Errorf("EMBEDDING_MODEL must name a local model")
	}
	return &OllamaEmbedder{baseURL: strings.TrimSuffix(baseURL, "/"), model: model, client: &http.Client{Timeout: 120 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (e *OllamaEmbedder) Model() string { return e.model }

// Embed calls the locally hosted Ollama model. No cloud provider or API key is used.
func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 || len(texts) > 8 {
		return nil, problem("EMBEDDING_INPUT", "Use batches of 1–8 text chunks.")
	}
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			return nil, problem("EMBEDDING_INPUT", "Cannot embed empty text.")
		}
	}
	body, _ := json.Marshal(map[string]any{"model": e.model, "input": texts, "truncate": false, "keep_alive": "10m"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := e.client.Do(req)
	if err != nil {
		return nil, problem("EMBEDDING_UNAVAILABLE", "Local embedding server could not be reached or timed out.")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, problem("EMBEDDING_UNAVAILABLE", fmt.Sprintf("Local embedding server returned HTTP %d. Check model availability and input size.", res.StatusCode))
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out); err != nil {
		return nil, problem("EMBEDDING_RESPONSE", "Invalid embedding response.")
	}
	if err := validateVectors(out.Embeddings, len(texts)); err != nil {
		return nil, err
	}
	return out.Embeddings, nil
}
func validateVectors(vectors [][]float32, count int) error {
	if len(vectors) != count {
		return problem("EMBEDDING_RESPONSE", "Embedding count does not match input count.")
	}
	for _, vector := range vectors {
		if len(vector) != EmbeddingDimensions {
			return problem("EMBEDDING_DIMENSION", fmt.Sprintf("Expected %d-dimensional vectors; check EMBEDDING_MODEL.", EmbeddingDimensions))
		}
		var magnitude float64
		for _, value := range vector {
			v := float64(value)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return problem("EMBEDDING_RESPONSE", "Embedding contains non-finite values.")
			}
			magnitude += v * v
		}
		if magnitude == 0 {
			return problem("EMBEDDING_RESPONSE", "Zero vectors cannot be used for cosine search.")
		}
	}
	return nil
}
func vectorLiteral(vector []float32) string { b, _ := json.Marshal(vector); return string(b) }
