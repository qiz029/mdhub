package main

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func withMockDatabase(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	previous := db
	db = mockDB
	t.Cleanup(func() {
		db = previous
		mockDB.Close()
	})
	return mock
}

func TestUpsertDocumentCommitsCompleteStateTransition(t *testing.T) {
	mock := withMockDatabase(t)
	doc := &Document{
		Slug: "notes/source", Title: "Source", Content: "body", RawContent: "# Source",
		Excerpt: "body", WordCount: 4, Published: true, Source: "agent",
		Tags: []string{"go"}, Backlinks: []string{"notes/target"},
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO documents").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM document_tags WHERE slug=$1")).
		WithArgs(doc.Slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO tags").WithArgs("go").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO document_tags").WithArgs(doc.Slug, "go").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM backlinks WHERE source_slug=$1")).
		WithArgs(doc.Slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT slug FROM documents WHERE slug=$1")).
		WithArgs("notes/target").WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("notes/target"))
	mock.ExpectExec("INSERT INTO backlinks").
		WithArgs(doc.Slug, "notes/target").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM embeddings WHERE slug=$1")).
		WithArgs(doc.Slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := upsertDocument(doc); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertDocumentRollsBackOnRelationshipFailure(t *testing.T) {
	mock := withMockDatabase(t)
	doc := &Document{Slug: "notes/source", Tags: []string{"go"}}
	wantErr := errors.New("tag store unavailable")

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO documents").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM document_tags WHERE slug=$1")).
		WithArgs(doc.Slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO tags").WithArgs("go").WillReturnError(wantErr)
	mock.ExpectRollback()

	if err := upsertDocument(doc); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped relationship error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertDocumentReportsCommitFailure(t *testing.T) {
	mock := withMockDatabase(t)
	doc := &Document{Slug: "notes/source"}
	wantErr := errors.New("commit failed")

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO documents").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM document_tags WHERE slug=$1")).
		WithArgs(doc.Slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM backlinks WHERE source_slug=$1")).
		WithArgs(doc.Slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM embeddings WHERE slug=$1")).
		WithArgs(doc.Slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(wantErr)

	if err := upsertDocument(doc); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped commit error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveStoredSlugFallsBackToTitle(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT slug FROM documents WHERE slug=$1")).
		WithArgs("Target").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT slug FROM documents WHERE strpos").
		WithArgs("Target").WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("notes/target"))

	slug, err := resolveStoredSlug(tx, "Target")
	if err != nil || slug != "notes/target" {
		t.Fatalf("slug = %q, error = %v", slug, err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveStoredSlugDecodesEncodedPathSegments(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT slug FROM documents WHERE slug=$1")).
		WithArgs("papers/memory%7Cv2%5D").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT slug FROM documents WHERE slug=$1")).
		WithArgs("papers/memory|v2]").WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("papers/memory|v2]"))

	slug, err := resolveStoredSlug(tx, "papers/memory%7Cv2%5D")
	if err != nil || slug != "papers/memory|v2]" {
		t.Fatalf("slug = %q, error = %v", slug, err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteStoredDocumentReturnsWhetherRowExisted(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM documents WHERE slug=$1")).
		WithArgs("notes/source").WillReturnResult(sqlmock.NewResult(0, 1))

	removed, err := deleteStoredDocument("notes/source")
	if err != nil || !removed {
		t.Fatalf("removed = %v, error = %v", removed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
