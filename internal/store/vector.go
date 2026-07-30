package store

// Semantic search over the reserved embedding columns (embedding,
// embedding_model, embedding_created_at — see the schema reservation in
// store.go). Vectors are stored as little-endian float32 BLOBs, tagged with
// the model that produced them so vectors from different models are never
// compared. There is no ANN index: candidates are the project-filtered active
// rows (capped), scored by cosine similarity in Go. At engram's scale
// (thousands of observations, results capped at 20) this is well under a
// millisecond.

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Embedder produces text embeddings. Implemented by embed.Client; stubbed in
// tests (mirrors the SemanticRunner injection pattern used for conflict
// detection).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
}

// Search modes accepted in SearchOptions.Mode. Empty string means auto:
// hybrid when an embedder is configured, lexical otherwise.
const (
	SearchModeLexical  = "lexical"
	SearchModeSemantic = "semantic"
	SearchModeHybrid   = "hybrid"
)

// semanticCandidateCap bounds how many embedded rows a semantic search scans,
// newest first. Brute-force cosine over this many 384-dim vectors is ~1ms.
const semanticCandidateCap = 5000

// embedTextLimit bounds how much text is sent to the embedding backend.
// Embedding models truncate to a few hundred tokens anyway; the title plus
// the head of the content carries the semantic signal.
const embedTextLimit = 2000

// rrfK is the standard Reciprocal Rank Fusion constant used to merge the
// lexical and semantic result lists in hybrid mode.
const rrfK = 60

// saveEmbedTimeout caps the embedding call made inline on the save path.
// Saves run through the single-worker MCP write queue, so a slow-but-alive
// backend must not be allowed to hold the queue for the embed client's full
// timeout — a save that misses this window is picked up by backfill.
const saveEmbedTimeout = 3 * time.Second

// SearchInfo reports how a search was actually served, so callers can tell a
// healthy hybrid response from one that silently degraded to keyword-only.
type SearchInfo struct {
	// Mode that produced the results: lexical, semantic, or hybrid.
	Mode string `json:"mode"`
	// Degraded is true when hybrid was requested (or auto-resolved) but the
	// semantic leg failed, so results are lexical-only.
	Degraded bool `json:"degraded,omitempty"`
	// DegradedReason is the backend error that caused the degradation.
	DegradedReason string `json:"degraded_reason,omitempty"`
}

var embedWarnOnce sync.Once

// SetEmbedder configures the embedding backend. nil disables semantic search
// (saves and search behave exactly as before).
func (s *Store) SetEmbedder(e Embedder) {
	s.embedder = e
}

// EncodeVector serializes a vector as a little-endian float32 BLOB.
func EncodeVector(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(f))
	}
	return buf
}

// DecodeVector deserializes a little-endian float32 BLOB.
func DecodeVector(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("vector blob length %d is not a multiple of 4", len(b))
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v, nil
}

// Cosine returns the cosine similarity of two vectors (0 when either has zero
// norm or lengths differ).
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// embedText builds the text that represents an observation in vector space.
func embedText(title, content string) string {
	text := strings.TrimSpace(title + "\n" + content)
	if len(text) > embedTextLimit {
		text = text[:embedTextLimit]
	}
	return text
}

// embedObservation computes and stores the embedding for one observation.
// Best-effort by design: callers must never fail a save because the
// embedding backend is down — unembedded rows are picked up later by
// BackfillEmbeddings.
func (s *Store) embedObservation(id int64, title, content string) error {
	if s.embedder == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), saveEmbedTimeout)
	defer cancel()
	vecs, err := s.embedder.Embed(ctx, []string{embedText(title, content)})
	if err != nil {
		return err
	}
	if len(vecs) != 1 {
		return fmt.Errorf("embed observation %d: got %d vectors", id, len(vecs))
	}
	_, err = s.execHook(s.db,
		`UPDATE observations SET embedding = ?, embedding_model = ?, embedding_created_at = datetime('now') WHERE id = ? AND deleted_at IS NULL`,
		EncodeVector(vecs[0]), s.embedder.Model(), id,
	)
	return err
}

// warnEmbedUnavailable logs the first embedding-backend failure of the
// process to stderr (stdout is reserved for the MCP protocol).
func warnEmbedUnavailable(err error) {
	embedWarnOnce.Do(func() {
		log.Printf("engram: embedding backend unavailable, falling back to lexical search (%v)", err)
	})
}

