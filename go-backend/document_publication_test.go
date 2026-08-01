package main

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func isolatePublicationState(t *testing.T) {
	t.Helper()
	mu.Lock()
	previousSearch := searchIndex
	previousEmbed := embedIndex
	searchIndex = map[string]*searchEntry{}
	embedIndex = map[string][]float32{}
	mu.Unlock()
	previousLLMKey, previousEmbedURL := llmAPIKey, embedBaseURL
	llmAPIKey, embedBaseURL = "", ""
	t.Cleanup(func() {
		mu.Lock()
		searchIndex = previousSearch
		embedIndex = previousEmbed
		mu.Unlock()
		llmAPIKey, embedBaseURL = previousLLMKey, previousEmbedURL
	})
}

func expectDocumentWrite(mock sqlmock.Sqlmock, slug string) {
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO documents").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM document_tags WHERE slug=$1")).
		WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM backlinks WHERE source_slug=$1")).
		WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM embeddings WHERE slug=$1")).
		WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func TestPublishDocumentUpdatesProjectionsAfterCommit(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	doc := &Document{Slug: "notes/source", Title: "Source", Content: "Fresh BODY", Published: true}
	searchIndex[doc.Slug] = &searchEntry{title: "Old"}
	embedIndex[doc.Slug] = []float32{1, 2}
	expectDocumentWrite(mock, doc.Slug)
	generation := universeVectorGeneration.Load()

	if err := publishDocument(doc); err != nil {
		t.Fatal(err)
	}

	mu.RLock()
	entry := searchIndex[doc.Slug]
	_, hasEmbedding := embedIndex[doc.Slug]
	mu.RUnlock()
	if entry == nil || entry.title != doc.Title || entry.plain != "fresh body" || entry.mtime <= 0 {
		t.Fatalf("search projection = %#v", entry)
	}
	if hasEmbedding {
		t.Fatal("stale embedding remained after publication")
	}
	if got := universeVectorGeneration.Load(); got != generation+1 {
		t.Fatalf("universe generation = %d, want %d", got, generation+1)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishDocumentFleetingStaysOutOfSearchIndexButEnqueuesEmbed(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	embedBaseURL = "http://embed"
	doc := &Document{Slug: "_sparks/1", Title: "Spark", Content: "idea", Kind: "fleeting"}
	searchIndex[doc.Slug] = &searchEntry{title: "Old"}
	expectDocumentWrite(mock, doc.Slug)

	if err := publishDocument(doc); err != nil {
		t.Fatal(err)
	}
	mu.RLock()
	_, searchable := searchIndex[doc.Slug]
	mu.RUnlock()
	if searchable {
		t.Fatal("fleeting note entered the public search index")
	}
	if got := drainEmbedJob(t); got != doc.Slug {
		t.Fatalf("embed job = %q, want %q", got, doc.Slug)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishDocumentDraftRemovesRuntimeProjections(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	doc := &Document{Slug: "notes/source", Title: "Source", Content: "draft"}
	searchIndex[doc.Slug] = &searchEntry{title: "Published"}
	embedIndex[doc.Slug] = []float32{1}
	expectDocumentWrite(mock, doc.Slug)

	if err := publishDocument(doc); err != nil {
		t.Fatal(err)
	}
	mu.RLock()
	_, searchable := searchIndex[doc.Slug]
	_, embedded := embedIndex[doc.Slug]
	mu.RUnlock()
	if searchable || embedded {
		t.Fatalf("draft remained projected: searchable=%v embedded=%v", searchable, embedded)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishDocumentStoreFailureLeavesProjectionsUntouched(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	doc := &Document{Slug: "notes/source", Title: "New", Content: "new", Published: true}
	oldEntry := &searchEntry{title: "Old"}
	oldVector := []float32{1, 2}
	searchIndex[doc.Slug] = oldEntry
	embedIndex[doc.Slug] = oldVector
	wantErr := errors.New("store unavailable")
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO documents").WillReturnError(wantErr)
	mock.ExpectRollback()
	generation := universeVectorGeneration.Load()

	if err := publishDocument(doc); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped store error", err)
	}
	mu.RLock()
	entry := searchIndex[doc.Slug]
	vector := embedIndex[doc.Slug]
	mu.RUnlock()
	if entry != oldEntry || len(vector) != len(oldVector) || vector[0] != oldVector[0] {
		t.Fatalf("projections changed after failure: entry=%#v vector=%v", entry, vector)
	}
	if got := universeVectorGeneration.Load(); got != generation {
		t.Fatalf("universe generation changed from %d to %d", generation, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveDocumentFailureLeavesProjectionsUntouched(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	slug := "notes/source"
	oldEntry := &searchEntry{title: "Old"}
	searchIndex[slug] = oldEntry
	embedIndex[slug] = []float32{1}
	wantErr := errors.New("delete unavailable")
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM documents WHERE slug=$1")).
		WithArgs(slug).WillReturnError(wantErr)
	generation := universeVectorGeneration.Load()

	if _, err := removeDocument(slug); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want delete error", err)
	}
	mu.RLock()
	entry := searchIndex[slug]
	_, embedded := embedIndex[slug]
	mu.RUnlock()
	if entry != oldEntry || !embedded {
		t.Fatalf("projections changed after delete failure: entry=%#v embedded=%v", entry, embedded)
	}
	if got := universeVectorGeneration.Load(); got != generation {
		t.Fatalf("universe generation changed from %d to %d", generation, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
