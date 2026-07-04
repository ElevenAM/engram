package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbedBatch(t *testing.T) {
	var gotPath string
	var gotReq embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{1, 0}, {0, 1}}})
	}))
	defer srv.Close()

	c := New(srv.URL, "test-model")
	vecs, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotPath != "/api/embed" {
		t.Errorf("path = %q, want /api/embed", gotPath)
	}
	if gotReq.Model != "test-model" || len(gotReq.Input) != 2 {
		t.Errorf("request = %+v", gotReq)
	}
	if len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][1] != 1 {
		t.Errorf("vecs = %v", vecs)
	}
}

func TestEmbedLegacyFallback(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			w.WriteHeader(http.StatusNotFound)
		case "/api/embeddings":
			calls++
			json.NewEncoder(w).Encode(legacyEmbedResponse{Embedding: []float32{float32(calls)}})
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "m")
	vecs, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if calls != 2 || len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Errorf("calls=%d vecs=%v", calls, vecs)
	}
}

func TestEmbedCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{1}}})
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "m").Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("want error on count mismatch, got nil")
	}
}

func TestEmbedServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "m").Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want error on 500, got nil")
	}
}

func TestFromEnv(t *testing.T) {
	cases := []struct {
		value   string
		enabled bool
	}{
		{"", false},
		{"off", false},
		{"nonsense", false},
		{"ollama", true},
		{"OLLAMA", true},
		{"on", true},
	}
	for _, tc := range cases {
		t.Setenv("ENGRAM_EMBEDDINGS", tc.value)
		c := FromEnv()
		if (c != nil) != tc.enabled {
			t.Errorf("ENGRAM_EMBEDDINGS=%q: client=%v, want enabled=%v", tc.value, c, tc.enabled)
		}
	}

	t.Setenv("ENGRAM_EMBEDDINGS", "ollama")
	t.Setenv("ENGRAM_EMBEDDINGS_MODEL", "custom")
	if got := FromEnv().Model(); got != "custom" {
		t.Errorf("Model() = %q, want custom", got)
	}
}
