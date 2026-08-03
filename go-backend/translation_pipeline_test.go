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
	mock.ExpectExec("UPDATE translation_jobs").
		WithArgs("job-one", "worker-one").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := renewTranslationLease(context.Background(), "job-one", "worker-one"); !errors.Is(err, errTranslationLeaseLost) {
		t.Fatalf("error = %v", err)
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
