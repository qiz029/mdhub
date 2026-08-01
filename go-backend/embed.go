package main

// Local embedding semantic search: an optional second ranking signal on top
// of the in-memory bigram keyword search. Vectors come from a local
// OpenAI-compatible embedding endpoint (Ollama running Qwen3-Embedding-0.6B
// on CPU), are stored in the `embeddings` table as little-endian float32
// blobs and mirrored into the in-memory embedIndex (guarded by mu, like
// searchIndex). Disabled entirely when MDHUB_EMBED_URL is empty — same
// philosophy as the classifier's empty API key.
//
// Indexing runs on a background queue (same channel + seen map + single
// worker + WaitGroup pattern as classify.go), triggered at write time and
// by POST /api/reembed; the read path only embeds the query sentence.

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

var (
	embedBaseURL = getEnv("MDHUB_EMBED_URL", "") // empty = semantic search disabled
	embedModel   = getEnv("MDHUB_EMBED_MODEL", "qwen3-embedding:0.6b")

	embedJobs = newKeyedJobQueue[string]("embed", 500)

	embedIndex = map[string][]float32{} // slug -> vector; guarded by mu (main.go)
)

const (
	embeddingChunkRunes = 512
	maxEmbeddingChunks  = 6
	maxEmbeddingTitle   = 120
)

// ---- vector codec ----

// encodeVec serializes a vector as 4-byte little-endian float32.
func encodeVec(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeVec is the inverse of encodeVec; returns nil when the blob length
// is not a multiple of 4.
func decodeVec(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// cosine returns the cosine similarity of two vectors; 0 for mismatched
// lengths or a zero vector.
func cosine(a, b []float32) float64 {
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
	return dot / math.Sqrt(na*nb)
}

// ---- embed API ----

// embedText performs one OpenAI-compatible embeddings request and returns
// the first vector. Pure apart from the HTTP call, so it is testable
// against httptest (via the embedBaseURL/embedModel vars).
func embedText(client *http.Client, text string) ([]float32, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": embedModel,
		"input": text,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", strings.TrimRight(embedBaseURL, "/")+"/v1/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("embed http status %d", resp.StatusCode)
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embed returned no vector")
	}
	return out.Data[0].Embedding, nil
}

// embeddingChunks samples a document from beginning to end instead of
// representing long notes by their introduction alone. Every chunk retains
// the title, and large documents are evenly sampled at a bounded cost.
func embeddingChunks(title, content string) []string {
	titleRunes := []rune(strings.TrimSpace(title))
	if len(titleRunes) > maxEmbeddingTitle {
		titleRunes = titleRunes[:maxEmbeddingTitle]
	}
	prefix := string(titleRunes) + "\n"
	bodyRunes := []rune(content)
	bodySize := embeddingChunkRunes - len([]rune(prefix))
	if bodySize < 1 {
		return []string{string([]rune(prefix)[:embeddingChunkRunes])}
	}
	if len(bodyRunes) == 0 {
		return []string{strings.TrimSpace(prefix)}
	}

	all := make([]string, 0, (len(bodyRunes)+bodySize-1)/bodySize)
	for start := 0; start < len(bodyRunes); start += bodySize {
		end := start + bodySize
		if end > len(bodyRunes) {
			end = len(bodyRunes)
		}
		all = append(all, prefix+string(bodyRunes[start:end]))
	}
	if len(all) <= maxEmbeddingChunks {
		return all
	}

	sampled := make([]string, 0, maxEmbeddingChunks)
	for i := 0; i < maxEmbeddingChunks; i++ {
		index := int(math.Round(float64(i) * float64(len(all)-1) / float64(maxEmbeddingChunks-1)))
		sampled = append(sampled, all[index])
	}
	return sampled
}

// meanEmbedding gives each chunk equal influence regardless of the magnitude
// returned by the embedding adapter, then normalizes the pooled document
// vector for stable cosine comparisons.
func meanEmbedding(vectors [][]float32) ([]float32, error) {
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("no embedding vectors to pool")
	}
	dimensions := len(vectors[0])
	mean := make([]float64, dimensions)
	used := 0
	for _, vector := range vectors {
		if len(vector) != dimensions {
			return nil, fmt.Errorf("embedding dimension mismatch: %d and %d", dimensions, len(vector))
		}
		var magnitude float64
		for _, value := range vector {
			magnitude += float64(value) * float64(value)
		}
		if magnitude == 0 {
			continue
		}
		magnitude = math.Sqrt(magnitude)
		for i, value := range vector {
			mean[i] += float64(value) / magnitude
		}
		used++
	}
	if used == 0 {
		return nil, fmt.Errorf("embedding vectors are all zero")
	}

	var magnitude float64
	for i := range mean {
		mean[i] /= float64(used)
		magnitude += mean[i] * mean[i]
	}
	if magnitude == 0 {
		return nil, fmt.Errorf("pooled embedding is zero")
	}
	magnitude = math.Sqrt(magnitude)
	pooled := make([]float32, dimensions)
	for i := range mean {
		pooled[i] = float32(mean[i] / magnitude)
	}
	return pooled, nil
}

// ---- indexing queue ----

// enqueueEmbed queues a slug for (re-)embedding. No-op when the feature is
// disabled; drops (logged) when the queue is full.
func enqueueEmbed(slug string) {
	if embedBaseURL == "" {
		return
	}
	embedJobs.enqueue(slug, slug)
}

