package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var translationModel = getEnv("MDHUB_TRANSLATION_MODEL", "")

var translationSlugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)
var citationMarkerPattern = regexp.MustCompile(`\[[0-9]{1,4}\]`)

type TranslationChunk struct {
	Ordinal        int    `json:"ordinal"`
	SourceText     string `json:"source_text"`
	SourceHash     string `json:"source_hash"`
	TranslatedText string `json:"translated_text"`
	State          string `json:"state"`
	Attempts       int    `json:"attempts"`
}

type TranslationValidationReport struct {
	Complete         bool     `json:"complete"`
	SourceChunks     int      `json:"source_chunks"`
	TranslatedChunks int      `json:"translated_chunks"`
	Issues           []string `json:"issues"`
	InvalidChunks    []int    `json:"invalid_chunks,omitempty"`
}

// chunkPaperText divides extracted paper text into bounded, ordered chunks
// without dropping or inserting characters. It prefers paragraph and page
// boundaries near the end of each chunk, then falls back to a hard rune cut.
func chunkPaperText(text string, maxRunes int) []string {
	if text == "" {
		return nil
	}
	if maxRunes < 1 {
		maxRunes = 1
	}
	remaining := []rune(text)
	chunks := make([]string, 0, (len(remaining)+maxRunes-1)/maxRunes)
	for len(remaining) > maxRunes {
		cut := maxRunes
		minimumPreferred := maxRunes / 2
		for i := maxRunes - 1; i >= minimumPreferred; i-- {
			if remaining[i] == '\f' {
				cut = i + 1
				break
			}
			if i > 0 && remaining[i-1] == '\n' && remaining[i] == '\n' {
				cut = i + 1
				break
			}
		}
		chunks = append(chunks, string(remaining[:cut]))
		remaining = remaining[cut:]
	}
	if len(remaining) > 0 {
		chunks = append(chunks, string(remaining))
	}
	return chunks
}

