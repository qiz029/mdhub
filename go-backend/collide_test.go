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

	t.Run("same-feed pairs suppressed, cross-domain kept", func(t *testing.T) {
		selfSlug := "_sparks/rss/aaaa11111111/entry1"
		vecs := map[string][]float32{
			selfSlug: {1, 0},
			// same feed, highest score: must be suppressed entirely
			"_sparks/rss/aaaa11111111/entry2": {0.99, 0.1411},
			// different feed: kept
			"_sparks/rss/bbbb22222222/entry1": {0.8, 0.6},
			// handwritten spark and normal note: kept
			"_sparks/inbox/idea": {0.7, 0.7142},
			"notes/article":      {0.6, 0.8},
		}
		hits := topCollisions(selfSlug, self, vecs)
		for _, h := range hits {
			if h.slug == "_sparks/rss/aaaa11111111/entry2" {
				t.Fatalf("same-feed pair survived: %v", hits)
			}
		}
		if len(hits) != 3 {
			t.Fatalf("hits = %v, want 3 cross-domain pairs", hits)
		}
		if hits[0].slug != "_sparks/rss/bbbb22222222/entry1" {
			t.Fatalf("hits[0] = %s, want the cross-feed pair first", hits[0].slug)
		}
	})

	t.Run("suppressed pairs do not consume top N slots", func(t *testing.T) {
		selfSlug := "_sparks/rss/aaaa11111111/entry1"
		vecs := map[string][]float32{selfSlug: {1, 0}}
		// 5 same-feed entries outranking 5 cross-domain notes; without
		// suppression the notes would be pushed out of the top 5
		for i, slug := range []string{"e2", "e3", "e4", "e5", "e6"} {
			vecs["_sparks/rss/aaaa11111111/"+slug] = []float32{0.99, 0.1411}
			c := 0.9 - float64(i)*0.01
			vecs["notes/"+slug] = []float32{float32(c), float32(math.Sqrt(1 - c*c))}
		}
		hits := topCollisions(selfSlug, self, vecs)
		if len(hits) != collisionTopN {
			t.Fatalf("len(hits) = %d, want %d (slots freed by suppression)", len(hits), collisionTopN)
		}
		for _, h := range hits {
			if !strings.HasPrefix(h.slug, "notes/") {
				t.Fatalf("same-feed pair took a slot: %v", hits)
			}
		}
	})
}

