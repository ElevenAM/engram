package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// fakeEmbedder maps substrings to fixed vectors so tests control similarity
// exactly. The first matching rule wins; unmatched text gets a zero vector.
type fakeEmbedder struct {
	rules []fakeRule
	fail  error
	calls int
}

type fakeRule struct {
	substr string
	vec    []float32
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	if f.fail != nil {
		return nil, f.fail
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = []float32{0, 0, 0}
		for _, r := range f.rules {
			if strings.Contains(t, r.substr) {
				out[i] = r.vec
				break
			}
		}
	}
	return out, nil
}

func (f *fakeEmbedder) Model() string { return "fake-model" }

func newVectorTestStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.CreateSession("s1", "engram", "/tmp/engram"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return s
}

func addObs(t *testing.T, s *Store, title, content string) int64 {
	t.Helper()
	id, err := s.AddObservation(AddObservationParams{
		SessionID: "s1", Type: "manual", Title: title, Content: content, Project: "engram",
	})
	if err != nil {
		t.Fatalf("add observation %q: %v", title, err)
	}
	return id
}

func TestVectorCodecRoundtrip(t *testing.T) {
	in := []float32{0.5, -1.25, 3.75, 0}
	out, err := DecodeVector(EncodeVector(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if in[i] != out[i] {
			t.Errorf("out[%d] = %v, want %v", i, out[i], in[i])
		}
	}
	if _, err := DecodeVector([]byte{1, 2, 3}); err == nil {
		t.Error("want error for blob length not divisible by 4")
	}
}

func TestCosine(t *testing.T) {
	if got := Cosine([]float32{1, 0}, []float32{1, 0}); math.Abs(got-1) > 1e-9 {
		t.Errorf("identical vectors: %v, want 1", got)
	}
	if got := Cosine([]float32{1, 0}, []float32{0, 1}); math.Abs(got) > 1e-9 {
		t.Errorf("orthogonal vectors: %v, want 0", got)
	}
	if got := Cosine([]float32{0, 0}, []float32{1, 0}); got != 0 {
		t.Errorf("zero vector: %v, want 0", got)
	}
	if got := Cosine([]float32{1}, []float32{1, 0}); got != 0 {
		t.Errorf("length mismatch: %v, want 0", got)
	}
}

func TestAddObservationStoresEmbedding(t *testing.T) {
	s := newVectorTestStore(t)
	s.SetEmbedder(&fakeEmbedder{rules: []fakeRule{{substr: "auth", vec: []float32{1, 0, 0}}}})

	id := addObs(t, s, "auth flow", "OAuth2 with PKCE")

	var blob []byte
	var model string
	if err := s.db.QueryRow(`SELECT embedding, embedding_model FROM observations WHERE id = ?`, id).Scan(&blob, &model); err != nil {
		t.Fatalf("read embedding: %v", err)
	}
	if model != "fake-model" {
		t.Errorf("embedding_model = %q, want fake-model", model)
	}
	vec, err := DecodeVector(blob)
	if err != nil || len(vec) != 3 || vec[0] != 1 {
		t.Errorf("stored vector = %v (err %v), want [1 0 0]", vec, err)
	}
}

func TestAddObservationSurvivesEmbedderFailure(t *testing.T) {
	s := newVectorTestStore(t)
	s.SetEmbedder(&fakeEmbedder{fail: errors.New("connection refused")})

	id := addObs(t, s, "some title", "some content")
	if id == 0 {
		t.Fatal("save failed when embedder was down")
	}
	var blob []byte
	if err := s.db.QueryRow(`SELECT embedding FROM observations WHERE id = ?`, id).Scan(&blob); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if blob != nil {
		t.Errorf("embedding = %v, want NULL after failed embed", blob)
	}
}

func TestSemanticSearchRanksBySimilarity(t *testing.T) {
	s := newVectorTestStore(t)
	s.SetEmbedder(&fakeEmbedder{rules: []fakeRule{
		{substr: "how do users sign in", vec: []float32{1, 0, 0}},
		{substr: "authentication", vec: []float32{0.95, 0.05, 0}},
		{substr: "database", vec: []float32{0, 1, 0}},
	}})

	authID := addObs(t, s, "authentication design", "sessions carried in cookies")
	addObs(t, s, "database schema", "twelve tables plus FTS")

	results, err := s.Search("how do users sign in", SearchOptions{Mode: SearchModeSemantic, Project: "engram"})
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if len(results) == 0 || results[0].ID != authID {
		t.Fatalf("results = %+v, want authentication design first", summarize(results))
	}
	// No lexical overlap between query and title — FTS5 alone would miss this.
	if results[0].Rank >= -0.9 {
		t.Errorf("rank = %v, want <= -0.9 (high cosine similarity)", results[0].Rank)
	}
}

func TestHybridFusesLexicalAndSemantic(t *testing.T) {
	s := newVectorTestStore(t)
	s.SetEmbedder(&fakeEmbedder{rules: []fakeRule{
		{substr: "login problems", vec: []float32{1, 0, 0}},
		{substr: "login handler", vec: []float32{0.9, 0.1, 0}}, // lexical + semantic match
		{substr: "authentication", vec: []float32{0.95, 0.05, 0}}, // semantic-only match
		{substr: "database", vec: []float32{0, 1, 0}},
	}})

	bothID := addObs(t, s, "login handler rewrite", "moved to middleware")
	semOnlyID := addObs(t, s, "authentication design", "sessions carried in cookies")
	addObs(t, s, "database schema", "twelve tables plus FTS")

	results, err := s.Search("login problems", SearchOptions{Mode: SearchModeHybrid, Project: "engram", MatchMode: "any"})
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("results = %+v, want both login handler and authentication design", summarize(results))
	}
	if results[0].ID != bothID {
		t.Errorf("first = %d, want %d (found by both rankers)", results[0].ID, bothID)
	}
	found := false
	for _, r := range results {
		if r.ID == semOnlyID {
			found = true
		}
	}
	if !found {
		t.Errorf("semantic-only match missing from hybrid results: %+v", summarize(results))
	}
}

