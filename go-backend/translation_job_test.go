package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreateTranslationJobQueuesNormalizedArxivSource(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("INSERT INTO translation_jobs").
		WithArgs(
			sqlmock.AnyArg(),
			"https://arxiv.org/abs/2401.01234v2",
			"arxiv",
			"https://arxiv.org/abs/2401.01234v2",
			"https://arxiv.org/pdf/2401.01234v2",
			"2401.01234",
			"v2",
			"zh-CN",
			"paper-translate-v1",
		).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))

	request := httptest.NewRequest(http.MethodPost, "/api/translation-jobs",
		strings.NewReader(`{"source":"https://arxiv.org/abs/2401.01234v2"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handleTranslationJobs(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %q", response.Code, response.Body.String())
	}
	var job TranslationJob
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.ID == "" || job.State != "queued" || job.Source.Kind != "arxiv" {
		t.Fatalf("job = %#v", job)
	}
	if job.Source.ArtifactURL != "https://arxiv.org/pdf/2401.01234v2" {
		t.Fatalf("artifact = %q", job.Source.ArtifactURL)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSetMarkdownPublishedOnlyChangesFrontmatter(t *testing.T) {
	raw := "---\ntitle: Paper\npublish: false\nnote: publish: false stays\n---\n\nBody publish: false"
	got := setMarkdownPublished(raw)
	if !strings.Contains(got, "\npublish: true\n") {
		t.Fatalf("publish flag not changed:\n%s", got)
	}
	if !strings.Contains(got, "note: publish: false stays") || !strings.Contains(got, "Body publish: false") {
		t.Fatalf("non-frontmatter content changed:\n%s", got)
	}
}

func TestPublishTranslationJobCommitsDocumentAndStateAtomically(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	slug := "_translations/paper-zh-cn-job-one"
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM translation_jobs WHERE id=\\$1 FOR UPDATE").
		WithArgs("job-one").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source_input", "source_kind", "canonical_url", "artifact_url",
			"source_identifier", "source_version", "source_title", "target_language",
			"profile", "state", "stage", "progress_current", "progress_total",
			"output_slug", "provider", "model", "validation_report", "error_summary",
			"created_at", "updated_at",
		}).AddRow(
			"job-one", "https://example.com/paper.pdf", "pdf", "https://example.com/paper.pdf",
			"https://example.com/paper.pdf", "", "", "Paper", "zh-CN", "paper-translate-v1",
			"draft_ready", "draft_ready", 1, 1, slug, "openai-compatible", "model", nil, "", now, now,
		))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT raw_content FROM documents WHERE slug=$1 FOR UPDATE")).
		WithArgs(slug).
		WillReturnRows(sqlmock.NewRows([]string{"raw_content"}).AddRow("---\ntitle: Paper\npublish: false\nsource: agent/translation\n---\n\n译文"))
	mock.ExpectExec("INSERT INTO documents").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM document_tags WHERE slug=$1")).WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM backlinks WHERE source_slug=$1")).WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM embeddings WHERE slug=$1")).WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE translation_jobs").WithArgs("job-one").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := publishTranslationJob("job-one"); err != nil {
		t.Fatal(err)
	}
	mu.RLock()
	_, searchable := searchIndex[slug]
	mu.RUnlock()
	if !searchable {
		t.Fatal("published translation was not projected after commit")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
