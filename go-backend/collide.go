package main

// Collision engine: after a document is (re-)embedded, its vector is compared
// against every other vector in embedIndex; pairs above collisionSimThreshold
// are recorded in the collisions table. When an LLM API key is configured,
// each new pair gets a short "non-obvious connection" plus one open question
// written by the model; without a key the bare score pair is still stored.
// Humans then curate pairs via POST /api/collisions/{id} (confirmed/dismissed).
//
// Sparks (kind='fleeting') participate in collisions and are public like
// everything else: this is a personal space, and any multi-tenancy gate
// lives at the ingress/proxy layer, not inside the app.
//
// Runs on a background queue (same keyedJobQueue pattern as classify.go),
// chained from doEmbed so collisions always run against the fresh vector.
// Disabled entirely when MDHUB_EMBED_URL is empty — collisions depend on
// vectors.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	// collisionSimThreshold is higher than the search simThreshold (0.4):
	// a collision claims a strong connection, not a loose keyword cousin.
	collisionSimThreshold = 0.55
	collisionTopN         = 5
)

var collideJobs = newKeyedJobQueue[string]("collide", 500)

// enqueueCollide queues a slug for collision detection. No-op when the
// feature is disabled; drops (logged) when the queue is full.
func enqueueCollide(slug string) {
	if embedBaseURL == "" {
		return
	}
	collideJobs.enqueue(slug, slug)
}

// startCollide launches the background worker; no-op when disabled.
func startCollide() {
	if embedBaseURL == "" {
		log.Println("collision engine disabled (MDHUB_EMBED_URL empty)")
		return
	}
	client := &http.Client{Timeout: 60 * time.Second}
	collideJobs.start(func(slug string) error { return doCollide(slug, client) })
}

// waitCollide blocks until every queued collision job has finished. Used by
// one-shot commands (-import) so results land before the process exits.
func waitCollide() {
	collideJobs.wait()
}

type collisionHit struct {
	slug  string
	score float64
}

// collisionPair returns the pair in canonical order (slug_a < slug_b), the
// invariant behind the collisions table's UNIQUE (slug_a, slug_b).
func collisionPair(x, y string) (string, string) {
	if x < y {
		return x, y
	}
	return y, x
}

// topCollisions ranks every other vector against selfVec by cosine
// similarity, keeping the best collisionTopN above collisionSimThreshold.
// Pure function.
func topCollisions(self string, selfVec []float32, vecs map[string][]float32) []collisionHit {
	if len(selfVec) == 0 {
		return nil
	}
	hits := []collisionHit{}
	for slug, vec := range vecs {
		if slug == self {
			continue
		}
		if score := cosine(selfVec, vec); score >= collisionSimThreshold {
			hits = append(hits, collisionHit{slug: slug, score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].slug < hits[j].slug
	})
	if len(hits) > collisionTopN {
		hits = hits[:collisionTopN]
	}
	return hits
}

// doCollide compares one freshly embedded document against the whole
// in-memory vector index and stores each new high-similarity pair.
func doCollide(slug string, client *http.Client) error {
	mu.RLock()
	selfVec, ok := embedIndex[slug]
	var hits []collisionHit
	if ok {
		hits = topCollisions(slug, selfVec, embedIndex)
	}
	mu.RUnlock()
	if !ok || len(hits) == 0 {
		return nil
	}

	for _, hit := range hits {
		slugA, slugB := collisionPair(slug, hit.slug)
		var exists bool
		if err := db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM collisions WHERE slug_a=$1 AND slug_b=$2)",
			slugA, slugB).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}

		explanation, question := "", ""
		if llmAPIKey != "" {
			var err error
			explanation, question, err = llmCollisionInsight(client, slugA, slugB)
			if err != nil {
				// the score pair is the core datum; an LLM failure must not
				// drop it, so log and store with empty text
				log.Printf("collide %s/%s insight: %v", slugA, slugB, err)
				explanation, question = "", ""
			}
		}

		if _, err := db.Exec(`
			INSERT INTO collisions (slug_a, slug_b, score, explanation, question)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (slug_a, slug_b) DO NOTHING`,
			slugA, slugB, hit.score, explanation, question); err != nil {
			return err
		}
		log.Printf("collision %s <-> %s score %.3f", slugA, slugB, hit.score)
	}
	return nil
}

// ---- LLM insight ----

// llmCollisionInsight asks the LLM for a non-obvious connection between two
// notes plus one open question. Returns ("", "", nil) when either document
// is gone.
func llmCollisionInsight(client *http.Client, slugA, slugB string) (string, string, error) {
	titleA, excerptA, err := collisionSide(slugA)
	if err != nil {
		return "", "", err
	}
	titleB, excerptB, err := collisionSide(slugB)
	if err != nil {
		return "", "", err
	}

	user := fmt.Sprintf("笔记 A 标题：%s\n笔记 A 摘录：\n%s\n\n笔记 B 标题：%s\n笔记 B 摘录：\n%s\n",
		titleA, excerptA, titleB, excerptB)
	answer, err := llmChat(client,
		"你的任务是找出两篇笔记之间非显而易见的联系。只输出 JSON，格式为 {\"connection\":\"…\",\"question\":\"…\"}，不要输出任何其他文字。要求：connection 用 2-3 句话指出两篇笔记深层的、表面看不出来的联系；question 是一个能把两者联系起来的开放问题，只提问不给答案。",
		user)
	if err != nil {
		return "", "", err
	}
	return parseCollisionInsight(answer)
}