func TestAutoModeWithoutEmbedderIsLexical(t *testing.T) {
	s := newVectorTestStore(t)
	addObs(t, s, "database schema", "twelve tables")

	results, err := s.Search("database", SearchOptions{Project: "engram"})
	if err != nil {
		t.Fatalf("auto-mode search without embedder: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want 1 lexical hit", summarize(results))
	}
}

func TestExplicitSemanticWithoutEmbedderErrors(t *testing.T) {
	s := newVectorTestStore(t)
	if _, err := s.Search("anything", SearchOptions{Mode: SearchModeSemantic}); err == nil {
		t.Fatal("want error for mode=semantic without embedder")
	}
	if _, err := s.Search("anything", SearchOptions{Mode: "bogus"}); err == nil {
		t.Fatal("want error for invalid mode")
	}
}

func TestHybridFallsBackWhenBackendDies(t *testing.T) {
	s := newVectorTestStore(t)
	fe := &fakeEmbedder{rules: []fakeRule{{substr: "database", vec: []float32{0, 1, 0}}}}
	s.SetEmbedder(fe)
	addObs(t, s, "database schema", "twelve tables")

	fe.fail = errors.New("ollama went away")
	results, err := s.Search("database", SearchOptions{Mode: SearchModeHybrid, Project: "engram"})
	if err != nil {
		t.Fatalf("hybrid search with dead backend: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want lexical fallback hit", summarize(results))
	}
}

func TestBackfillEmbeddings(t *testing.T) {
	s := newVectorTestStore(t)
	addObs(t, s, "authentication design", "sessions carried in cookies")
	addObs(t, s, "database schema", "twelve tables")

	fe := &fakeEmbedder{rules: []fakeRule{
		{substr: "authentication", vec: []float32{1, 0, 0}},
		{substr: "database", vec: []float32{0, 1, 0}},
	}}
	s.SetEmbedder(fe)

	done, err := s.BackfillEmbeddings(context.Background(), 1, nil)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if done != 2 {
		t.Errorf("backfilled %d, want 2", done)
	}

	stats, err := s.GetEmbeddingStats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.ActiveObservations != 2 || stats.Embedded != 2 || stats.Models["fake-model"] != 2 {
		t.Errorf("stats = %+v, want full coverage under fake-model", stats)
	}

	// Re-running is a no-op.
	done, err = s.BackfillEmbeddings(context.Background(), 10, nil)
	if err != nil || done != 0 {
		t.Errorf("second backfill = (%d, %v), want (0, nil)", done, err)
	}
}

func summarize(results []SearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Title
	}
	return out
}
