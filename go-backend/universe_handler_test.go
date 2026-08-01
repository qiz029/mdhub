package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func isolateUniverseState(t *testing.T) {
	t.Helper()
	universeCache.Lock()
	previousReady, previousKey, previousGraph := universeCache.ready, universeCache.key, universeCache.graph
	universeCache.ready = false
	universeCache.key = 0
	universeCache.graph = universeGraph{}
	universeCache.Unlock()
	t.Cleanup(func() {
		universeCache.Lock()
		universeCache.ready = previousReady
		universeCache.key = previousKey
		universeCache.graph = previousGraph
		universeCache.Unlock()
	})
}

func universeDocumentRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"slug", "title", "excerpt", "category_path", "file_mtime", "word_count", "tags",
	}).
		AddRow("a", "Alpha", "first", "技术", time.Unix(100, 0), 100, "{go}").
		AddRow("b", "Beta", "second", "技术", time.Unix(200, 0), 200, "{}")
}

func TestLoadUniverseDocumentsDecodesRows(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT d.slug, d.title, d.excerpt").WillReturnRows(universeDocumentRows())

	docs, err := loadUniverseDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Slug != "a" || docs[0].Updated != time.Unix(100, 0).UnixMilli() {
		t.Fatalf("documents = %#v", docs)
	}
	if len(docs[0].Tags) != 1 || docs[0].Tags[0] != "go" || docs[1].Tags == nil {
		t.Fatalf("tags were not normalized: %#v", docs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleUniverseBuildsGraphFromStoreAndEmbeddings(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	isolateUniverseState(t)
	embedIndex["a"] = []float32{1, 0}
	embedIndex["b"] = []float32{0.9, 0.1}
	mock.ExpectQuery("SELECT d.slug, d.title, d.excerpt").WillReturnRows(universeDocumentRows())
	request := httptest.NewRequest(http.MethodGet, "/api/universe", nil)
	response := httptest.NewRecorder()

	handleUniverse(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"documents":2`) {
		t.Fatalf("status = %d body = %q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"id":"a"`) {
		t.Fatalf("graph omitted document: %q", response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleUniverseRejectsMethodAndHidesStoreError(t *testing.T) {
	response := httptest.NewRecorder()
	handleUniverse(response, httptest.NewRequest(http.MethodPost, "/api/universe", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", response.Code)
	}

	mock := withMockDatabase(t)
	isolateUniverseState(t)
	mock.ExpectQuery("SELECT d.slug, d.title, d.excerpt").WillReturnError(errors.New("database private"))
	response = httptest.NewRecorder()
	handleUniverse(response, httptest.NewRequest(http.MethodGet, "/api/universe", nil))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("error response = %d %q", response.Code, response.Body.String())
	}
}

func TestHandleRelatedDocumentsValidatesAndRanks(t *testing.T) {
	isolatePublicationState(t)
	searchIndex["a"] = &searchEntry{title: "Alpha"}
	searchIndex["b"] = &searchEntry{title: "Beta"}
	searchIndex["c"] = &searchEntry{title: "Gamma"}
	embedIndex["a"] = []float32{1, 0}
	embedIndex["b"] = []float32{0.9, 0.1}
	embedIndex["c"] = []float32{-1, 0}

	for _, test := range []struct {
		name   string
		method string
		url    string
		status int
	}{
		{name: "method", method: http.MethodPost, url: "/api/related?slug=a", status: 405},
		{name: "missing", method: http.MethodGet, url: "/api/related", status: 400},
		{name: "success", method: http.MethodGet, url: "/api/related?slug=a", status: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handleRelatedDocuments(response, httptest.NewRequest(test.method, test.url, nil))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if test.name == "success" && (!strings.Contains(response.Body.String(), `"slug":"b"`) || strings.Contains(response.Body.String(), `"slug":"c"`)) {
				t.Fatalf("related response = %q", response.Body.String())
			}
		})
	}
}
