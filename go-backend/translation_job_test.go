package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func translationPDFUploadRequest(t *testing.T, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "paper.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/translation-jobs/job-one/source", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

var translationJobTestColumns = []string{
	"id", "source_input", "source_kind", "canonical_url", "artifact_url",
	"source_identifier", "source_version", "source_title", "source_hash", "source_manifest",
	"source_series_key", "source_revision_key", "source_content_key", "target_language",
	"profile", "state", "stage", "progress_current", "progress_total",
	"output_slug", "provider", "model", "validation_report", "error_summary",
	"created_at", "updated_at",
}

func translationJobRow(id, state string, now time.Time) *sqlmock.Rows {
	return translationJobRowWithIdentity(id, state, now, "url:paper", "url:paper", "")
}

func translationJobRowWithIdentity(id, state string, now time.Time, seriesKey, revisionKey, contentKey string) *sqlmock.Rows {
	return sqlmock.NewRows(translationJobTestColumns).AddRow(
		id, "https://example.com/paper.pdf", "pdf", "https://example.com/paper.pdf",
		"https://example.com/paper.pdf", "", "", "Paper", "", nil,
		seriesKey, revisionKey, contentKey, "zh-CN", "paper-translate-v1",
		state, state, 1, 2, "", "", "", nil, "", now, now,
	)
}

func TestCreateTranslationJobQueuesNormalizedArxivSource(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT[[:space:]]+id, source_input, source_kind").
		WithArgs("capture-one").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source_input", "source_kind", "canonical_url", "artifact_url",
			"source_identifier", "source_version", "source_title", "series_key", "revision_key",
			"content_key", "artifact_hash", "status", "size_bytes",
		}).AddRow(
			"capture-one", "https://arxiv.org/abs/2401.01234v2", "arxiv",
			"https://arxiv.org/abs/2401.01234v2", "https://arxiv.org/pdf/2401.01234v2",
			"2401.01234", "v2", "Paper", "arxiv:2401.01234", "arxiv:2401.01234:v2",
			"sha256:artifact-hash", "artifact-hash", "captured", int64(1234),
		))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT[[:space:][:print:]]*FROM translation_jobs").
		WithArgs("zh-CN", "paper-translate-v1", "arxiv:2401.01234:v2", "sha256:artifact-hash").
		WillReturnRows(sqlmock.NewRows(translationJobTestColumns))
	mock.ExpectQuery("INSERT INTO translation_jobs").
		WithArgs(
			sqlmock.AnyArg(),
			"https://arxiv.org/abs/2401.01234v2",
			"arxiv",
			"https://arxiv.org/abs/2401.01234v2",
			"https://arxiv.org/pdf/2401.01234v2",
			"2401.01234",
			"v2",
			"Paper",
			"artifact-hash",
			"artifact-hash",
			"capture-one",
			"arxiv:2401.01234",
			"arxiv:2401.01234:v2",
			"sha256:artifact-hash",
			"zh-CN",
			"paper-translate-v1",
			"queued",
		).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))
	mock.ExpectCommit()

	request := httptest.NewRequest(http.MethodPost, "/api/translation-jobs",
		strings.NewReader(`{"capture_id":"capture-one"}`))
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

