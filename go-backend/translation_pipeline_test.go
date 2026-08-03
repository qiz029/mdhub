package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

type recordingLLMProvider struct {
	requests []LLMRequest
}

type truncatedLLMProvider struct{}

func (truncatedLLMProvider) Complete(context.Context, LLMRequest) (LLMResult, error) {
	return LLMResult{Content: "partial", Model: "paper-model", FinishReason: "length"}, nil
}

func (p *recordingLLMProvider) Complete(_ context.Context, request LLMRequest) (LLMResult, error) {
	p.requests = append(p.requests, request)
	return LLMResult{Content: "译文 " + request.User, Model: "paper-model"}, nil
}

func TestChunkPaperTextPreservesEveryBlockWithinLimit(t *testing.T) {
	source := "Abstract\n\n" + strings.Repeat("甲", 12) + "\n\nConclusion"
	chunks := chunkPaperText(source, 10)
	if len(chunks) < 3 {
		t.Fatalf("chunks = %#v", chunks)
	}
	for _, chunk := range chunks {
		if len([]rune(chunk)) > 10 {
			t.Fatalf("chunk exceeds limit: %q", chunk)
		}
	}
	if got := strings.Join(chunks, ""); got != source {
		t.Fatalf("reassembled = %q", got)
	}
}