func TestSameFeedSpark(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"same feed", "_sparks/rss/aaaa11111111/x", "_sparks/rss/aaaa11111111/y", true},
		{"same slug", "_sparks/rss/aaaa11111111/x", "_sparks/rss/aaaa11111111/x", true},
		{"different feeds", "_sparks/rss/aaaa11111111/x", "_sparks/rss/bbbb22222222/x", false},
		{"one not rss", "_sparks/rss/aaaa11111111/x", "_sparks/inbox/x", false},
		{"neither rss", "notes/a", "notes/b", false},
		{"missing entry segment", "_sparks/rss/aaaa11111111", "_sparks/rss/aaaa11111111/x", false},
		{"both missing entry segment", "_sparks/rss/aaaa11111111", "_sparks/rss/aaaa11111111", false},
		{"empty feed hash", "_sparks/rss//x", "_sparks/rss//y", false},
		{"prefix of a longer slug", "_sparks/rss2/aaaa/x", "_sparks/rss2/aaaa/y", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameFeedSpark(c.a, c.b); got != c.want {
				t.Fatalf("sameFeedSpark(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
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
	mock.ExpectQuery("FROM documents").
		WillReturnRows(sqlmock.NewRows([]string{"slug", "title", "excerpt", "file_mtime", "source", "collisions"}).
			AddRow("_sparks/1", "Spark", "idea", time.Unix(100, 0), "rss/Test Feed", 3))

	// no edit token: sparks are public
	response := httptest.NewRecorder()
	handleSparks(response, httptest.NewRequest(http.MethodGet, "/api/sparks", nil))

	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"slug":"_sparks/1"`) ||
		!strings.Contains(response.Body.String(), `"source":"rss/Test Feed"`) ||
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
		"answered_by", "answered_at",
	}
	row := []driver.Value{
		int64(7), "a", "b", "A", "B", 0.9, "conn", "q", "new", time.Unix(100, 0),
		"notes/answer", time.Unix(200, 0),
	}

	t.Run("anonymous sees every collision", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery("FROM collisions").
			WithArgs("").
			WillReturnRows(sqlmock.NewRows(columns).AddRow(row...))

		// no edit token: the full collision list is public
		response := httptest.NewRecorder()
		handleCollisions(response, httptest.NewRequest(http.MethodGet, "/api/collisions", nil))
		if response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), `"id":7`) ||
			!strings.Contains(response.Body.String(), `"answered_by":"notes/answer"`) ||
			!strings.Contains(response.Body.String(), `"answered_at":200000`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("slug filter", func(t *testing.T) {
		mock := withMockDatabase(t)
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
		request := httptest.NewRequest(http.MethodPost, "/api/collisions/7",
			strings.NewReader(`{"verdict":"bogus"}`))
		response := httptest.NewRecorder()
		handleCollision(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("valid verdict updates", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE collisions SET verdict=$1 WHERE id=$2")).
			WithArgs("confirmed", "7").
			WillReturnResult(sqlmock.NewResult(0, 1))
		request := httptest.NewRequest(http.MethodPost, "/api/collisions/7",
			strings.NewReader(`{"verdict":"confirmed"}`))
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
		mock.ExpectExec("UPDATE collisions").
			WithArgs("dismissed", "99").
			WillReturnResult(sqlmock.NewResult(0, 0))
		request := httptest.NewRequest(http.MethodPost, "/api/collisions/99",
			strings.NewReader(`{"verdict":"dismissed"}`))
		response := httptest.NewRecorder()
		handleCollision(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestHandleRecollideQueuesEveryEmbeddedSlug(t *testing.T) {
	withMockDatabase(t)
	isolatePublicationState(t)
	embedIndex["a"] = []float32{1}
	embedIndex["b"] = []float32{0, 1}

	request := httptest.NewRequest(http.MethodPost, "/api/recollide", strings.NewReader(""))
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

func answerRequest(t *testing.T, path, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	return httptest.NewRecorder(), request
}

func TestHandleCollisionAnswer(t *testing.T) {
	t.Run("claims the bounty", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM documents WHERE slug=$1)")).
			WithArgs("notes/answer").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec(regexp.QuoteMeta("UPDATE collisions SET answered_by=$1, answered_at=now() WHERE id=$2")).
			WithArgs("notes/answer", "7").
			WillReturnResult(sqlmock.NewResult(0, 1))
		response, request := answerRequest(t, "/api/collisions/7/answer", `{"slug":"notes/answer"}`)

		handleCollision(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("re-answering overwrites the previous claim", func(t *testing.T) {
		mock := withMockDatabase(t)
		for _, slug := range []string{"notes/first", "notes/second"} {
			mock.ExpectQuery("SELECT EXISTS").
				WithArgs(slug).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			mock.ExpectExec("UPDATE collisions SET answered_by").
				WithArgs(slug, "7").
				WillReturnResult(sqlmock.NewResult(0, 1))
		}
		for _, slug := range []string{"notes/first", "notes/second"} {
			response, request := answerRequest(t, "/api/collisions/7/answer", `{"slug":"`+slug+`"}`)
			handleCollision(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("slug %s: status = %d, body = %q", slug, response.Code, response.Body.String())
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown slug is 400", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("notes/ghost").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		response, request := answerRequest(t, "/api/collisions/7/answer", `{"slug":"notes/ghost"}`)

		handleCollision(response, request)

		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("empty slug is 400", func(t *testing.T) {
		withMockDatabase(t)
		response, request := answerRequest(t, "/api/collisions/7/answer", `{"slug":"  "}`)
		handleCollision(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("invalid body is 400", func(t *testing.T) {
		withMockDatabase(t)
		response, request := answerRequest(t, "/api/collisions/7/answer", `not json`)
		handleCollision(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("unknown collision id is 404", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("notes/answer").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec("UPDATE collisions SET answered_by").
			WithArgs("notes/answer", "99").
			WillReturnResult(sqlmock.NewResult(0, 0))
		response, request := answerRequest(t, "/api/collisions/99/answer", `{"slug":"notes/answer"}`)

		handleCollision(response, request)

		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("GET is not allowed", func(t *testing.T) {
		withMockDatabase(t)
		request := httptest.NewRequest(http.MethodGet, "/api/collisions/7/answer", nil)
		response := httptest.NewRecorder()
		handleCollision(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", response.Code)
		}
	})
}

func TestHandleBlindbox(t *testing.T) {
	columns := []string{
		"id", "slug_a", "slug_b", "title_a", "title_b", "excerpt_a", "excerpt_b",
		"score", "explanation", "question", "verdict", "created_at",
		"answered_by", "answered_at",
	}

	t.Run("maps the drawn collision", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery("md5").
			WillReturnRows(sqlmock.NewRows(columns).AddRow(
				int64(3), "a", "b", "Title A", "Title B", "excerpt a", "excerpt b",
				0.9, "conn", "q", "new", time.Unix(100, 0),
				"", nil,
			))

		response := httptest.NewRecorder()
		handleBlindbox(response, httptest.NewRequest(http.MethodGet, "/api/blindbox", nil))

		body := response.Body.String()
		for _, want := range []string{
			`"id":3`, `"slug_a":"a"`, `"title_b":"Title B"`,
			`"excerpt_a":"excerpt a"`, `"excerpt_b":"excerpt b"`,
			`"explanation":"conn"`, `"answered_by":""`, `"answered_at":0`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("response missing %s: %d %q", want, response.Code, body)
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("null when nothing to draw", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery("md5").
			WillReturnRows(sqlmock.NewRows(columns))

		response := httptest.NewRecorder()
		handleBlindbox(response, httptest.NewRequest(http.MethodGet, "/api/blindbox", nil))

		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "null" {
			t.Fatalf("response = %d %q, want null", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("POST is not allowed", func(t *testing.T) {
		response := httptest.NewRecorder()
		handleBlindbox(response, httptest.NewRequest(http.MethodPost, "/api/blindbox", nil))
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", response.Code)
		}
	})
}