func TestCreateTranslationJobReusesActiveSourceRevision(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT[[:space:]]+id, source_input, source_kind").
		WithArgs("capture-one").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source_input", "source_kind", "canonical_url", "artifact_url",
			"source_identifier", "source_version", "source_title", "series_key", "revision_key",
			"content_key", "artifact_hash", "status", "size_bytes",
		}).AddRow(
			"capture-one", "10.1000/example", "doi", "https://doi.org/10.1000/example",
			"https://doi.org/10.1000/example", "10.1000/example", "", "", "doi:10.1000/example",
			"doi:10.1000/example", "", "", "needs_input", int64(0),
		))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT[[:space:][:print:]]*FROM translation_jobs").
		WithArgs("zh-CN", "paper-translate-v1", "doi:10.1000/example", "").
		WillReturnRows(translationJobRow("job-existing", "needs_input", now))
	mock.ExpectCommit()

	job, created, err := createTranslationJobFromCapture("capture-one", "zh-CN", "paper-translate-v1")
	if err != nil || created || job.ID != "job-existing" {
		t.Fatalf("job=%#v created=%v error=%v", job, created, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTranslationJobRejectsChangedArtifactForSameArxivRevision(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT[[:space:]]+id, source_input, source_kind").
		WithArgs("capture-one").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source_input", "source_kind", "canonical_url", "artifact_url",
			"source_identifier", "source_version", "source_title", "series_key", "revision_key",
			"content_key", "artifact_hash", "status", "size_bytes",
		}).AddRow(
			"capture-one", "2401.01234v2", "arxiv", "https://arxiv.org/abs/2401.01234v2",
			"https://arxiv.org/pdf/2401.01234v2", "2401.01234", "v2", "Paper",
			"arxiv:2401.01234", "arxiv:2401.01234:v2", "sha256:new",
			"new", "captured", int64(1234),
		))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT[[:space:][:print:]]*FROM translation_jobs").
		WithArgs("zh-CN", "paper-translate-v1", "arxiv:2401.01234:v2", "sha256:new").
		WillReturnRows(translationJobRowWithIdentity(
			"job-existing", "queued", now, "arxiv:2401.01234", "arxiv:2401.01234:v2", "sha256:old"))
	mock.ExpectRollback()

	_, _, err := createTranslationJobFromCapture("capture-one", "zh-CN", "paper-translate-v1")
	if !errors.Is(err, errTranslationRevisionConflict) {
		t.Fatalf("error = %v", err)
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

func TestHandleTranslationJobReturnsPersistedProgressAndChunks(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT .* FROM translation_jobs WHERE id=\\$1").
		WithArgs("job-one").
		WillReturnRows(translationJobRow("job-one", "translating", now))
	mock.ExpectQuery("SELECT ordinal, source_text, source_hash, translated_text, state, attempts").
		WithArgs("job-one").
		WillReturnRows(sqlmock.NewRows([]string{"ordinal", "source_text", "source_hash", "translated_text", "state", "attempts", "provider", "model"}).
			AddRow(0, "source", paperChunkHash("source"), "译文", "complete", 1, "openai-compatible", "paper-model"))

	request := httptest.NewRequest(http.MethodGet, "/api/translation-jobs/job-one", nil)
	response := httptest.NewRecorder()
	handleTranslationJob(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"progress_total":2`) || !strings.Contains(response.Body.String(), `"translated_text":"译文"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListTranslationJobsHandlerReturnsPersistedJobs(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT .* FROM translation_jobs ORDER BY created_at DESC LIMIT 100").
		WillReturnRows(translationJobRow("job-one", "queued", now))
	request := httptest.NewRequest(http.MethodGet, "/api/translation-jobs", nil)
	response := httptest.NewRecorder()

	handleTranslationJobs(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"job-one"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTranslationSourceUploadHandlerQueuesVerifiedPDF(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	pdf := []byte("%PDF-1.7 uploaded paper\n%%EOF")
	artifact, err := paperArtifactFromPDF(pdf)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, target_language, profile, source_capture_id").
		WithArgs("job-one").
		WillReturnRows(sqlmock.NewRows([]string{"state", "target_language", "profile", "source_capture_id", "source_kind", "source_revision_key"}).
			AddRow("needs_input", "zh-CN", "paper-translate-v1", "capture-one", "web", "url:paper"))
	mock.ExpectExec("INSERT INTO translation_artifacts").
		WithArgs(artifact.Hash, artifact.MIME, artifact.Data).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id FROM translation_jobs").
		WithArgs("job-one", "zh-CN", "paper-translate-v1", "sha256:"+artifact.Hash).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("UPDATE translation_source_captures").
		WithArgs(artifact.Hash, len(artifact.Data), "sha256:"+artifact.Hash, "sha256:"+artifact.Hash, "capture-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM translation_chunks WHERE job_id=$1")).
		WithArgs("job-one").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET source_kind='pdf'").
		WithArgs(artifact.Hash, "sha256:"+artifact.Hash, "sha256:"+artifact.Hash, "job-one").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT .* FROM translation_jobs WHERE id=\\$1").
		WithArgs("job-one").WillReturnRows(translationJobRow("job-one", "queued", now))

	request := translationPDFUploadRequest(t, pdf)
	response := httptest.NewRecorder()
	handleTranslationJob(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"queued"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleTranslationJobMapsStateConflictAndMissingJob(t *testing.T) {
	t.Run("state conflict", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectExec("UPDATE translation_jobs").WithArgs("job-one").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("SELECT EXISTS").WithArgs("job-one").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		request := httptest.NewRequest(http.MethodPost, "/api/translation-jobs/job-one/retry", nil)
		response := httptest.NewRecorder()
		handleTranslationJob(response, request)
		if response.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("missing", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectExec("UPDATE translation_jobs").WithArgs("missing-job").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("SELECT EXISTS").WithArgs("missing-job").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		request := httptest.NewRequest(http.MethodPost, "/api/translation-jobs/missing-job/cancel", nil)
		response := httptest.NewRecorder()
		handleTranslationJob(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTranslationJobActionsApplyOnlyAllowedTransitions(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectExec("state IN \\('queued','claimed','fetching','extracting','translating','validating','failed','needs_input'\\)").
			WithArgs("job-one").WillReturnResult(sqlmock.NewResult(0, 1))
		if err := cancelTranslationJob("job-one"); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("retry", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectExec("UPDATE translation_jobs").WithArgs("job-one").WillReturnResult(sqlmock.NewResult(0, 1))
		if err := retryTranslationJob("job-one"); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestParseTranslationPDFUploadValidatesSignature(t *testing.T) {
	valid := []byte("%PDF-1.7 uploaded paper\n%%EOF")
	artifact, status, err := parseTranslationPDFUpload(httptest.NewRecorder(), translationPDFUploadRequest(t, valid))
	if err != nil || status != http.StatusOK || !bytes.Equal(artifact.Data, valid) || artifact.Hash == "" {
		t.Fatalf("artifact=%#v status=%d error=%v", artifact, status, err)
	}

	_, status, err = parseTranslationPDFUpload(httptest.NewRecorder(), translationPDFUploadRequest(t, []byte("not a pdf")))
	if !errors.Is(err, errInvalidPaperPDF) || status != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d error=%v", status, err)
	}
	_, status, err = parseTranslationPDFUpload(httptest.NewRecorder(), translationPDFUploadRequest(t, []byte("%PDF-1.7 truncated")))
	if !errors.Is(err, errInvalidPaperPDF) || status != http.StatusUnsupportedMediaType || !strings.Contains(err.Error(), "trailer") {
		t.Fatalf("status=%d error=%v", status, err)
	}
}

func TestAttachTranslationPDFAtomicallyResetsNeedsInputJob(t *testing.T) {
	mock := withMockDatabase(t)
	artifact, err := paperArtifactFromPDF([]byte("%PDF-1.7 uploaded paper\n%%EOF"))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, target_language, profile, source_capture_id").
		WithArgs("job-one").
		WillReturnRows(sqlmock.NewRows([]string{"state", "target_language", "profile", "source_capture_id", "source_kind", "source_revision_key"}).
			AddRow("needs_input", "zh-CN", "paper-translate-v1", "capture-one", "web", "url:paper"))
	mock.ExpectExec("INSERT INTO translation_artifacts").
		WithArgs(artifact.Hash, artifact.MIME, artifact.Data).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id FROM translation_jobs").
		WithArgs("job-one", "zh-CN", "paper-translate-v1", "sha256:"+artifact.Hash).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("UPDATE translation_source_captures").
		WithArgs(artifact.Hash, len(artifact.Data), "sha256:"+artifact.Hash, "sha256:"+artifact.Hash, "capture-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM translation_chunks WHERE job_id=$1")).
		WithArgs("job-one").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("SET source_kind='pdf'").
		WithArgs(artifact.Hash, "sha256:"+artifact.Hash, "sha256:"+artifact.Hash, "job-one").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := attachTranslationPDF("job-one", artifact); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAttachTranslationPDFRejectsNonNeedsInputJobWithoutMutation(t *testing.T) {
	mock := withMockDatabase(t)
	artifact, _ := paperArtifactFromPDF([]byte("%PDF-1.7 uploaded paper\n%%EOF"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, target_language, profile, source_capture_id").
		WithArgs("job-one").
		WillReturnRows(sqlmock.NewRows([]string{"state", "target_language", "profile", "source_capture_id", "source_kind", "source_revision_key"}).
			AddRow("draft_ready", "zh-CN", "paper-translate-v1", "capture-one", "web", "url:paper"))
	mock.ExpectRollback()

	if err := attachTranslationPDF("job-one", artifact); !errors.Is(err, errTranslationStateConflict) {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAttachTranslationPDFRejectsDuplicateActiveArtifact(t *testing.T) {
	mock := withMockDatabase(t)
	artifact, _ := paperArtifactFromPDF([]byte("%PDF-1.7 uploaded paper\n%%EOF"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, target_language, profile, source_capture_id").
		WithArgs("job-one").
		WillReturnRows(sqlmock.NewRows([]string{"state", "target_language", "profile", "source_capture_id", "source_kind", "source_revision_key"}).
			AddRow("needs_input", "zh-CN", "paper-translate-v1", "capture-one", "web", "url:paper"))
	mock.ExpectExec("INSERT INTO translation_artifacts").
		WithArgs(artifact.Hash, artifact.MIME, artifact.Data).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id FROM translation_jobs").
		WithArgs("job-one", "zh-CN", "paper-translate-v1", "sha256:"+artifact.Hash).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("job-existing"))
	mock.ExpectRollback()

	if err := attachTranslationPDF("job-one", artifact); !errors.Is(err, errTranslationDuplicateSource) {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryTranslationJobsReturnsRowsAndPropagatesScanErrors(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	t.Run("list", func(t *testing.T) {
		mock := withMockDatabase(t)
		mock.ExpectQuery("SELECT .* FROM translation_jobs ORDER BY created_at DESC LIMIT 100").
			WillReturnRows(translationJobRow("job-one", "queued", now))
		jobs, err := queryTranslationJobs("")
		if err != nil || len(jobs) != 1 || jobs[0].ID != "job-one" || jobs[0].CreatedAt != now.UnixMilli() {
			t.Fatalf("jobs=%#v error=%v", jobs, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		mock := withMockDatabase(t)
		want := errors.New("database unavailable")
		mock.ExpectQuery("SELECT .* FROM translation_jobs WHERE id=\\$1").WithArgs("job-one").WillReturnError(want)
		_, err := queryTranslationJobs("job-one")
		if !errors.Is(err, want) {
			t.Fatalf("error=%v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestValidTranslationJobIDRestrictsPathSegments(t *testing.T) {
	for _, id := range []string{"job-one", "abc123", "a"} {
		if !validTranslationJobID(id) {
			t.Fatalf("valid id rejected: %q", id)
		}
	}
	for _, id := range []string{"", "JOB", "../job", "job_one", strings.Repeat("a", 81)} {
		if validTranslationJobID(id) {
			t.Fatalf("invalid id accepted: %q", id)
		}
	}
}

func TestTranslationActionMissPropagatesDatabaseFailure(t *testing.T) {
	mock := withMockDatabase(t)
	want := errors.New("database unavailable")
	mock.ExpectQuery("SELECT EXISTS").WithArgs("job-one").WillReturnError(want)
	if err := translationActionMiss("job-one"); !errors.Is(err, want) || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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
		WillReturnRows(sqlmock.NewRows(translationJobTestColumns).AddRow(
			"job-one", "https://example.com/paper.pdf", "pdf", "https://example.com/paper.pdf",
			"https://example.com/paper.pdf", "", "", "Paper", "", nil,
			"url:paper", "url:paper", "",
			"zh-CN", "paper-translate-v1", "draft_ready", "draft_ready", 1, 1, slug, "openai-compatible", "model", nil, "", now, now,
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