func TestReadBoundedOutputRejectsBeforeUnboundedAllocation(t *testing.T) {
	if _, err := readBoundedOutput(bytes.NewReader(bytes.Repeat([]byte("x"), 33)), 32); err == nil {
		t.Fatal("expected bounded output error")
	}
	got, err := readBoundedOutput(bytes.NewReader([]byte("paper")), 32)
	if err != nil || string(got) != "paper" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestRenewTranslationLeaseDetectsLostOwnership(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectExec("lease_until > now\\(\\)").
		WithArgs("job-one", "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := renewTranslationLease(context.Background(), "job-one", "worker-one"); !errors.Is(err, errTranslationLeaseLost) {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdvanceTranslationStageRequiresExpectedStateAndLiveLease(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectExec("state=\\$7 AND lease_until > now\\(\\)").
		WithArgs("translating", "translating", 2, 3, "job-one", "worker-one", "extracting").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := advanceTranslationStage("job-one", "worker-one", translationExtracting, translationTranslating, 2, 3)
	if !errors.Is(err, errTranslationLeaseLost) {
		t.Fatalf("error = %v, want lease lost", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTranslationChunksRejectsMissingAndOmittedContent(t *testing.T) {
	report := validateTranslationChunks([]TranslationChunk{
		{Ordinal: 0, SourceText: "Abstract", SourceHash: paperChunkHash("Abstract"), TranslatedText: "摘要"},
		{Ordinal: 1, SourceText: "Methods", SourceHash: paperChunkHash("Methods"), TranslatedText: ""},
		{Ordinal: 2, SourceText: "Conclusion", SourceHash: paperChunkHash("Conclusion"), TranslatedText: "其余内容略"},
	})
	if report.Complete {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Issues) != 2 {
		t.Fatalf("issues = %#v", report.Issues)
	}
}

func TestValidateTranslationEvidenceRejectsSelfConsistentPrefix(t *testing.T) {
	sources := []string{"Abstract\n\nFirst half.", "Conclusion\n\nFinal half."}
	job := TranslationJob{
		Source:         PaperSource{Kind: "pdf", CanonicalURL: "https://example.com/paper.pdf"},
		SourceHash:     "artifact-hash",
		SourceManifest: buildTranslationSourceManifest("artifact-hash", sources),
		Profile:        "paper-translate-v1",
		Provider:       "openai-compatible",
		Model:          "paper-model",
	}
	prefix := []TranslationChunk{{
		Ordinal: 0, SourceText: sources[0], SourceHash: paperChunkHash(sources[0]), TranslatedText: "摘要\n\n前半部分。", State: "complete", Attempts: 1, Provider: "openai-compatible", Model: "paper-model",
	}}

	markdown, _ := buildTranslationMarkdown(job, prefix)
	report := newTranslationEvidenceGate().Validate(job, prefix, markdown)
	if report.Complete {
		t.Fatalf("self-consistent prefix passed validation: %#v", report)
	}
	joined := strings.Join(report.Issues, " ")
	for _, want := range []string{"expects 2 chunks", "manifest mismatch", "final source chunk"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %#v", want, report.Issues)
		}
	}
}

func TestValidateTranslationEvidenceRecordsCompleteProvenance(t *testing.T) {
	source := "Conclusion"
	job := TranslationJob{
		Source:         PaperSource{Kind: "pdf", CanonicalURL: "https://example.com/paper.pdf"},
		SourceHash:     "artifact-hash",
		SourceManifest: buildTranslationSourceManifest("artifact-hash", []string{source}),
		Profile:        "paper-translate-v1",
		Provider:       "openai-compatible",
		Model:          "paper-model",
	}
	chunks := []TranslationChunk{{
		Ordinal: 0, SourceText: source, SourceHash: paperChunkHash(source), TranslatedText: "结论", State: "complete", Attempts: 1, Provider: "openai-compatible", Model: "paper-model",
	}}
	markdown, _ := buildTranslationMarkdown(job, chunks)
	report := newTranslationEvidenceGate().Validate(job, chunks, markdown)
	if !report.Complete || report.ArtifactHash != "artifact-hash" || report.Profile != job.Profile || report.Model != job.Model ||
		len(report.ChunkProvenance) != 1 || report.ChunkProvenance[0].Model != "paper-model" {
		t.Fatalf("report = %#v", report)
	}
}

func TestTranslationEvidenceGateRecordsMixedModelResume(t *testing.T) {
	sources := []string{"Abstract", "Conclusion"}
	job := TranslationJob{
		Source:         PaperSource{Kind: "pdf", CanonicalURL: "https://example.com/paper.pdf"},
		SourceHash:     "artifact-hash",
		SourceManifest: buildTranslationSourceManifest("artifact-hash", sources),
		Profile:        "paper-translate-v1", Provider: "openai-compatible", Model: "new-model",
	}
	chunks := []TranslationChunk{
		{Ordinal: 0, SourceText: sources[0], SourceHash: paperChunkHash(sources[0]), TranslatedText: "摘要", State: "complete", Attempts: 1, Provider: "openai-compatible", Model: "old-model"},
		{Ordinal: 1, SourceText: sources[1], SourceHash: paperChunkHash(sources[1]), TranslatedText: "结论", State: "complete", Attempts: 1, Provider: "openai-compatible", Model: "new-model"},
	}
	markdown, _ := buildTranslationMarkdown(job, chunks)
	report := newTranslationEvidenceGate().Validate(job, chunks, markdown)
	if !report.Complete || len(report.ChunkProvenance) != 2 ||
		report.ChunkProvenance[0].Model != "old-model" || report.ChunkProvenance[1].Model != "new-model" {
		t.Fatalf("report = %#v", report)
	}
}

func TestTranslationEvidenceGateRejectsUncheckpointedChunk(t *testing.T) {
	source := "Conclusion"
	job := TranslationJob{
		Source:         PaperSource{Kind: "pdf", CanonicalURL: "https://example.com/paper.pdf"},
		SourceHash:     "artifact-hash",
		SourceManifest: buildTranslationSourceManifest("artifact-hash", []string{source}),
		Profile:        "paper-translate-v1", Provider: "openai-compatible", Model: "paper-model",
	}
	chunks := []TranslationChunk{{
		Ordinal: 0, SourceText: source, SourceHash: paperChunkHash(source), TranslatedText: "结论",
		State: "pending", Attempts: 0,
	}}
	markdown, _ := buildTranslationMarkdown(job, chunks)
	report := newTranslationEvidenceGate().Validate(job, chunks, markdown)
	if report.Complete || len(report.InvalidChunks) != 1 || report.InvalidChunks[0] != 0 ||
		!strings.Contains(strings.Join(report.Issues, " "), "no completed translation checkpoint") {
		t.Fatalf("report = %#v", report)
	}
}

func TestTranslationEvidenceGateRejectsCompletedChunkWithoutProviderProvenance(t *testing.T) {
	source := "Conclusion"
	job := TranslationJob{
		Source:         PaperSource{Kind: "pdf", CanonicalURL: "https://example.com/paper.pdf"},
		SourceHash:     "artifact-hash",
		SourceManifest: buildTranslationSourceManifest("artifact-hash", []string{source}),
		Profile:        "paper-translate-v1", Provider: "openai-compatible", Model: "paper-model",
	}
	chunks := []TranslationChunk{{
		Ordinal: 0, SourceText: source, SourceHash: paperChunkHash(source), TranslatedText: "结论",
		State: "complete", Attempts: 1,
	}}
	markdown, _ := buildTranslationMarkdown(job, chunks)
	report := newTranslationEvidenceGate().Validate(job, chunks, markdown)
	if report.Complete || len(report.InvalidChunks) != 1 ||
		!strings.Contains(strings.Join(report.Issues, " "), "incomplete provider provenance") {
		t.Fatalf("report = %#v", report)
	}
}

func TestTranslationEvidenceGateRejectsMovingArxivSource(t *testing.T) {
	source := "Conclusion"
	job := TranslationJob{
		Source:         PaperSource{Kind: "arxiv", Identifier: "2401.01234", CanonicalURL: "https://arxiv.org/abs/2401.01234"},
		SourceHash:     "artifact-hash",
		SourceManifest: buildTranslationSourceManifest("artifact-hash", []string{source}),
		Profile:        "paper-translate-v1", Provider: "openai-compatible", Model: "paper-model",
	}
	chunks := []TranslationChunk{{
		Ordinal: 0, SourceText: source, SourceHash: paperChunkHash(source), TranslatedText: "结论",
		State: "complete", Attempts: 1, Provider: "openai-compatible", Model: "paper-model",
	}}
	markdown, _ := buildTranslationMarkdown(job, chunks)
	report := newTranslationEvidenceGate().Validate(job, chunks, markdown)
	if report.Complete || !strings.Contains(strings.Join(report.Issues, " "), "source revision provenance") {
		t.Fatalf("report = %#v", report)
	}
}

func TestTranslationEvidenceGateChecksAssembledFrontmatterProvenance(t *testing.T) {
	source := "Conclusion"
	job := TranslationJob{
		Source:         PaperSource{Kind: "pdf", CanonicalURL: "https://example.com/paper.pdf"},
		SourceHash:     "artifact-hash",
		SourceManifest: buildTranslationSourceManifest("artifact-hash", []string{source}),
		Profile:        "paper-translate-v1", Provider: "openai-compatible", Model: "paper-model",
	}
	chunks := []TranslationChunk{{
		Ordinal: 0, SourceText: source, SourceHash: paperChunkHash(source), TranslatedText: "结论",
		State: "complete", Attempts: 1, Provider: "openai-compatible", Model: "paper-model",
	}}
	markdown, _ := buildTranslationMarkdown(job, chunks)
	markdown = strings.Replace(markdown, "source_artifact_hash: \"artifact-hash\"", "source_artifact_hash: \"other-hash\"", 1)
	report := newTranslationEvidenceGate().Validate(job, chunks, markdown)
	if report.Complete || !strings.Contains(strings.Join(report.Issues, " "), "mismatched source_artifact_hash") {
		t.Fatalf("report = %#v", report)
	}
}

func TestValidateTranslationChunksRejectsTruncationAndLostStructure(t *testing.T) {
	source := strings.Repeat("method ", 80) + "as shown in [12]"
	report := validateTranslationChunks([]TranslationChunk{{
		Ordinal:        0,
		SourceText:     source,
		SourceHash:     paperChunkHash(source),
		TranslatedText: "简短总结```",
	}})
	if report.Complete || len(report.InvalidChunks) != 1 {
		t.Fatalf("report = %#v", report)
	}
	joined := strings.Join(report.Issues, " ")
	for _, want := range []string{"implausibly short", "lost citation marker [12]", "unclosed Markdown fence"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %#v", want, report.Issues)
		}
	}
}

func TestValidateTranslationChunksRejectsLostDocumentStructure(t *testing.T) {
	source := "## Methods\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n$$x=1$$\n\nClaim[^note]"
	report := validateTranslationChunks([]TranslationChunk{{
		Ordinal: 0, SourceText: source, SourceHash: paperChunkHash(source),
		TranslatedText: "# 方法\n\nA 和 B\n\nx 等于一\n\n结论",
	}})
	if report.Complete {
		t.Fatalf("report = %#v", report)
	}
	joined := strings.Join(report.Issues, " ")
	for _, want := range []string{"changed heading order", "lost table structure", "lost formula structure", "lost footnote marker"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %#v", want, report.Issues)
		}
	}
}

func TestTranslatePaperChunksUsesFullTranslationProfileInOrder(t *testing.T) {
	provider := &recordingLLMProvider{}
	job := TranslationJob{TargetLanguage: "zh-CN", Profile: "paper-translate-v1"}
	chunks, model, err := translatePaperChunks(context.Background(), provider, job, []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if model != "paper-model" || len(chunks) != 2 || len(provider.requests) != 2 {
		t.Fatalf("model=%q chunks=%#v requests=%#v", model, chunks, provider.requests)
	}
	if chunks[0].Ordinal != 0 || chunks[0].SourceText != "first" || chunks[1].SourceText != "second" {
		t.Fatalf("chunks = %#v", chunks)
	}
	if !strings.Contains(provider.requests[0].System, "不得摘要") ||
		!strings.Contains(provider.requests[0].System, "zh-CN") {
		t.Fatalf("system prompt = %q", provider.requests[0].System)
	}
}

func TestTranslatePaperChunkRejectsTruncatedProviderResponse(t *testing.T) {
	_, _, err := translatePaperChunk(context.Background(), truncatedLLMProvider{}, TranslationJob{
		TargetLanguage: "zh-CN",
		Profile:        "paper-translate-v1",
	}, "source", 0, 1)
	if err == nil || !strings.Contains(err.Error(), "incomplete provider response (length)") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateTranslationDraftSizeRejectsInternalOversizeDraft(t *testing.T) {
	source := "Conclusion"
	job := TranslationJob{
		Source:         PaperSource{Kind: "pdf", CanonicalURL: "https://example.com/paper.pdf"},
		SourceHash:     "artifact-hash",
		SourceManifest: buildTranslationSourceManifest("artifact-hash", []string{source}),
		Profile:        "paper-translate-v1",
		Provider:       "openai-compatible",
		Model:          "paper-model",
	}
	chunks := []TranslationChunk{{
		Ordinal: 0, SourceText: source, SourceHash: paperChunkHash(source), TranslatedText: "结论", State: "complete", Attempts: 1, Provider: "openai-compatible", Model: "paper-model",
	}}
	markdown, _ := buildTranslationMarkdown(job, chunks)
	gate := translationEvidenceGate{maxDocumentBytes: int64(len(markdown) - 1)}
	report := gate.Validate(job, chunks, markdown)
	if report.Complete || !strings.Contains(strings.Join(report.Issues, " "), "translation draft exceeds") {
		t.Fatalf("oversized draft passed validation: %#v", report)
	}
	gate.maxDocumentBytes = int64(len(markdown))
	if report := gate.Validate(job, chunks, markdown); !report.Complete {
		t.Fatalf("exact limit rejected: %#v", report)
	}
}

func TestBuildTranslationMarkdownCreatesPrivateProvenancedDraft(t *testing.T) {
	job := TranslationJob{
		ID: "job-abc123",
		Source: PaperSource{
			Kind:         "arxiv",
			Identifier:   "2401.01234",
			Version:      "v2",
			Title:        "A Useful Paper",
			CanonicalURL: "https://arxiv.org/abs/2401.01234v2",
		},
		TargetLanguage: "zh-CN",
		Profile:        "paper-translate-v1",
		Provider:       "openai-compatible",
		Model:          "paper-model",
	}
	markdown, slug := buildTranslationMarkdown(job, []TranslationChunk{
		{Ordinal: 0, TranslatedText: "## 摘要\n\n译文。"},
	})
	if slug != "_translations/arxiv-2401-01234-v2-zh-cn-job-abc123" {
		t.Fatalf("slug = %q", slug)
	}
	doc := parseDoc(slug, "", markdown)
	if doc.Published || doc.Source != "agent/translation" || doc.Title != "中文翻译：A Useful Paper" {
		t.Fatalf("doc = %#v", doc)
	}
	for _, required := range []string{
		"source_url: \"https://arxiv.org/abs/2401.01234v2\"",
		"translation_profile: \"paper-translate-v1\"",
		"translation_model: \"paper-model\"",
		"## 摘要",
	} {
		if !strings.Contains(markdown, required) {
			t.Fatalf("missing %q in:\n%s", required, markdown)
		}
	}
}

func TestTranslationOutputSlugSeparatesJobsAndLanguages(t *testing.T) {
	base := TranslationJob{
		ID:             "job-one",
		Source:         PaperSource{Kind: "pdf", ArtifactURL: "https://example.com/paper.pdf"},
		TargetLanguage: "zh-CN",
	}
	otherJob := base
	otherJob.ID = "job-two"
	otherLanguage := base
	otherLanguage.TargetLanguage = "ja-JP"
	if translationOutputSlug(base) == translationOutputSlug(otherJob) ||
		translationOutputSlug(base) == translationOutputSlug(otherLanguage) {
		t.Fatal("translation output slugs collided")
	}
}