// collisionSide fetches a document's title and a truncated excerpt for the
// collision prompt.
func collisionSide(slug string) (title, excerpt string, err error) {
	err = db.QueryRow("SELECT title, excerpt FROM documents WHERE slug=$1", slug).
		Scan(&title, &excerpt)
	if err != nil {
		return "", "", err
	}
	if r := []rune(excerpt); len(r) > 200 {
		excerpt = string(r[:200])
	}
	return title, excerpt, nil
}

// parseCollisionInsight extracts the {"connection","question"} payload from
// the LLM's answer, tolerating code fences and surrounding chatter (same
// approach as parseSplitGroups).
func parseCollisionInsight(answer string) (string, string, error) {
	a := strings.TrimSpace(answer)
	a = strings.TrimPrefix(a, "```json")
	a = strings.TrimPrefix(a, "```")
	a = strings.TrimSuffix(a, "```")
	a = strings.TrimSpace(a)
	start := strings.Index(a, "{")
	end := strings.LastIndex(a, "}")
	if start < 0 || end <= start {
		return "", "", fmt.Errorf("no JSON object in answer: %q", answer)
	}
	var out struct {
		Connection string `json:"connection"`
		Question   string `json:"question"`
	}
	if err := json.Unmarshal([]byte(a[start:end+1]), &out); err != nil {
		return "", "", fmt.Errorf("bad collision JSON: %w", err)
	}
	return strings.TrimSpace(out.Connection), strings.TrimSpace(out.Question), nil
}

// ---- HTTP API ----

type sparkItem struct {
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Excerpt    string `json:"excerpt"`
	Updated    int64  `json:"updated"` // file_mtime, Unix ms
	Collisions int    `json:"collisions"`
}

// handleSparks serves GET /api/sparks — fleeting notes, newest first, with
// their collision counts.
func handleSparks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	rows, err := db.Query(`
		SELECT d.slug, d.title, d.excerpt, d.file_mtime,
			(SELECT COUNT(*) FROM collisions c WHERE c.slug_a=d.slug OR c.slug_b=d.slug)
		FROM documents d
		WHERE d.kind='fleeting'
		ORDER BY d.file_mtime DESC`)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []sparkItem{}
	for rows.Next() {
		var it sparkItem
		var mtime time.Time
		if err := rows.Scan(&it.Slug, &it.Title, &it.Excerpt, &mtime, &it.Collisions); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		it.Updated = mtime.UnixMilli()
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

type collisionItem struct {
	ID          int64   `json:"id"`
	SlugA       string  `json:"slug_a"`
	SlugB       string  `json:"slug_b"`
	TitleA      string  `json:"title_a"`
	TitleB      string  `json:"title_b"`
	Score       float64 `json:"score"`
	Explanation string  `json:"explanation"`
	Question    string  `json:"question"`
	Verdict     string  `json:"verdict"`
	CreatedAt   int64   `json:"created_at"` // Unix ms
}

// handleCollisions serves GET /api/collisions — newest 50 pairs, optionally
// filtered to one slug with ?slug=x.
func handleCollisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	rows, err := db.Query(`
		SELECT c.id, c.slug_a, c.slug_b, a.title, b.title,
			c.score, c.explanation, c.question, c.verdict, c.created_at
		FROM collisions c
		JOIN documents a ON a.slug=c.slug_a
		JOIN documents b ON b.slug=c.slug_b
		WHERE ($1='' OR c.slug_a=$1 OR c.slug_b=$1)
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT 50`, slug)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []collisionItem{}
	for rows.Next() {
		var it collisionItem
		var at time.Time
		if err := rows.Scan(&it.ID, &it.SlugA, &it.SlugB, &it.TitleA, &it.TitleB,
			&it.Score, &it.Explanation, &it.Question, &it.Verdict, &at); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		it.CreatedAt = at.UnixMilli()
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// handleCollision dispatches /api/collisions/{id} requests.
func handleCollision(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/collisions/"), "/")
	if id == "" {
		handleCollisions(w, r)
		return
	}
	if r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	if !requireEditAccess(w, r) {
		return
	}

	var body struct {
		Verdict string `json:"verdict"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCommentBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("invalid body"), http.StatusBadRequest)
		return
	}
	switch body.Verdict {
	case "new", "confirmed", "dismissed":
	default:
		httpError(w, fmt.Errorf("verdict must be one of: new, confirmed, dismissed"), http.StatusBadRequest)
		return
	}

	result, err := db.Exec("UPDATE collisions SET verdict=$1 WHERE id=$2", body.Verdict, id)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if updated, _ := result.RowsAffected(); updated == 0 {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleRecollide queues every embedded slug for collision detection
// (backfill). POST only; responds with {"queued": N}. N counts slugs seen
// even when the feature is disabled (enqueue is then a no-op), matching
// /api/reembed.
func handleRecollide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	if !requireEditAccess(w, r) {
		return
	}
	mu.RLock()
	slugs := make([]string, 0, len(embedIndex))
	for slug := range embedIndex {
		slugs = append(slugs, slug)
	}
	mu.RUnlock()
	for _, slug := range slugs {
		enqueueCollide(slug)
	}
	writeJSON(w, map[string]int{"queued": len(slugs)})
}
