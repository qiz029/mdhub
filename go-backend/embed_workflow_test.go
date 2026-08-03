package main

import (
	"database/sql"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDoEmbedStoresAndPublishesFreshVector(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[3,4]}]}`))
	}))
	t.Cleanup(server.Close)
	embedBaseURL = server.URL
	revision := time.Date(2026, 8, 3, 7, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT title, content, file_mtime FROM documents").
		WithArgs("note").
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "file_mtime"}).AddRow("Title", "Body", revision))
	mock.ExpectExec("INSERT INTO embeddings").
		WithArgs("note", sqlmock.AnyArg(), revision).
		WillReturnResult(sqlmock.NewResult(0, 1))
	generation := universeVectorGeneration.Load()

	if err := doEmbed("note", server.Client()); err != nil {
		t.Fatal(err)
	}
	mu.RLock()
	vector := append([]float32(nil), embedIndex["note"]...)
	mu.RUnlock()
	if len(vector) != 2 || math.Abs(float64(vector[0]-0.6)) > 1e-6 || math.Abs(float64(vector[1]-0.8)) > 1e-6 {
		t.Fatalf("vector = %v, want normalized [0.6 0.8]", vector)
	}
	if got := universeVectorGeneration.Load(); got != generation+1 {
		t.Fatalf("generation = %d, want %d", got, generation+1)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoEmbedDeletesVectorForUnpublishedDocument(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	embedIndex["draft"] = []float32{1}
	mock.ExpectQuery("SELECT title, content, file_mtime FROM documents").
		WithArgs("draft").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM embeddings WHERE slug=$1")).
		WithArgs("draft").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := doEmbed("draft", nil); err != nil {
		t.Fatal(err)
	}
	mu.RLock()
	_, exists := embedIndex["draft"]
	mu.RUnlock()
	if exists {
		t.Fatal("stale draft embedding remained in memory")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoEmbedRetriesWhenDocumentRevisionChanges(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0,1]}]}`))
	}))
	t.Cleanup(server.Close)
	embedBaseURL = server.URL
	oldRevision := time.Date(2026, 8, 3, 7, 0, 0, 0, time.UTC)
	newRevision := oldRevision.Add(time.Second)
	mock.ExpectQuery("SELECT title, content, file_mtime FROM documents").WithArgs("note").
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "file_mtime"}).AddRow("Old", "body", oldRevision))
	mock.ExpectExec("INSERT INTO embeddings").WithArgs("note", sqlmock.AnyArg(), oldRevision).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT title, content, file_mtime FROM documents").WithArgs("note").
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "file_mtime"}).AddRow("New", "body", newRevision))
	mock.ExpectExec("INSERT INTO embeddings").WithArgs("note", sqlmock.AnyArg(), newRevision).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := doEmbed("note", server.Client()); err != nil {
		t.Fatal(err)
	}
	mu.RLock()
	vector := append([]float32(nil), embedIndex["note"]...)
	mu.RUnlock()
	if !reflect.DeepEqual(vector, []float32{0, 1}) {
		t.Fatalf("vector = %v, want latest revision [0 1]", vector)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadEmbeddingIndexFromDBBuildsValidatedSnapshot(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT e.slug, e.embedding FROM embeddings").
		WillReturnRows(sqlmock.NewRows([]string{"slug", "embedding"}).
			AddRow("valid", encodeVec([]float32{1, 2})).
			AddRow("invalid", []byte{1, 2, 3}))

	snapshot, err := readEmbeddingIndexFromDB()
	if err != nil {
		t.Fatal(err)
	}
	valid := snapshot["valid"]
	_, invalidExists := snapshot["invalid"]
	if len(valid) != 2 || invalidExists {
		t.Fatalf("embedding snapshot = valid:%v invalid:%v", valid, invalidExists)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleReembedRequiresPostAndQueuesPublishedDocuments(t *testing.T) {
	t.Run("method", func(t *testing.T) {
		response := httptest.NewRecorder()
		handleReembed(response, httptest.NewRequest(http.MethodGet, "/api/reembed", nil))
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d", response.Code)
		}
	})

	t.Run("published documents", func(t *testing.T) {
		mock := withMockDatabase(t)
		oldURL := embedBaseURL
		embedBaseURL = ""
		t.Cleanup(func() { embedBaseURL = oldURL })
		mock.ExpectQuery("SELECT slug FROM documents WHERE published=true").
			WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("a").AddRow("b"))
		request := httptest.NewRequest(http.MethodPost, "/api/reembed", strings.NewReader(""))
		response := httptest.NewRecorder()

		handleReembed(response, request)

		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"queued":2`) {
			t.Fatalf("status = %d body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