// startEmbedder launches the background worker; no-op when disabled.
func startEmbedder() {
	if embedBaseURL == "" {
		log.Println("embedding semantic search disabled (MDHUB_EMBED_URL empty)")
		return
	}
	client := &http.Client{Timeout: 120 * time.Second}
	embedJobs.start(func(slug string) error { return doEmbed(slug, client) })
}

// waitEmbed blocks until every queued embedding has finished. Used by
// one-shot commands (-import) so results land before the process exits.
func waitEmbed() {
	embedJobs.wait()
}

// doEmbed embeds one published document and stores the vector in Postgres
// and the in-memory index. A slug that is no longer published gets its
// stale embedding row deleted instead.
func doEmbed(slug string, client *http.Client) error {
	var title, content string
	err := db.QueryRow(
		"SELECT title, content FROM documents WHERE slug=$1 AND published=true", slug).
		Scan(&title, &content)
	if err == sql.ErrNoRows {
		if _, deleteErr := db.Exec("DELETE FROM embeddings WHERE slug=$1", slug); deleteErr != nil {
			return fmt.Errorf("delete stale embedding: %w", deleteErr)
		}
		mu.Lock()
		delete(embedIndex, slug)
		mu.Unlock()
		markUniverseDirty()
		return nil
	}
	if err != nil {
		return err
	}

	chunkVectors := make([][]float32, 0, maxEmbeddingChunks)
	for _, chunk := range embeddingChunks(title, content) {
		vector, err := embedText(client, chunk)
		if err != nil {
			return err
		}
		chunkVectors = append(chunkVectors, vector)
	}
	vec, err := meanEmbedding(chunkVectors)
	if err != nil {
		return err
	}

	if _, err := db.Exec(`
		INSERT INTO embeddings (slug, embedding) VALUES ($1,$2)
		ON CONFLICT (slug) DO UPDATE SET embedding=EXCLUDED.embedding, updated_at=now()`,
		slug, encodeVec(vec)); err != nil {
		return err
	}
	mu.Lock()
	embedIndex[slug] = vec
	mu.Unlock()
	markUniverseDirty()
	return nil
}

// loadEmbeddingsFromDB rebuilds the in-memory vector index from all
// published documents that have a stored embedding.
func loadEmbeddingsFromDB() {
	rows, err := db.Query(`
		SELECT e.slug, e.embedding FROM embeddings e
		JOIN documents d ON d.slug=e.slug AND d.published=true`)
	if err != nil {
		log.Printf("load embeddings: %v", err)
		return
	}
	defer rows.Close()

	newIndex := map[string][]float32{}
	for rows.Next() {
		var slug string
		var blob []byte
		if err := rows.Scan(&slug, &blob); err != nil {
			log.Printf("scan embedding index: %v", err)
			return
		}
		if v := decodeVec(blob); v != nil {
			newIndex[slug] = v
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("read embedding index: %v", err)
		return
	}
	mu.Lock()
	embedIndex = newIndex
	mu.Unlock()
	markUniverseDirty()
	log.Printf("embedding index loaded, %d vectors", len(newIndex))
}

// ---- hybrid ranking ----

// simThreshold is the minimum cosine similarity for a purely semantic hit
// (no keyword match) to enter the results.
const simThreshold = 0.4

type mergedHit struct {
	slug  string
	score float64 // combined: normalized keyword score + clamped similarity
	kw    bool    // true when the keyword search matched (drives snippet style)
}

// mergeHits fuses keyword bigram scores with embedding cosine similarity.
// kwScores holds slug -> raw keyword score (only for keyword matches),
// vecs the document vectors, mtimes slug -> file mtime (ms) for tie-breaking.
// A nil/empty queryVec degrades to pure keyword ranking. Pure function.
func mergeHits(kwScores map[string]float64, queryVec []float32, vecs map[string][]float32, mtimes map[string]int64) []mergedHit {
	maxKw := 0.0
	for _, s := range kwScores {
		if s > maxKw {
			maxKw = s
		}
	}

	hits := []mergedHit{}
	seen := map[string]bool{}
	add := func(slug string) {
		if seen[slug] {
			return
		}
		seen[slug] = true
		kwNorm := 0.0
		if maxKw > 0 {
			kwNorm = kwScores[slug] / maxKw
		}
		sim := 0.0
		if len(queryVec) > 0 {
			if v, ok := vecs[slug]; ok {
				if s := cosine(queryVec, v); s > 0 {
					sim = s
				}
			}
		}
		if kwNorm > 0 || sim >= simThreshold {
			hits = append(hits, mergedHit{slug: slug, score: kwNorm + sim, kw: kwNorm > 0})
		}
	}
	for slug := range kwScores {
		add(slug)
	}
	for slug := range vecs {
		add(slug)
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return mtimes[hits[i].slug] > mtimes[hits[j].slug]
	})
	if len(hits) > 20 {
		hits = hits[:20]
	}
	return hits
}

// ---- reembed endpoint ----

// handleReembed queues every published document for re-embedding.
// POST only; responds with {"queued": N}. N counts slugs seen even when the
// feature is disabled (enqueue is then a no-op), matching /api/reclassify.
func handleReembed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), 405)
		return
	}
	if !requireEditAccess(w, r) {
		return
	}
	rows, err := db.Query("SELECT slug FROM documents WHERE published=true")
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer rows.Close()
	queued := 0
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			enqueueEmbed(slug)
			queued++
		}
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]int{"queued": queued})
}
