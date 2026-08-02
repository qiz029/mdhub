package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetCommentsReturnsThreads(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("FROM comment_threads").
		WithArgs("draft").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "quote", "prefix", "suffix", "author", "text", "created_at",
		}).AddRow("thread", "some quote", "before", "after", "Todd", "note", time.Now()))

	// comments are readable for any document, published or not
	request := httptest.NewRequest(http.MethodGet, "/api/documents/draft/comments", nil)
	response := httptest.NewRecorder()
	getComments(response, request, "draft")

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "some quote") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostCommentCreatesThreadAtomically(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT published FROM documents WHERE slug=$1")).
		WithArgs("published").
		WillReturnRows(sqlmock.NewRows([]string{"published"}).AddRow(true))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO comment_threads").
		WithArgs(sqlmock.AnyArg(), "published", "quote", "before", "after").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO comment_entries").
		WithArgs(sqlmock.AnyArg(), "用户", "hello").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/documents/published/comments",
		strings.NewReader(`{"text":" hello ","anchor":{"quote":" quote ","prefix":"before","suffix":"after"}}`),
	)
	response := httptest.NewRecorder()
	postComment(response, request, "published")

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostCommentRejectsDraftBeforeReadingBody(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT published FROM documents WHERE slug=$1")).
		WithArgs("draft").
		WillReturnRows(sqlmock.NewRows([]string{"published"}).AddRow(false))

	request := httptest.NewRequest(http.MethodPost, "/api/documents/draft/comments", strings.NewReader("not json"))
	response := httptest.NewRecorder()
	postComment(response, request, "draft")

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