// resolveSearchMode validates opts.Mode and resolves auto ("") to hybrid or
// lexical depending on whether an embedder is configured. Explicitly
// requesting semantic or hybrid without a configured embedder is an error;
// auto never is.
func (s *Store) resolveSearchMode(mode string) (string, error) {
	switch mode {
	case "":
		if s.embedder != nil {
			return SearchModeHybrid, nil
		}
		return SearchModeLexical, nil
	case SearchModeLexical:
		return SearchModeLexical, nil
	case SearchModeSemantic, SearchModeHybrid:
		if s.embedder == nil {
			return "", fmt.Errorf("mode %q requires embeddings: set ENGRAM_EMBEDDINGS=ollama (and run `engram embed backfill`)", mode)
		}
		return mode, nil
	default:
		return "", fmt.Errorf("invalid mode %q: must be \"lexical\", \"semantic\", or \"hybrid\"", mode)
	}
}

// searchSemantic returns the top-limit observations by cosine similarity to
// the query, honoring the same type/project/scope filters as lexical search.
func (s *Store) searchSemantic(query string, opts SearchOptions, limit int) ([]SearchResult, error) {
	qVecs, err := s.embedder.Embed(context.Background(), []string{query})
	if err != nil {
		return nil, err
	}
	if len(qVecs) != 1 {
		return nil, fmt.Errorf("semantic search: got %d query vectors", len(qVecs))
	}
	qVec := qVecs[0]

	sqlQ := `
		SELECT id, embedding FROM observations
		WHERE deleted_at IS NULL AND embedding IS NOT NULL AND embedding_model = ?
	`
	args := []any{s.embedder.Model()}
	if opts.Type != "" {
		sqlQ += " AND type = ?"
		args = append(args, opts.Type)
	}
	if opts.Project != "" {
		sqlQ += " AND LOWER(project) = ?"
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		sqlQ += " AND scope = ?"
		args = append(args, normalizeScope(opts.Scope))
	}
	sqlQ += " ORDER BY datetime(updated_at) DESC LIMIT ?"
	args = append(args, semanticCandidateCap)

	rows, err := s.queryItHook(s.db, sqlQ, args...)
	if err != nil {
		return nil, fmt.Errorf("semantic search: %w", err)
	}
	defer rows.Close()

	type scored struct {
		id  int64
		sim float64
	}
	var candidates []scored
	var corruptVectors []int64
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		vec, err := DecodeVector(blob)
		if err != nil {
			// Corrupt blob: don't fail the search, but don't skip silently
			// either — a non-NULL blob with a matching model tag is invisible
			// to both backfill and embed status. NULL it out so the row
			// re-enrolls in the backfill predicate and self-heals.
			corruptVectors = append(corruptVectors, id)
			continue
		}
		candidates = append(candidates, scored{id: id, sim: Cosine(qVec, vec)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	s.healCorruptVectors(corruptVectors)

	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].sim > candidates[j].sim })
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	ids := make([]int64, len(candidates))
	for i, c := range candidates {
		ids[i] = c.id
	}
	byID, err := s.observationsByID(ids)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(candidates))
	for _, c := range candidates {
		obs, ok := byID[c.id]
		if !ok {
			continue
		}
		// Rank keeps the "lower is better" convention of FTS5 bm25.
		results = append(results, SearchResult{Observation: obs, Rank: -c.sim})
	}
	return results, nil
}

// healCorruptVectors clears embeddings that failed to decode so the rows
// re-enter the backfill predicate (embedding IS NULL) instead of remaining
// permanently invisible to semantic search, backfill, and embed status.
func (s *Store) healCorruptVectors(ids []int64) {
	if len(ids) == 0 {
		return
	}
	log.Printf("engram: %d corrupt embedding blob(s) detected (observation ids %v); clearing so `engram embed backfill` re-embeds them", len(ids), ids)
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := s.execHook(s.db,
		`UPDATE observations SET embedding = NULL, embedding_model = NULL, embedding_created_at = NULL WHERE id IN (`+placeholders+`)`,
		args...,
	); err != nil {
		log.Printf("engram: failed to clear corrupt embeddings: %v", err)
	}
}