func validateTranslationChunks(chunks []TranslationChunk) TranslationValidationReport {
	report := TranslationValidationReport{
		SourceChunks:  len(chunks),
		Issues:        []string{},
		InvalidChunks: []int{},
	}
	omissionMarkers := []string{
		"其余内容略",
		"内容省略",
		"剩余内容省略",
		"remaining content omitted",
		"rest omitted",
	}
	for index, chunk := range chunks {
		invalid := false
		if chunk.Ordinal != index {
			report.Issues = append(report.Issues, fmt.Sprintf("chunk %d has ordinal %d", index, chunk.Ordinal))
			invalid = true
		}
		if strings.TrimSpace(chunk.SourceText) == "" || chunk.SourceHash != paperChunkHash(chunk.SourceText) {
			report.Issues = append(report.Issues, fmt.Sprintf("chunk %d source hash mismatch", index))
			invalid = true
		}
		translated := strings.TrimSpace(chunk.TranslatedText)
		if translated == "" {
			report.Issues = append(report.Issues, fmt.Sprintf("chunk %d has no translation", index))
			invalid = true
		} else {
			report.TranslatedChunks++
			lower := strings.ToLower(translated)
			for _, marker := range omissionMarkers {
				if strings.Contains(lower, marker) {
					report.Issues = append(report.Issues, fmt.Sprintf("chunk %d contains omission marker", index))
					invalid = true
					break
				}
			}
			sourceRunes := len([]rune(strings.TrimSpace(chunk.SourceText)))
			translatedRunes := len([]rune(translated))
			if sourceRunes >= 200 && translatedRunes < sourceRunes/6 {
				report.Issues = append(report.Issues, fmt.Sprintf("chunk %d translation is implausibly short", index))
				invalid = true
			}
			if sourceRunes >= 200 && translatedRunes > sourceRunes*4 {
				report.Issues = append(report.Issues, fmt.Sprintf("chunk %d translation is implausibly long", index))
				invalid = true
			}
			for _, marker := range uniqueStrings(citationMarkerPattern.FindAllString(chunk.SourceText, -1)) {
				if !strings.Contains(translated, marker) {
					report.Issues = append(report.Issues, fmt.Sprintf("chunk %d lost citation marker %s", index, marker))
					invalid = true
				}
			}
			if strings.Count(translated, "```")%2 != 0 {
				report.Issues = append(report.Issues, fmt.Sprintf("chunk %d has an unclosed Markdown fence", index))
				invalid = true
			}
		}
		if invalid {
			report.InvalidChunks = append(report.InvalidChunks, chunk.Ordinal)
		}
	}
	report.Complete = len(chunks) > 0 && len(report.Issues) == 0 && report.TranslatedChunks == report.SourceChunks
	return report
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func translatePaperChunks(ctx context.Context, provider LLMProvider, job TranslationJob, sourceChunks []string) ([]TranslationChunk, string, error) {
	if provider == nil {
		return nil, "", fmt.Errorf("translation provider is required")
	}
	chunks := make([]TranslationChunk, 0, len(sourceChunks))
	usedModel := translationModel
	for ordinal, source := range sourceChunks {
		translated, model, err := translatePaperChunk(ctx, provider, job, source, ordinal, len(sourceChunks))
		if err != nil {
			return chunks, usedModel, err
		}
		if model != "" {
			usedModel = model
		}
		chunks = append(chunks, TranslationChunk{
			Ordinal:        ordinal,
			SourceText:     source,
			SourceHash:     paperChunkHash(source),
			TranslatedText: translated,
			State:          "complete",
			Attempts:       1,
		})
	}
	return chunks, usedModel, nil
}

func paperChunkHash(source string) string {
	hash := sha256.Sum256([]byte(source))
	return fmt.Sprintf("%x", hash[:])
}

func buildTranslationMarkdown(job TranslationJob, chunks []TranslationChunk) (string, string) {
	sourceTitle := strings.TrimSpace(job.Source.Title)
	if sourceTitle == "" {
		sourceTitle = strings.TrimSpace(job.Source.Identifier)
	}
	if sourceTitle == "" {
		sourceTitle = "论文"
	}
	title := "中文翻译：" + sourceTitle
	slug := translationOutputSlug(job)

	var body strings.Builder
	body.WriteString("---\n")
	body.WriteString("title: " + yamlQuote(title) + "\n")
	body.WriteString("publish: false\n")
	body.WriteString("source: agent/translation\n")
	body.WriteString("tags: [translation, paper]\n")
	body.WriteString("source_url: " + yamlQuote(job.Source.CanonicalURL) + "\n")
	body.WriteString("source_kind: " + yamlQuote(job.Source.Kind) + "\n")
	body.WriteString("source_version: " + yamlQuote(job.Source.Version) + "\n")
	body.WriteString("translation_language: " + yamlQuote(job.TargetLanguage) + "\n")
	body.WriteString("translation_profile: " + yamlQuote(job.Profile) + "\n")
	body.WriteString("translation_provider: " + yamlQuote(job.Provider) + "\n")
	body.WriteString("translation_model: " + yamlQuote(job.Model) + "\n")
	body.WriteString("---\n\n")
	body.WriteString("# " + title + "\n\n")
	if job.Source.CanonicalURL != "" {
		body.WriteString("> 原文：[" + sourceTitle + "](" + job.Source.CanonicalURL + ")\n\n")
	}
	for index, chunk := range chunks {
		if index > 0 {
			body.WriteString("\n\n")
		}
		body.WriteString(strings.TrimSpace(chunk.TranslatedText))
	}
	body.WriteString("\n")
	return body.String(), slug
}

func translationOutputSlug(job TranslationJob) string {
	base := ""
	if job.Source.Kind == "arxiv" && job.Source.Identifier != "" {
		base = "arxiv-" + job.Source.Identifier
		if job.Source.Version != "" {
			base += "-" + job.Source.Version
		}
	} else if parsed, err := url.Parse(job.Source.ArtifactURL); err == nil {
		base = strings.TrimSuffix(path.Base(parsed.Path), path.Ext(parsed.Path))
	}
	base = slugComponent(base)
	if base == "" {
		base = "paper"
	}
	language := slugComponent(job.TargetLanguage)
	if language == "" {
		language = "translation"
	}
	identity := slugComponent(job.ID)
	if identity == "" {
		hash := sha256.Sum256([]byte(job.Source.CanonicalURL + "\x00" + job.Profile + "\x00" + job.TargetLanguage))
		identity = fmt.Sprintf("%x", hash[:6])
	}
	identity = truncateRunes(identity, 16)
	suffix := "-" + language + "-" + identity
	base = truncateRunes(base, 120-len([]rune(suffix)))
	return "_translations/" + base + suffix
}

func slugComponent(value string) string {
	value = strings.ToLower(value)
	value = translationSlugUnsafe.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}
