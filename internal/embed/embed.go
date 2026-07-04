// Package embed provides text-embedding clients for semantic search.
//
// The feature is opt-in via environment variables (mirroring the
// ENGRAM_AGENT_CLI pattern used for LLM-judged conflict detection):
//
//	ENGRAM_EMBEDDINGS        "ollama" enables embeddings; unset/"" /"off" disables
//	ENGRAM_EMBEDDINGS_URL    Ollama base URL   (default http://localhost:11434)
//	ENGRAM_EMBEDDINGS_MODEL  embedding model   (default all-minilm, 384-dim)
//
// When disabled — or when the backend is unreachable — engram behaves exactly
// as before: saves succeed without embeddings and search falls back to FTS5.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL = "http://localhost:11434"
	defaultModel   = "all-minilm"
)

// Client embeds text via a local Ollama server. Safe for concurrent use.
type Client struct {
	baseURL string
	model   string
	hc      *http.Client
}

// New returns a Client for the given Ollama base URL and model.
func New(baseURL, model string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if model == "" {
		model = defaultModel
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		// Generous timeout: the first call after ollama starts loads the
		// model into memory, which can take several seconds.
		hc: &http.Client{Timeout: 30 * time.Second},
	}
}

// FromEnv returns a Client when ENGRAM_EMBEDDINGS enables the feature, or nil
// when it is unset/disabled. Unknown provider values are treated as disabled
// so a typo can never break saves or search.
func FromEnv() *Client {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("ENGRAM_EMBEDDINGS"))) {
	case "ollama", "on", "1", "true":
		return New(
			strings.TrimSpace(os.Getenv("ENGRAM_EMBEDDINGS_URL")),
			strings.TrimSpace(os.Getenv("ENGRAM_EMBEDDINGS_MODEL")),
		)
	default:
		return nil
	}
}

// Model returns the embedding model identifier, used to tag stored vectors so
// vectors from different models are never compared against each other.
func (c *Client) Model() string { return c.model }

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type legacyEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type legacyEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Embed returns one vector per input text, in order. It uses the batch
// /api/embed endpoint and falls back to the older per-prompt /api/embeddings
// endpoint when the server predates /api/embed.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(embedRequest{Model: c.model, Input: texts})
	if err != nil {
		return nil, err
	}
	status, respBody, err := c.post(ctx, "/api/embed", body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return c.embedLegacy(ctx, texts)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("embed: %s returned %d: %s", c.baseURL, status, truncateErr(respBody))
	}

	var parsed embedResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if len(parsed.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed: got %d embeddings for %d inputs", len(parsed.Embeddings), len(texts))
	}
	return parsed.Embeddings, nil
}

// embedLegacy calls /api/embeddings once per text (pre-0.3.4 Ollama).
func (c *Client) embedLegacy(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		body, err := json.Marshal(legacyEmbedRequest{Model: c.model, Prompt: t})
		if err != nil {
			return nil, err
		}
		status, respBody, err := c.post(ctx, "/api/embeddings", body)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("embed: %s returned %d: %s", c.baseURL, status, truncateErr(respBody))
		}
		var parsed legacyEmbedResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return nil, fmt.Errorf("embed: decode response: %w", err)
		}
		out = append(out, parsed.Embedding)
	}
	return out, nil
}

func (c *Client) post(ctx context.Context, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("embed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("embed: read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

func truncateErr(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
