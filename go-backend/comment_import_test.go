package main

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestImportCommentsEmptySidecarClearsExistingThreads(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM comment_threads WHERE slug=$1")).
		WithArgs("note").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	threads, replies, err := importComments("note", "")
	if err != nil || threads != 0 || replies != 0 {
		t.Fatalf("threads=%d replies=%d error=%v", threads, replies, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportCommentsRollsBackCompleteReplacementOnEntryFailure(t *testing.T) {
	mock := withMockDatabase(t)
	wantErr := errors.New("entry unavailable")
	raw := "## Todd · 2026-08-01 01:00\n<!-- {\"id\":\"thread\",\"quote\":\"quote\",\"prefix\":\"\",\"suffix\":\"\"} -->\nhello"
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM comment_threads WHERE slug=$1")).
		WithArgs("note").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO comment_threads").
		WithArgs("thread", "note", "quote", "", "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO comment_entries").
		WithArgs("thread", "Todd", "hello", sqlmock.AnyArg()).
		WillReturnError(wantErr)
	mock.ExpectRollback()

	threads, replies, err := importComments("note", raw)
	if !errors.Is(err, wantErr) || threads != 0 || replies != 0 {
		t.Fatalf("threads=%d replies=%d error=%v", threads, replies, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
