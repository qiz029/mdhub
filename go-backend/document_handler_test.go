package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func expectDraftDocument(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT slug, file_path, title, raw_content").
		WithArgs("draft").
		WillReturnRows(sqlmock.NewRows([]string{
			"slug", "file_path", "title", "raw_content", "excerpt", "word_count",
			"published", "kind", "source", "category_path", "file_mtime",
		}).AddRow("draft", "", "Draft", "secret draft", "secret", 12, false, "note", "user", "", time.Now()))
}

func TestGetDocumentReturnsDraft(t *testing.T) {
	mock := withMockDatabase(t)
	expectDraftDocument(mock)
	mock.ExpectQuery("SELECT tag FROM document_tags").
		WithArgs("draft").WillReturnRows(sqlmock.NewRows([]string{"tag"}))
	mock.ExpectQuery("SELECT source_slug FROM backlinks").
		WithArgs("draft").WillReturnRows(sqlmock.NewRows([]string{"source_slug"}))

	// unpublished notes are readable like everything else; publish only
	// curates feed/search/tree/Universe, it is not access control
	request := httptest.NewRequest(http.MethodGet, "/api/documents/draft", nil)
	response := httptest.NewRecorder()
	getDocument(response, request, "draft")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetDocumentReturnsFleetingDraft(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT slug, file_path, title, raw_content").
		WithArgs("_sparks/1").
		WillReturnRows(sqlmock.NewRows([]string{
			"slug", "file_path", "title", "raw_content", "excerpt", "word_count",
			"published", "kind", "source", "category_path", "file_mtime",
		}).AddRow("_sparks/1", "", "Spark", "idea", "idea", 4, false, "fleeting", "user", "", time.Now()))
	mock.ExpectQuery("SELECT tag FROM document_tags").
		WithArgs("_sparks/1").WillReturnRows(sqlmock.NewRows([]string{"tag"}))
	mock.ExpectQuery("SELECT source_slug FROM backlinks").
		WithArgs("_sparks/1").WillReturnRows(sqlmock.NewRows([]string{"source_slug"}))

	request := httptest.NewRequest(http.MethodGet, "/api/documents/_sparks/1", nil)
	response := httptest.NewRecorder()
	getDocument(response, request, "_sparks/1")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDocumentPublishesNestedSlugThroughPublicationModule(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	slug := "folder/note"
	mock.ExpectQuery("SELECT file_path FROM documents").
		WithArgs(slug).
		WillReturnRows(sqlmock.NewRows([]string{"file_path"}))
	expectDocumentWrite(mock, slug)
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/documents/"+slug,
		strings.NewReader("---\ntitle: Fresh\npublish: true\n---\nFresh body"),
	)
	response := httptest.NewRecorder()

	handleDocument(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	mu.RLock()
	entry := searchIndex[slug]
	mu.RUnlock()
	if entry == nil || entry.slug != slug || entry.plain != "fresh body" {
		t.Fatalf("published search projection = %#v", entry)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDocumentDeletesRuntimeProjections(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	slug := "folder/note"
	searchIndex[slug] = &searchEntry{title: "Old"}
	embedIndex[slug] = []float32{1}
	mock.ExpectExec("DELETE FROM documents").
		WithArgs(slug).
		WillReturnResult(sqlmock.NewResult(0, 1))
	request := httptest.NewRequest(http.MethodDelete, "/api/documents/"+slug, nil)
	response := httptest.NewRecorder()

	handleDocument(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	mu.RLock()
	_, searchable := searchIndex[slug]
	_, embedded := embedIndex[slug]
	mu.RUnlock()
	if searchable || embedded {
		t.Fatalf("deleted document remained projected: searchable=%v embedded=%v", searchable, embedded)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
