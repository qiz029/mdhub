package main

import (
	"database/sql/driver"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCollisionPairKeepsCanonicalOrder(t *testing.T) {
	a, b := collisionPair("b-note", "a-note")
	if a != "a-note" || b != "b-note" {
		t.Fatalf("pair = (%q, %q), want (a-note, b-note)", a, b)
	}
	a, b = collisionPair("a-note", "b-note")
	if a != "a-note" || b != "b-note" {
		t.Fatalf("pair = (%q, %q), want (a-note, b-note)", a, b)
	}
}

func TestTopCollisions(t *testing.T) {
	self := []float32{1, 0}

	t.Run("threshold and self exclusion", func(t *testing.T) {
		vecs := map[string][]float32{
			"self":  {1, 0},
			"above": {0.9, 0.4359},
			"edge":  {0.551, 0.8345}, // just above the 0.55 threshold
			"below": {0.54, 0.8415},
			"ortho": {0, 1},
		}
		hits := topCollisions("self", self, vecs)
		got := []string{}
		for _, h := range hits {
			got = append(got, h.slug)
		}
		if strings.Join(got, ",") != "above,edge" {
			t.Fatalf("hits = %v, want [above edge]", got)
		}
	})

	t.Run("capped at top N, sorted by score", func(t *testing.T) {
		vecs := map[string][]float32{}
		for i, slug := range []string{"f", "e", "d", "c", "b", "a", "g"} {
			c := 0.99 - float64(i)*0.01 // f highest, g lowest
			vecs[slug] = []float32{float32(c), float32(math.Sqrt(1 - c*c))}
		}
		hits := topCollisions("self", self, vecs)
		if len(hits) != collisionTopN {
			t.Fatalf("len(hits) = %d, want %d", len(hits), collisionTopN)
		}
		for i := 1; i < len(hits); i++ {
			if hits[i-1].score < hits[i].score {
				t.Fatalf("hits not sorted by score: %v", hits)
			}
		}
		if hits[0].slug != "f" {
			t.Fatalf("hits[0] = %s, want f (highest score)", hits[0].slug)
		}
	})

	t.Run("empty self vector yields nothing", func(t *testing.T) {
		if hits := topCollisions("self", nil, map[string][]float32{"a": {1, 0}}); len(hits) != 0 {
			t.Fatalf("hits = %v, want empty", hits)
		}
	})
}

func TestParseCollisionInsight(t *testing.T) {
	t.Run("plain JSON", func(t *testing.T) {
		conn, q, err := parseCollisionInsight(`{"connection":"都涉及记忆","question":"如何互相强化？"}`)
		if err != nil {
			t.Fatal(err)
		}
		if conn != "都涉及记忆" || q != "如何互相强化？" {
			t.Fatalf("got (%q, %q)", conn, q)
		}
	})

	t.Run("code fence and chatter", func(t *testing.T) {
		conn, q, err := parseCollisionInsight("好的：\n```json\n{\"connection\":\"c\",\"question\":\"q\"}\n```\n")
		if err != nil || conn != "c" || q != "q" {
			t.Fatalf("got (%q, %q, %v)", conn, q, err)
		}
	})

	t.Run("no JSON", func(t *testing.T) {
		if _, _, err := parseCollisionInsight("这不是 JSON"); err == nil {
			t.Fatal("expected error for non-JSON answer")
		}
	})
}

func TestDoCollideStoresScorePairWithoutLLM(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t) // llmAPIKey=""
	embedIndex["self"] = []float32{1, 0}
	embedIndex["peer"] = []float32{0.6, 0.8}
	embedIndex["ortho"] = []float32{0, 1}

	// canonical order: ("peer", "self"); no LLM -> empty explanation/question
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM collisions WHERE slug_a=$1 AND slug_b=$2)")).
		WithArgs("peer", "self").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("INSERT INTO collisions").
		WithArgs("peer", "self", sqlmock.AnyArg(), "", "").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := doCollide("self", nil); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoCollideSkipsExistingPair(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	embedIndex["self"] = []float32{1, 0}
	embedIndex["peer"] = []float32{0.6, 0.8}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM collisions WHERE slug_a=$1 AND slug_b=$2)")).
		WithArgs("peer", "self").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	// no INSERT expected

	if err := doCollide("self", nil); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoCollideStoresLLMInsight(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	srv := fakeLLM(t, 200, "```json\n{\"connection\":\"深层联系\",\"question\":\"开放问题？\"}\n```")
	embedIndex["self"] = []float32{1, 0}
	embedIndex["peer"] = []float32{0.6, 0.8}

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("peer", "self").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT title, excerpt FROM documents WHERE slug=$1")).
		WithArgs("peer").
		WillReturnRows(sqlmock.NewRows([]string{"title", "excerpt"}).AddRow("Peer", "peer excerpt"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT title, excerpt FROM documents WHERE slug=$1")).
		WithArgs("self").
		WillReturnRows(sqlmock.NewRows([]string{"title", "excerpt"}).AddRow("Self", "self excerpt"))
	mock.ExpectExec("INSERT INTO collisions").
		WithArgs("peer", "self", sqlmock.AnyArg(), "深层联系", "开放问题？").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := doCollide("self", srv.Client()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoCollideMissingVectorIsNoOp(t *testing.T) {
	withMockDatabase(t)
	isolatePublicationState(t)
	if err := doCollide("absent", nil); err != nil {
		t.Fatal(err)
	}
}

// drainEmbedJob pops one queued embed job and releases its queue accounting,
// so tests leave no residue in the global queue.
func drainEmbedJob(t *testing.T) string {
	t.Helper()
	select {
	case job := <-embedJobs.ch:
		embedJobs.mu.Lock()
		delete(embedJobs.seen, job.key)
		embedJobs.mu.Unlock()
		embedJobs.wg.Done()
		return job.value
	default:
		t.Fatal("no embed job queued")
		return ""
	}
}

// drainCollideJob is drainEmbedJob for the collision queue.
func drainCollideJob(t *testing.T) string {
	t.Helper()
	select {
	case job := <-collideJobs.ch:
		collideJobs.mu.Lock()
		delete(collideJobs.seen, job.key)
		collideJobs.mu.Unlock()
		collideJobs.wg.Done()
		return job.value
	default:
		t.Fatal("no collide job queued")
		return ""
	}
}

func TestDoEmbedChainsIntoCollisionQueue(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[3,4]}]}`))
	}))
	t.Cleanup(server.Close)
	embedBaseURL = server.URL
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT title, content FROM documents WHERE slug=$1 AND (published=true OR kind='fleeting')")).
		WithArgs("_sparks/1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "content"}).AddRow("Spark", "idea"))
	mock.ExpectExec("INSERT INTO embeddings").
		WithArgs("_sparks/1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := doEmbed("_sparks/1", server.Client()); err != nil {
		t.Fatal(err)
	}
	if got := drainCollideJob(t); got != "_sparks/1" {
		t.Fatalf("collide job = %q, want _sparks/1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleSparksServesAnonymousGet(t *testing.T) {
	mock := withMockDatabase(t)
	isolateEditAccess(t)
	mock.ExpectQuery("FROM documents").
		WillReturnRows(sqlmock.NewRows([]string{"slug", "title", "excerpt", "file_mtime", "collisions"}).
			AddRow("_sparks/1", "Spark", "idea", time.Unix(100, 0), 3))

	// no edit token: sparks are public
	response := httptest.NewRecorder()
	handleSparks(response, httptest.NewRequest(http.MethodGet, "/api/sparks", nil))

	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"slug":"_sparks/1"`) ||
		!strings.Contains(response.Body.String(), `"collisions":3`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleCollisionsPublicRead(t *testing.T) {
	columns := []string{
		"id", "slug_a", "slug_b", "title_a", "title_b",
		"score", "explanation", "question", "verdict", "created_at",
	}
	row := []driver.Value{
		int64(7), "a", "b", "A", "B", 0.9, "conn", "q", "new", time.Unix(100, 0),
	}

	t.Run("anonymous sees every collision", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectQuery("FROM collisions").
			WithArgs("").
			WillReturnRows(sqlmock.NewRows(columns).AddRow(row...))

		// no edit token: the full collision list is public
		response := httptest.NewRecorder()
		handleCollisions(response, httptest.NewRequest(http.MethodGet, "/api/collisions", nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":7`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("slug filter", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectQuery("FROM collisions").
			WithArgs("a").
			WillReturnRows(sqlmock.NewRows(columns).AddRow(row...))

		response := httptest.NewRecorder()
		handleCollisions(response, httptest.NewRequest(http.MethodGet, "/api/collisions?slug=a", nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"slug_a":"a"`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestHandleCollisionVerdict(t *testing.T) {
	t.Run("invalid verdict is rejected", func(t *testing.T) {
		withMockDatabase(t)
		isolateEditAccess(t)
		request := httptest.NewRequest(http.MethodPost, "/api/collisions/7",
			strings.NewReader(`{"verdict":"bogus"}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleCollision(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("valid verdict updates", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE collisions SET verdict=$1 WHERE id=$2")).
			WithArgs("confirmed", "7").
			WillReturnResult(sqlmock.NewResult(0, 1))
		request := httptest.NewRequest(http.MethodPost, "/api/collisions/7",
			strings.NewReader(`{"verdict":"confirmed"}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleCollision(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectExec("UPDATE collisions").
			WithArgs("dismissed", "99").
			WillReturnResult(sqlmock.NewResult(0, 0))
		request := httptest.NewRequest(http.MethodPost, "/api/collisions/99",
			strings.NewReader(`{"verdict":"dismissed"}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleCollision(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("edit token required", func(t *testing.T) {
		withMockDatabase(t)
		isolateEditAccess(t)
		request := httptest.NewRequest(http.MethodPost, "/api/collisions/7",
			strings.NewReader(`{"verdict":"confirmed"}`))
		response := httptest.NewRecorder()
		handleCollision(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.Code)
		}
	})
}

func TestHandleRecollideQueuesEveryEmbeddedSlug(t *testing.T) {
	withMockDatabase(t)
	isolatePublicationState(t)
	isolateEditAccess(t)
	embedIndex["a"] = []float32{1}
	embedIndex["b"] = []float32{0, 1}

	request := httptest.NewRequest(http.MethodPost, "/api/recollide", strings.NewReader(""))
	request.Header.Set("X-MDHub-Edit-Token", "secret")
	response := httptest.NewRecorder()
	handleRecollide(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"queued":2`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handleRecollide(response, httptest.NewRequest(http.MethodGet, "/api/recollide", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", response.Code)
	}
}