// observationsByID loads full observation rows for the given IDs.
func (s *Store) observationsByID(ids []int64) (map[int64]Observation, error) {
	if len(ids) == 0 {
		return map[int64]Observation{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	obs, err := s.queryObservations(
		`SELECT `+observationSelectColumns+` FROM observations o WHERE o.id IN (`+placeholders+`) AND o.deleted_at IS NULL`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Observation, len(obs))
	for _, o := range obs {
		byID[o.ID] = o
	}
	return byID, nil
}

// rrfMerge fuses the lexical and semantic result lists with Reciprocal Rank
// Fusion: score(doc) = Σ 1/(rrfK + position). Documents found by both lists
// rank above documents found by only one.
func rrfMerge(lists [][]SearchResult, limit int) []SearchResult {
	type fused struct {
		result SearchResult
		score  float64
		order  int // first-seen order, for deterministic ties
	}
	byID := make(map[int64]*fused)
	seen := 0
	for _, list := range lists {
		for pos, r := range list {
			f, ok := byID[r.ID]
			if !ok {
				f = &fused{result: r, order: seen}
				seen++
				byID[r.ID] = f
			}
			f.score += 1.0 / float64(rrfK+pos+1)
		}
	}
	all := make([]*fused, 0, len(byID))
	for _, f := range byID {
		all = append(all, f)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].order < all[j].order
	})
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]SearchResult, len(all))
	for i, f := range all {
		f.result.Rank = -f.score
		out[i] = f.result
	}
	return out
}

// EmbeddingStats reports embedding coverage for `engram embed status`.
type EmbeddingStats struct {
	ActiveObservations int64            `json:"active_observations"`
	Embedded           int64            `json:"embedded"`
	Models             map[string]int64 `json:"models,omitempty"`
}

// GetEmbeddingStats returns embedding coverage over active observations.
func (s *Store) GetEmbeddingStats() (*EmbeddingStats, error) {
	stats := &EmbeddingStats{Models: map[string]int64{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL`).Scan(&stats.ActiveObservations); err != nil {
		return nil, err
	}
	rows, err := s.queryItHook(s.db, `SELECT embedding_model, COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NOT NULL GROUP BY embedding_model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var n int64
		if err := rows.Scan(&model, &n); err != nil {
			return nil, err
		}
		stats.Models[model] = n
		stats.Embedded += n
	}
	return stats, rows.Err()
}

// BackfillEmbeddings embeds every active observation that has no vector for
// the current model, in batches. progress (optional) is called after each
// batch with the running count. Returns how many observations were embedded.
func (s *Store) BackfillEmbeddings(ctx context.Context, batchSize int, progress func(done int64)) (int64, error) {
	if s.embedder == nil {
		return 0, fmt.Errorf("embeddings not configured: set ENGRAM_EMBEDDINGS=ollama")
	}
	if batchSize <= 0 {
		batchSize = 32
	}
	model := s.embedder.Model()

	var done int64
	for {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		type row struct {
			id             int64
			title, content string
		}
		rows, err := s.queryItHook(s.db,
			`SELECT id, title, content FROM observations
			 WHERE deleted_at IS NULL AND (embedding IS NULL OR embedding_model IS NULL OR embedding_model != ?)
			 ORDER BY id LIMIT ?`,
			model, batchSize,
		)
		if err != nil {
			return done, err
		}
		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.title, &r.content); err != nil {
				rows.Close()
				return done, err
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return done, err
		}
		if len(batch) == 0 {
			return done, nil
		}

		texts := make([]string, len(batch))
		for i, r := range batch {
			texts[i] = embedText(r.title, r.content)
		}
		vecs, err := s.embedder.Embed(ctx, texts)
		if err != nil {
			return done, err
		}
		if len(vecs) != len(batch) {
			return done, fmt.Errorf("backfill: got %d vectors for %d texts", len(vecs), len(batch))
		}
		now := time.Now().UTC().Format("2006-01-02 15:04:05")
		for i, r := range batch {
			if _, err := s.execHook(s.db,
				`UPDATE observations SET embedding = ?, embedding_model = ?, embedding_created_at = ? WHERE id = ?`,
				EncodeVector(vecs[i]), model, now, r.id,
			); err != nil {
				return done, err
			}
			done++
		}
		if progress != nil {
			progress(done)
		}
	}
}
