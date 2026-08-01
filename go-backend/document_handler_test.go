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

func TestGetDocumentHidesDraftWithoutEditAccess(t *testing.T) {
	mock := withMockDatabase(t)
	previousToken, previousAddress := editToken, listenAddr
	editToken, listenAddr = "secret", "127.0.0.1:10002"
	t.Cleanup(func() { editToken, listenAddr = previousToken, previousAddress })
	expectDraftDocument(mock)

	request := httptest.NewRequest(http.MethodGet, "/api/documents/draft", nil)
	response := httptest.NewRecorder()
	getDocument(response, request, "draft")

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetDocumentReturnsDraftWithEditAccess(t *testing.T) {
	mock := withMockDatabase(t)
	previousToken, previousAddress := editToken, listenAddr
	editToken, listenAddr = "secret", "127.0.0.1:10002"
	t.Cleanup(func() { editToken, listenAddr = previousToken, previousAddress })
	expectDraftDocument(mock)
	mock.ExpectQuery("SELECT tag FROM document_tags").
		WithArgs("draft").WillReturnRows(sqlmock.NewRows([]string{"tag"}))
	mock.ExpectQuery("SELECT source_slug FROM backlinks").
		WithArgs("draft").WillReturnRows(sqlmock.NewRows([]string{"source_slug"}))

	request := httptest.NewRequest(http.MethodGet, "/api/documents/draft", nil)
	request.Header.Set("X-MDHub-Edit-Token", "secret")
	response := httptest.NewRecorder()
	getDocument(response, request, "draft")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDocumentPublishesNestedSlugThroughPublicationModule(t *testing.T) {
	mock := withMockDatabase(t)
	isolateEditAccess(t)
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
	request.Header.Set("X-MDHub-Edit-Token", "secret")
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
	isolateEditAccess(t)
	isolatePublicationState(t)
	slug := "folder/note"
	searchIndex[slug] = &searchEntry{title: "Old"}
	embedIndex[slug] = []float32{1}
	mock.ExpectExec("DELETE FROM documents").
		WithArgs(slug).
		WillReturnResult(sqlmock.NewResult(0, 1))
	request := httptest.NewRequest(http.MethodDelete, "/api/documents/"+slug, nil)
	request.Header.Set("X-MDHub-Edit-Token", "secret")
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

func TestHandleDocumentRejectsMutationWithoutEditAccess(t *testing.T) {
	withMockDatabase(t)
	isolateEditAccess(t)
	request := httptest.NewRequest(http.MethodPut, "/api/documents/note", strings.NewReader("body"))
	response := httptest.NewRecorder()

	handleDocument(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}
