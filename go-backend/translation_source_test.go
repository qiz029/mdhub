package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestResolvePaperSourceNormalizesArxivAbstractURL(t *testing.T) {
	source, err := resolvePaperSource("https://arxiv.org/abs/2401.01234v3")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != "arxiv" || source.Identifier != "2401.01234" || source.Version != "v3" {
		t.Fatalf("source = %#v", source)
	}
	if source.CanonicalURL != "https://arxiv.org/abs/2401.01234v3" {
		t.Fatalf("canonical url = %q", source.CanonicalURL)
	}
	if source.ArtifactURL != "https://arxiv.org/pdf/2401.01234v3" {
		t.Fatalf("artifact url = %q", source.ArtifactURL)
	}
}

func TestResolvePaperSourceRequiresStableArxivRevision(t *testing.T) {
	for _, input := range []string{"2401.01234", "https://arxiv.org/abs/2401.01234"} {
		if _, err := resolvePaperSource(input); err == nil || !strings.Contains(err.Error(), "explicit version") {
			t.Fatalf("input %q error = %v", input, err)
		}
	}
}

func TestResolvePaperSourceNormalizesDOI(t *testing.T) {
	for _, input := range []string{"10.1000/ABC.123", "https://doi.org/10.1000/ABC.123"} {
		source, err := resolvePaperSource(input)
		if err != nil {
			t.Fatal(err)
		}
		if source.Kind != "doi" || source.Identifier != "10.1000/abc.123" || source.CanonicalURL != "https://doi.org/10.1000/abc.123" {
			t.Fatalf("source = %#v", source)
		}
	}
}

func TestSourceCaptureUsesArtifactIdentityAcrossURLAliases(t *testing.T) {
	artifact, err := paperArtifactFromPDF([]byte("%PDF-1.4\nsame paper\n%%EOF"))
	if err != nil {
		t.Fatal(err)
	}
	service := &translationSourceCaptureService{
		fetchPDF: func(context.Context, *remoteSourceClient, PaperSource) (paperArtifact, error) {
			return artifact, nil
		},
	}
	first, err := service.Capture(context.Background(), "https://one.example/paper.pdf")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Capture(context.Background(), "https://two.example/alias.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "captured" || first.ContentKey == "" || first.ContentKey != second.ContentKey || first.RevisionKey != second.RevisionKey {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}

func TestSourceCaptureVerifiesSignedDownloadWithoutPDFSuffix(t *testing.T) {
	artifact, _ := paperArtifactFromPDF([]byte("%PDF-1.4\nsigned download\n%%EOF"))
	service := &translationSourceCaptureService{
		fetchPDF: func(context.Context, *remoteSourceClient, PaperSource) (paperArtifact, error) {
			return artifact, nil
		},
	}
	capture, err := service.Capture(context.Background(), "https://papers.example/download?id=one")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Source.Kind != "web" || capture.Status != "captured" || capture.ContentKey != "sha256:"+artifact.Hash {
		t.Fatalf("capture = %#v", capture)
	}
}

func TestPaperFetcherRejectsPrivateAndMetadataAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1"} {
		if !disallowedRemoteIP(net.ParseIP(raw)) {
			t.Fatalf("address %s should be rejected", raw)
		}
	}
	if disallowedRemoteIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
}

func TestTranslationSourceResolveEndpointPreviewsDirectPDF(t *testing.T) {
	mock := withMockDatabase(t)
	artifact, err := paperArtifactFromPDF([]byte("%PDF-1.4\nverified\n%%EOF"))
	if err != nil {
		t.Fatal(err)
	}
	previous := sourceCaptureService
	sourceCaptureService = &translationSourceCaptureService{
		fetchPDF: func(context.Context, *remoteSourceClient, PaperSource) (paperArtifact, error) {
			return artifact, nil
		},
	}
	t.Cleanup(func() { sourceCaptureService = previous })
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO translation_artifacts").
		WithArgs(artifact.Hash, artifact.MIME, artifact.Data).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO translation_source_captures").
		WithArgs(sqlmock.AnyArg(), "https://papers.example/research.pdf", "pdf",
			"https://papers.example/research.pdf", "https://papers.example/research.pdf",
			"", "", "", "sha256:"+artifact.Hash, "sha256:"+artifact.Hash,
			"sha256:"+artifact.Hash, artifact.Hash, "captured", int64(len(artifact.Data))).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT id, output_slug, source_revision_key, source_content_key FROM translation_jobs").
		WithArgs("zh-CN", "paper-translate-v1", "sha256:"+artifact.Hash, "sha256:"+artifact.Hash).
		WillReturnRows(sqlmock.NewRows([]string{"id", "output_slug", "source_revision_key", "source_content_key"}))
	mock.ExpectQuery("SELECT id FROM translation_jobs").
		WithArgs("zh-CN", "paper-translate-v1", "sha256:"+artifact.Hash, "sha256:"+artifact.Hash).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	request := httptest.NewRequest(http.MethodPost, "/api/translation-sources/resolve",
		strings.NewReader(`{"source":"https://papers.example/research.pdf"}`))
	response := httptest.NewRecorder()
	handleTranslationSourceResolve(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"kind":"pdf"`) ||
		!strings.Contains(response.Body.String(), `"status":"captured"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAnnotateExistingTranslationDistinguishesPreviousRevision(t *testing.T) {
	mock := withMockDatabase(t)
	capture := &TranslationSourceCapture{
		SeriesKey: "arxiv:2401.01234", RevisionKey: "arxiv:2401.01234:v2", ContentKey: "sha256:new",
	}
	mock.ExpectQuery("SELECT id, output_slug, source_revision_key, source_content_key FROM translation_jobs").
		WithArgs("zh-CN", "paper-translate-v1", capture.RevisionKey, capture.ContentKey).
		WillReturnRows(sqlmock.NewRows([]string{"id", "output_slug", "source_revision_key", "source_content_key"}))
	mock.ExpectQuery("SELECT id FROM translation_jobs").
		WithArgs("zh-CN", "paper-translate-v1", capture.SeriesKey, capture.RevisionKey).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("job-v1"))

	if err := annotateExistingTranslation(capture, "zh-CN", "paper-translate-v1"); err != nil {
		t.Fatal(err)
	}
	if capture.ExistingJobID != "" || capture.PreviousJobID != "job-v1" {
		t.Fatalf("capture = %#v", capture)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAnnotateExistingTranslationFlagsChangedRevisionContent(t *testing.T) {
	mock := withMockDatabase(t)
	capture := &TranslationSourceCapture{
		SeriesKey: "arxiv:2401.01234", RevisionKey: "arxiv:2401.01234:v2", ContentKey: "sha256:new",
	}
	mock.ExpectQuery("SELECT id, output_slug, source_revision_key, source_content_key FROM translation_jobs").
		WithArgs("zh-CN", "paper-translate-v1", capture.RevisionKey, capture.ContentKey).
		WillReturnRows(sqlmock.NewRows([]string{"id", "output_slug", "source_revision_key", "source_content_key"}).
			AddRow("job-existing", "", capture.RevisionKey, "sha256:old"))

	if err := annotateExistingTranslation(capture, "zh-CN", "paper-translate-v1"); err != nil {
		t.Fatal(err)
	}
	if !capture.RevisionConflict || capture.ExistingJobID != "job-existing" {
		t.Fatalf("capture = %#v", capture)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePaperSourceRejectsEmbeddedCredentials(t *testing.T) {
	if _, err := resolvePaperSource("https://user:secret@example.com/paper.pdf"); err == nil {
		t.Fatal("expected credentials to be rejected")
	}
}
