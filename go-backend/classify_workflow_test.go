package main

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func categoryPathRows(paths ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"category_path"})
	for _, path := range paths {
		rows.AddRow(path)
	}
	return rows
}

func TestDoInsertDescendsAndStoresChosenCategory(t *testing.T) {
	mock := withMockDatabase(t)
	server := fakeLLM(t, 200, "技术")
	mock.ExpectQuery("SELECT title, content, category_path, category_manual").
		WithArgs("note").
		WillReturnRows(sqlmock.NewRows([]string{
			"title", "content", "category_path", "category_manual",
		}).AddRow("Go testing", "content", "", false))
	// The root has one child folder, so the LLM chooses it.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT category_path FROM documents WHERE published")).
		WillReturnRows(categoryPathRows("技术/已有"))
	// The chosen folder has no child folder, so descent stops there.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT category_path FROM documents WHERE published")).
		WillReturnRows(categoryPathRows("技术"))
	mock.ExpectExec("UPDATE documents SET category_path").
		WithArgs("技术", "note").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// The final node is below the split threshold.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT category_path FROM documents WHERE published")).
		WillReturnRows(categoryPathRows("技术", "技术/已有"))

	if err := doInsert("note", server.Client()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoInsertSkipsPinnedOrAlreadyPlacedDocument(t *testing.T) {
	for _, row := range []struct {
		name   string
		path   string
		manual bool
	}{
		{name: "manual", manual: true},
		{name: "already placed", path: "技术"},
	} {
		t.Run(row.name, func(t *testing.T) {
			mock := withMockDatabase(t)
			mock.ExpectQuery("SELECT title, content, category_path, category_manual").
				WithArgs("note").
				WillReturnRows(sqlmock.NewRows([]string{
					"title", "content", "category_path", "category_manual",
				}).AddRow("title", "content", row.path, row.manual))
			if err := doInsert("note", nil); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDoSplitStoresValidGroupingAtomically(t *testing.T) {
	mock := withMockDatabase(t)
	server := fakeLLM(t, 200, `{"groups":[{"name":"语言","slugs":["a"]},{"name":"数据库","slugs":["b"]}]}`)
	mock.ExpectQuery("SELECT slug, title, content FROM documents").
		WithArgs("技术").
		WillReturnRows(sqlmock.NewRows([]string{"slug", "title", "content"}).
			AddRow("a", "Go", "language").
			AddRow("b", "Postgres", "database"))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE documents SET category_path").
		WithArgs("技术/语言", "a", "技术").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE documents SET category_path").
		WithArgs("技术/数据库", "b", "技术").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT category_path FROM documents WHERE published")).
		WillReturnRows(categoryPathRows("技术/语言", "技术/数据库"))

	if err := doSplit("技术", server.Client()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoSplitStopsWhenFewerThanTwoMovableNotes(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT slug, title, content FROM documents").
		WithArgs("技术").
		WillReturnRows(sqlmock.NewRows([]string{"slug", "title", "content"}).
			AddRow("only", "Only", "content"))

	if err := doSplit("技术", nil); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
