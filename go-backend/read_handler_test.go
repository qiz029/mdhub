package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestHandleSearchReturnsKeywordHitsAndEmptyQuery(t *testing.T) {
	isolatePublicationState(t)
	searchIndex["a"] = &searchEntry{
		slug: "a", title: "Go testing", plain: "a practical go testing guide",
		display: "A practical Go testing guide", mtime: 200,
	}
	searchIndex["b"] = &searchEntry{
		slug: "b", title: "Cooking", plain: "soup", display: "Soup", mtime: 100,
	}

	response := httptest.NewRecorder()
	handleSearch(response, httptest.NewRequest(http.MethodGet, "/api/search?q=Go+testing", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"slug":"a"`) {
		t.Fatalf("search response = %d %q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "&lt;") && strings.Contains(response.Body.String(), "<script") {
		t.Fatalf("unsafe snippet = %q", response.Body.String())
	}

	response = httptest.NewRecorder()
	handleSearch(response, httptest.NewRequest(http.MethodGet, "/api/search?q=", nil))
	if strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("empty search = %q", response.Body.String())
	}
}

func TestHandleTagsListsCountsAndDocuments(t *testing.T) {
	t.Run("counts", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery("SELECT t.name, COUNT").
			WillReturnRows(sqlmock.NewRows([]string{"name", "count"}).AddRow("go", 2))
		response := httptest.NewRecorder()
		handleTags(response, httptest.NewRequest(http.MethodGet, "/api/tags", nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"count":2`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("documents", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery("SELECT d.slug, d.title FROM documents").
			WithArgs("go").
			WillReturnRows(sqlmock.NewRows([]string{"slug", "title"}).AddRow("note", "Note"))
		response := httptest.NewRecorder()
		handleTags(response, httptest.NewRequest(http.MethodGet, "/api/tags?tag=go", nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"slug":"note"`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestHandleBacklinksListsPublishedSources(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT d.slug, d.title FROM documents").
		WithArgs("target").
		WillReturnRows(sqlmock.NewRows([]string{"slug", "title"}).AddRow("source", "Source"))
	response := httptest.NewRecorder()
	handleBacklinks(response, httptest.NewRequest(http.MethodGet, "/api/backlinks/target", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"slug":"source"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDocumentListReturnsPublishedSummaries(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT d.slug, d.title, d.excerpt").
		WillReturnRows(sqlmock.NewRows([]string{
			"slug", "title", "excerpt", "file_mtime", "category_path", "kind", "tags",
		}).AddRow("note", "Note", "Excerpt", time.Unix(100, 0), "技术", "note", "{go}"))
	response := httptest.NewRecorder()
	handleDocumentList(response, httptest.NewRequest(http.MethodGet, "/api/documents", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"category":"技术"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	response = httptest.NewRecorder()
	handleDocumentList(response, httptest.NewRequest(http.MethodPost, "/api/documents", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", response.Code)
	}
}

func TestHandleCommentsCreatesReplyForPublishedDocument(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT published FROM documents").
		WithArgs("note").
		WillReturnRows(sqlmock.NewRows([]string{"published"}).AddRow(true))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("thread", "note").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("INSERT INTO comment_entries").
		WithArgs("thread", "Agent", "reply").
		WillReturnResult(sqlmock.NewResult(0, 1))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/documents/note/comments",
		strings.NewReader(`{"author":"Agent","text":"reply","reply":"thread"}`),
	)
	response := httptest.NewRecorder()

	handleComments(response, request, "note")

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"thread"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleReindexReloadsSearchAndEmbeddingProjections(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	isolateEditAccess(t)
	mock.ExpectQuery("SELECT slug, title, content, file_mtime FROM documents").
		WillReturnRows(sqlmock.NewRows([]string{"slug", "title", "content", "file_mtime"}).
			AddRow("note", "Note", "Body", time.Unix(100, 0)))
	mock.ExpectQuery("SELECT e.slug, e.embedding FROM embeddings").
		WillReturnRows(sqlmock.NewRows([]string{"slug", "embedding"}).
			AddRow("note", encodeVec([]float32{1, 0})))
	request := httptest.NewRequest(http.MethodPost, "/api/reindex", nil)
	request.Header.Set("X-MDHub-Edit-Token", "secret")
	response := httptest.NewRecorder()

	handleReindex(response, request)

	if response.Code != http.StatusOK || searchIndex["note"] == nil || len(embedIndex["note"]) != 2 {
		t.Fatalf("response=%d search=%#v embed=%v", response.Code, searchIndex, embedIndex)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleReclassifyQueuesEveryReturnedSlug(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	isolateEditAccess(t)
	mock.ExpectQuery("UPDATE documents SET category_path").
		WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("a").AddRow("b"))
	request := httptest.NewRequest(http.MethodPost, "/api/reclassify", nil)
	request.Header.Set("X-MDHub-Edit-Token", "secret")
	response := httptest.NewRecorder()

	handleReclassify(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"queued":2`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
