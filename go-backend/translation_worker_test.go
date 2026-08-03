package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type cancellationLLMProvider struct{}

func (cancellationLLMProvider) Complete(ctx context.Context, _ LLMRequest) (LLMResult, error) {
	<-ctx.Done()
	return LLMResult{}, ctx.Err()
}

func expectDraftUpsert(mock sqlmock.Sqlmock, draft *Document) {
	mock.ExpectExec("INSERT INTO documents").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM document_tags WHERE slug=$1")).
		WithArgs(draft.Slug).WillReturnResult(sqlmock.NewResult(0, 0))
	for _, tag := range draft.Tags {
		mock.ExpectExec("INSERT INTO tags").WithArgs(tag).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO document_tags").WithArgs(draft.Slug, tag).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM backlinks WHERE source_slug=$1")).
		WithArgs(draft.Slug).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM embeddings WHERE slug=$1")).
		WithArgs(draft.Slug).WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestCompleteTranslationDraftCommitsDocumentAndJobTogether(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	draft := parseDoc("_translations/paper-zh-cn-job-one", "", "---\ntitle: Draft\npublish: false\n---\n\n译文")
	report := TranslationValidationReport{Complete: true, SourceChunks: 1, TranslatedChunks: 1, Issues: []string{}}

	mock.ExpectBegin()
	expectDraftUpsert(mock, draft)
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs(draft.Slug, "openai-compatible", "paper-model", sqlmock.AnyArg(), "job-one", "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := completeTranslationDraft("job-one", "worker-one", draft, "openai-compatible", "paper-model", report); err != nil {
		t.Fatal(err)
	}
	mu.RLock()
	_, searchable := searchIndex[draft.Slug]
	mu.RUnlock()
	if searchable {
		t.Fatal("unpublished translation draft became searchable")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteTranslationDraftRollsBackWhenLeaseWasLost(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	draft := parseDoc("_translations/paper-zh-cn-job-one", "", "---\ntitle: Draft\npublish: false\n---\n\n译文")

	mock.ExpectBegin()
	expectDraftUpsert(mock, draft)
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs(draft.Slug, "openai-compatible", "paper-model", sqlmock.AnyArg(), "job-one", "former-worker").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := completeTranslationDraft("job-one", "former-worker", draft, "openai-compatible", "paper-model", TranslationValidationReport{})
	if !errors.Is(err, errTranslationLeaseLost) {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTranslationWorkerResumesCompletedChunksAndCreatesDraft(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	source := "Complete source paragraph."
	translated := "完整译文段落。"
	job := TranslationJob{
		ID:             "job-one",
		Source:         PaperSource{Kind: "pdf", Title: "Paper", CanonicalURL: "https://papers.example/paper.pdf", ArtifactURL: "https://papers.example/paper.pdf"},
		TargetLanguage: "zh-CN",
		Profile:        "paper-translate-v1",
	}
	job.SourceHash = "artifact-hash"
	job.SourceManifest = buildTranslationSourceManifest(job.SourceHash, []string{source})
	job.Provider = "openai-compatible"
	job.Model = "paper-model"
	draftSlug := translationOutputSlug(job)

	mock.ExpectQuery("SELECT ordinal, source_text, source_hash, translated_text, state, attempts").
		WithArgs(job.ID).
		WillReturnRows(sqlmock.NewRows([]string{"ordinal", "source_text", "source_hash", "translated_text", "state", "attempts", "provider", "model"}).
			AddRow(0, source, paperChunkHash(source), translated, "complete", 1, "openai-compatible", "paper-model"))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("translating", "translating", 1, 1, job.ID, "worker-one", "claimed").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("validating", "validating", 1, 1, job.ID, "worker-one", "translating").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	expectDraftUpsert(mock, &Document{Slug: draftSlug, Tags: []string{"translation", "paper"}})
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs(draftSlug, "openai-compatible", sqlmock.AnyArg(), sqlmock.AnyArg(), job.ID, "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	worker := &translationAgentWorker{id: "worker-one"}
	if err := worker.process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTranslationWorkerRunsInitialPipelineFromArtifactToDraft(t *testing.T) {
	mock := withMockDatabase(t)
	isolatePublicationState(t)
	pdf := []byte("%PDF-1.7 paper\n%%EOF")
	extracted := "Paper Title\n\nComplete source paragraph."
	job := TranslationJob{
		ID: "job-one",
		Source: PaperSource{
			Kind:         "pdf",
			CanonicalURL: "https://papers.example/paper.pdf",
			ArtifactURL:  "https://papers.example/paper.pdf",
		},
		TargetLanguage: "zh-CN",
		Profile:        "paper-translate-v1",
	}
	provider := &recordingLLMProvider{}
	client := &remoteSourceClient{http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(pdf)))}, nil
	})}}

	mock.ExpectQuery("SELECT ordinal, source_text, source_hash, translated_text, state, attempts").
		WithArgs(job.ID).
		WillReturnRows(sqlmock.NewRows([]string{"ordinal", "source_text", "source_hash", "translated_text", "state", "attempts", "provider", "model"}))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("fetching", "fetching", 0, 0, job.ID, "worker-one", "claimed").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT a.hash, a.mime, a.data").WithArgs(job.ID).WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO translation_artifacts").
		WithArgs(sqlmock.AnyArg(), "application/pdf", pdf).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), job.ID, "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("extracting", "extracting", 0, 0, job.ID, "worker-one", "fetching").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("Paper Title", 1, sqlmock.AnyArg(), job.ID, "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO translation_chunks").
		WithArgs(job.ID, 0, extracted, paperChunkHash(extracted)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT ordinal, source_text, source_hash, translated_text, state, attempts").
		WithArgs(job.ID).
		WillReturnRows(sqlmock.NewRows([]string{"ordinal", "source_text", "source_hash", "translated_text", "state", "attempts", "provider", "model"}).
			AddRow(0, extracted, paperChunkHash(extracted), "", "pending", 0, "", ""))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("translating", "translating", 0, 1, job.ID, "worker-one", "extracting").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE translation_chunks").
		WithArgs(sqlmock.AnyArg(), "paper-model", job.ID, 0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs(job.ID, "worker-one", "paper-model").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("validating", "validating", 1, 1, job.ID, "worker-one", "translating").
		WillReturnResult(sqlmock.NewResult(0, 1))
	draftSlug := translationOutputSlug(job)
	mock.ExpectBegin()
	expectDraftUpsert(mock, &Document{Slug: draftSlug, Tags: []string{"translation", "paper"}})
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs(draftSlug, "openai-compatible", "paper-model", sqlmock.AnyArg(), job.ID, "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	worker := &translationAgentWorker{
		id:       "worker-one",
		provider: provider,
		client:   client,
		extract: func(context.Context, []byte) (string, error) {
			return extracted, nil
		},
	}
	if err := worker.process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %d", len(provider.requests))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareTranslationChunksRequiresActiveExtractingLease(t *testing.T) {
	t.Run("commits metadata and chunks together", func(t *testing.T) {
		mock := withMockDatabase(t)
		chunks := []string{"first", "second"}
		manifest := buildTranslationSourceManifest("artifact-hash", chunks)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE translation_jobs").
			WithArgs("Paper", len(chunks), sqlmock.AnyArg(), "job-one", "worker-one").
			WillReturnResult(sqlmock.NewResult(0, 1))
		for ordinal, source := range chunks {
			mock.ExpectExec("INSERT INTO translation_chunks").
				WithArgs("job-one", ordinal, source, paperChunkHash(source)).
				WillReturnResult(sqlmock.NewResult(0, 1))
		}
		mock.ExpectCommit()
		if err := prepareTranslationChunks("job-one", "worker-one", "Paper", chunks, manifest); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rolls back before inserting after lease loss", func(t *testing.T) {
		mock := withMockDatabase(t)
		manifest := buildTranslationSourceManifest("artifact-hash", []string{"source"})
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE translation_jobs").
			WithArgs("Paper", 1, sqlmock.AnyArg(), "job-one", "former-worker").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectRollback()
		err := prepareTranslationChunks("job-one", "former-worker", "Paper", []string{"source"}, manifest)
		if !errors.Is(err, errTranslationLeaseLost) {
			t.Fatalf("error = %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTranslationWorkerResetsInvalidChunkForRetry(t *testing.T) {
	mock := withMockDatabase(t)
	source := "Conclusion"
	job := TranslationJob{ID: "job-one", TargetLanguage: "zh-CN", Profile: "paper-translate-v1"}

	mock.ExpectQuery("SELECT ordinal, source_text, source_hash, translated_text, state, attempts").
		WithArgs(job.ID).
		WillReturnRows(sqlmock.NewRows([]string{"ordinal", "source_text", "source_hash", "translated_text", "state", "attempts", "provider", "model"}).
			AddRow(0, source, paperChunkHash(source), "其余内容略", "complete", 1, "openai-compatible", "paper-model"))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("translating", "translating", 1, 1, job.ID, "worker-one", "claimed").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("validating", "validating", 1, 1, job.ID, "worker-one", "translating").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE translation_chunks").WithArgs(job.ID, 0).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs(sqlmock.AnyArg(), job.ID, "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	worker := &translationAgentWorker{id: "worker-one"}
	err := worker.process(context.Background(), job)
	if err == nil || !strings.Contains(err.Error(), "translation validation failed") {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFetchPaperPDFValidatesStatusAndSignature(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("User-Agent") != "MDHub-Paper-Translator/1.0" {
			t.Fatalf("user agent = %q", request.Header.Get("User-Agent"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("%PDF-1.7 paper\n%%EOF")),
		}, nil
	})}
	remoteClient := &remoteSourceClient{http: client}
	artifact, err := fetchPaperPDF(context.Background(), remoteClient, PaperSource{ArtifactURL: "https://papers.example/paper.pdf"})
	if err != nil || artifact.MIME != "application/pdf" || string(artifact.Data) != "%PDF-1.7 paper\n%%EOF" || artifact.Hash == "" {
		t.Fatalf("artifact=%#v error=%v", artifact, err)
	}

	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("<html>paywall</html>"))}, nil
	})
	_, err = fetchPaperPDF(context.Background(), remoteClient, PaperSource{ArtifactURL: "https://papers.example/paper"})
	if !errors.Is(err, errPaperNeedsInput) {
		t.Fatalf("error = %v", err)
	}
}

func TestPaperArtifactFromPDFRejectsInvalidSignature(t *testing.T) {
	if _, err := paperArtifactFromPDF([]byte("<html>paywall</html>")); !errors.Is(err, errInvalidPaperPDF) {
		t.Fatalf("error = %v", err)
	}
}

func TestBoundedByteBufferRetainsPrefixAndReportsFullWrite(t *testing.T) {
	buffer := &boundedByteBuffer{limit: 4}
	written, err := buffer.Write([]byte("abcdef"))
	if err != nil || written != 6 || buffer.String() != "abcd" {
		t.Fatalf("written=%d value=%q error=%v", written, buffer.String(), err)
	}
}

func TestRunTranslationWorkerOnceStopsWhenQueueIsEmpty(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectQuery("WITH candidate AS").WithArgs(sqlmock.AnyArg()).WillReturnError(sql.ErrNoRows)
	if err := runTranslationWorker(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunTranslationAgentRequeuesInterruptedJobInsteadOfFailingIt(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("WITH candidate AS").
		WithArgs("worker-one").
		WillReturnRows(translationJobRow("job-one", "claimed", now))
	mock.ExpectQuery("SELECT ordinal, source_text, source_hash, translated_text, state, attempts").
		WithArgs("job-one").
		WillReturnRows(sqlmock.NewRows([]string{"ordinal", "source_text", "source_hash", "translated_text", "state", "attempts", "provider", "model"}).
			AddRow(0, "source", paperChunkHash("source"), "", "pending", 0, "", ""))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("translating", "translating", 0, 1, "job-one", "worker-one", "claimed").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("job-one", "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := &translationAgentWorker{id: "worker-one", provider: cancellationLLMProvider{}}
	err := runTranslationAgent(ctx, true, worker)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimTranslationJobReturnsPersistedLeaseState(t *testing.T) {
	mock := withMockDatabase(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("WITH candidate AS").WithArgs("worker-one").WillReturnRows(translationJobRow("job-one", "claimed", now))
	job, err := claimTranslationJob("worker-one")
	if err != nil || job.ID != "job-one" || job.State != "claimed" {
		t.Fatalf("job=%#v error=%v", job, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTranslationFailurePreservesNeedsInputAndLeaseOwnership(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
		state string
	}{
		{name: "provider failure", cause: errors.New("provider unavailable"), state: "failed"},
		{name: "paper upload required", cause: fmt.Errorf("capture source: %w", errPaperNeedsInput), state: "needs_input"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mock := withMockDatabase(t)
			mock.ExpectExec("UPDATE translation_jobs").
				WithArgs(test.state, test.cause.Error(), "job-one", "worker-one").
				WillReturnResult(sqlmock.NewResult(0, 1))
			if err := failTranslationJob("job-one", "worker-one", test.cause); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStoreTranslationArtifactCommitsOnlyForLeaseOwner(t *testing.T) {
	mock := withMockDatabase(t)
	artifact := paperArtifact{Hash: "paper-hash", MIME: "application/pdf", Data: []byte("%PDF")}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO translation_artifacts").
		WithArgs(artifact.Hash, artifact.MIME, artifact.Data).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs(artifact.Hash, "sha256:"+artifact.Hash, "job-one", "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := storeTranslationArtifact("job-one", "worker-one", artifact); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteTranslationChunkCommitsProgressWithChunk(t *testing.T) {
	mock := withMockDatabase(t)
	chunk := TranslationChunk{Ordinal: 2, TranslatedText: "译文"}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE translation_chunks").
		WithArgs(chunk.TranslatedText, "paper-model", "job-one", chunk.Ordinal).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("progress_current=\\(SELECT count\\(\\*\\) FROM translation_chunks").
		WithArgs("job-one", "worker-one", "paper-model").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := completeTranslationChunk("job-one", "worker-one", chunk, "paper-model"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWithTranslationLeaseHeartbeatReturnsImmediateOperationResult(t *testing.T) {
	value, err := withTranslationLeaseHeartbeat(context.Background(), "job-one", "worker-one", func(context.Context) (string, error) {
		return "done", nil
	})
	if err != nil || value != "done" {
		t.Fatalf("value=%q error=%v", value, err)
	}
}

func TestInferPaperTitleUsesFirstSubstantialLineThenFallback(t *testing.T) {
	if got := inferPaperTitle("x\n  A   Useful   Paper Title  \nbody", "fallback"); got != "A Useful Paper Title" {
		t.Fatalf("title=%q", got)
	}
	if got := inferPaperTitle("x\nshort", "paper-id"); got != "paper-id" {
		t.Fatalf("fallback=%q", got)
	}
	if got := inferPaperTitle("", ""); got != "论文" {
		t.Fatalf("default=%q", got)
	}
}
