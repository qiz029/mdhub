package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var translationModel = getEnv("MDHUB_TRANSLATION_MODEL", "")

var translationSlugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)
var citationMarkerPattern = regexp.MustCompile(`\[[0-9]{1,4}\]`)
var markdownHeadingPattern = regexp.MustCompile(`(?m)^(#{1,6})[\t ]+\S`)
var footnoteMarkerPattern = regexp.MustCompile(`\[\^[^\]]+\]`)

type TranslationChunk struct {
	Ordinal        int    `json:"ordinal"`
	SourceText     string `json:"source_text"`
	SourceHash     string `json:"source_hash"`
	TranslatedText string `json:"translated_text"`
	State          string `json:"state"`
	Attempts       int    `json:"attempts"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
}

type TranslationChunkProvenance struct {
	Ordinal  int    `json:"ordinal"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type TranslationValidationReport struct {
	Complete         bool                         `json:"complete"`
	SourceChunks     int                          `json:"source_chunks"`
	TranslatedChunks int                          `json:"translated_chunks"`
	Issues           []string                     `json:"issues"`
	InvalidChunks    []int                        `json:"invalid_chunks,omitempty"`
	ArtifactHash     string                       `json:"artifact_hash"`
	ManifestHash     string                       `json:"manifest_hash"`
	FinalChunkHash   string                       `json:"final_chunk_hash"`
	Profile          string                       `json:"profile"`
	Provider         string                       `json:"provider"`
	Model            string                       `json:"model"`
	SourceURL        string                       `json:"source_url"`
	SourceVersion    string                       `json:"source_version"`
	ChunkProvenance  []TranslationChunkProvenance `json:"chunk_provenance"`
}

type TranslationSourceManifest struct {
	ArtifactHash   string `json:"artifact_hash"`
	ChunkCount     int    `json:"chunk_count"`
	ManifestHash   string `json:"manifest_hash"`
	FinalChunkHash string `json:"final_chunk_hash"`
}

type translationEvidenceGate struct {
	maxDocumentBytes int64
}

func newTranslationEvidenceGate() translationEvidenceGate {
	return translationEvidenceGate{maxDocumentBytes: maxDocumentBytes}
}

func buildTranslationSourceManifest(artifactHash string, sourceChunks []string) TranslationSourceManifest {
	hasher := sha256.New()
	manifest := TranslationSourceManifest{ArtifactHash: artifactHash, ChunkCount: len(sourceChunks)}
	for ordinal, source := range sourceChunks {
		hash := paperChunkHash(source)
		fmt.Fprintf(hasher, "%d:%s\n", ordinal, hash)
		manifest.FinalChunkHash = hash
	}
	manifest.ManifestHash = fmt.Sprintf("%x", hasher.Sum(nil))
	return manifest
}

func persistedTranslationManifest(chunks []TranslationChunk) string {
	hasher := sha256.New()
	for _, chunk := range chunks {
		fmt.Fprintf(hasher, "%d:%s\n", chunk.Ordinal, chunk.SourceHash)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func (gate translationEvidenceGate) Validate(job TranslationJob, chunks []TranslationChunk, markdown string) TranslationValidationReport {
	report := validateTranslationChunks(chunks)
	report.ArtifactHash = job.SourceManifest.ArtifactHash
	report.ManifestHash = job.SourceManifest.ManifestHash
	report.FinalChunkHash = job.SourceManifest.FinalChunkHash
	report.Profile = job.Profile
	report.Provider = job.Provider
	report.Model = job.Model
	report.SourceURL = job.Source.CanonicalURL
	report.SourceVersion = job.Source.Version
	report.ChunkProvenance = translationChunkProvenance(chunks)
	report.Issues = append(report.Issues, translationProvenanceIssues(job)...)
	report.Issues = append(report.Issues, translationManifestIssues(job.SourceManifest, chunks)...)
	checkpointIssues, checkpointInvalid := translationCheckpointIssues(chunks)
	report.Issues = append(report.Issues, checkpointIssues...)
	report.InvalidChunks = mergeInvalidChunks(report.InvalidChunks, checkpointInvalid)
	report.Issues = append(report.Issues, gate.draftIssues(job, markdown)...)
	report.Complete = report.Complete && len(report.Issues) == 0
	return report
}

func translationProvenanceIssues(job TranslationJob) []string {
	issues := []string{}
	if job.SourceHash == "" || job.SourceManifest.ArtifactHash == "" || job.SourceHash != job.SourceManifest.ArtifactHash {
		issues = append(issues, "source artifact provenance mismatch")
	}
	if job.Profile == "" || job.Provider == "" || job.Model == "" {
		issues = append(issues, "translation provenance is incomplete")
	}
	if job.Source.CanonicalURL == "" || (job.Source.Kind == "arxiv" && job.Source.Version == "") {
		issues = append(issues, "source revision provenance is incomplete")
	}
	return issues
}

func translationManifestIssues(manifest TranslationSourceManifest, chunks []TranslationChunk) []string {
	issues := []string{}
	if manifest.ChunkCount != len(chunks) {
		issues = append(issues, fmt.Sprintf("source manifest expects %d chunks, found %d", manifest.ChunkCount, len(chunks)))
	}
	if manifest.ManifestHash == "" || persistedTranslationManifest(chunks) != manifest.ManifestHash {
		issues = append(issues, "source chunk manifest mismatch")
	}
	if len(chunks) == 0 || manifest.FinalChunkHash == "" || chunks[len(chunks)-1].SourceHash != manifest.FinalChunkHash {
		issues = append(issues, "final source chunk is missing")
	}
	return issues
}

func translationCheckpointIssues(chunks []TranslationChunk) ([]string, []int) {
	issues := []string{}
	invalid := []int{}
	for _, chunk := range chunks {
		valid := true
		if chunk.State != "complete" || chunk.Attempts < 1 {
			issues = append(issues, fmt.Sprintf("chunk %d has no completed translation checkpoint", chunk.Ordinal))
			valid = false
		}
		if chunk.Provider == "" || chunk.Model == "" {
			issues = append(issues, fmt.Sprintf("chunk %d has incomplete provider provenance", chunk.Ordinal))
			valid = false
		}
		if !valid {
			invalid = append(invalid, chunk.Ordinal)
		}
	}
	return issues, invalid
}

func mergeInvalidChunks(existing, additional []int) []int {
	seen := make(map[int]struct{}, len(existing)+len(additional))
	merged := make([]int, 0, len(existing)+len(additional))
	for _, values := range [][]int{existing, additional} {
		for _, ordinal := range values {
			if _, ok := seen[ordinal]; ok {
				continue
			}
			seen[ordinal] = struct{}{}
			merged = append(merged, ordinal)
		}
	}
	return merged
}

func translationChunkProvenance(chunks []TranslationChunk) []TranslationChunkProvenance {
	provenance := make([]TranslationChunkProvenance, 0, len(chunks))
	for _, chunk := range chunks {
		provenance = append(provenance, TranslationChunkProvenance{
			Ordinal: chunk.Ordinal, Provider: chunk.Provider, Model: chunk.Model,
		})
	}
	return provenance
}

func (gate translationEvidenceGate) draftIssues(job TranslationJob, markdown string) []string {
	issues := []string{}
	if int64(len(markdown)) > gate.maxDocumentBytes {
		issues = append(issues, fmt.Sprintf("translation draft exceeds %d bytes", gate.maxDocumentBytes))
	}
	frontmatter, body := splitFrontmatter(markdown)
	draft := parseDoc("translation-validation", "", markdown)
	if frontmatter == "" || strings.TrimSpace(body) == "" || draft.Published || draft.Source != "agent/translation" {
		issues = append(issues, "assembled Markdown is not a private translation draft")
	}
	expected := []struct{ key, value string }{
		{"source_url", job.Source.CanonicalURL},
		{"source_version", job.Source.Version},
		{"source_artifact_hash", job.SourceHash},
		{"translation_profile", job.Profile},
		{"translation_provider", job.Provider},
		{"translation_model", job.Model},
	}
	for _, field := range expected {
		if actual, ok := translationFrontmatterValue(frontmatter, field.key); !ok || actual != field.value {
			issues = append(issues, fmt.Sprintf("assembled Markdown has mismatched %s", field.key))
		}
	}
	return issues
}

func translationFrontmatterValue(frontmatter, key string) (string, bool) {
	for _, line := range strings.Split(frontmatter, "\n") {
		if value, ok := frontmatterValue(strings.TrimSpace(line), key); ok {
			value = strings.TrimSpace(value)
			if unquoted, err := strconv.Unquote(value); err == nil {
				return unquoted, true
			}
			return strings.Trim(value, "'"), true
		}
	}
	return "", false
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
	for index, chunk := range chunks {
		translated, issues := validateTranslationChunk(index, chunk)
		if translated {
			report.TranslatedChunks++
		}
		report.Issues = append(report.Issues, issues...)
		if len(issues) > 0 {
			report.InvalidChunks = append(report.InvalidChunks, chunk.Ordinal)
		}
	}
	report.Complete = len(chunks) > 0 && len(report.Issues) == 0 && report.TranslatedChunks == report.SourceChunks
	return report
}

func validateTranslationChunk(index int, chunk TranslationChunk) (bool, []string) {
	issues := []string{}
	if chunk.Ordinal != index {
		issues = append(issues, fmt.Sprintf("chunk %d has ordinal %d", index, chunk.Ordinal))
	}
	if strings.TrimSpace(chunk.SourceText) == "" || chunk.SourceHash != paperChunkHash(chunk.SourceText) {
		issues = append(issues, fmt.Sprintf("chunk %d source hash mismatch", index))
	}
	translated := strings.TrimSpace(chunk.TranslatedText)
	if translated == "" {
		return false, append(issues, fmt.Sprintf("chunk %d has no translation", index))
	}
	issues = append(issues, translationTextIssues(index, chunk.SourceText, translated)...)
	issues = append(issues, translationStructureIssues(index, chunk.SourceText, translated)...)
	return true, issues
}

func translationTextIssues(index int, source, translated string) []string {
	issues := []string{}
	lower := strings.ToLower(translated)
	for _, marker := range []string{"其余内容略", "内容省略", "剩余内容省略", "remaining content omitted", "rest omitted"} {
		if strings.Contains(lower, marker) {
			issues = append(issues, fmt.Sprintf("chunk %d contains omission marker", index))
			break
		}
	}
	sourceRunes, translatedRunes := len([]rune(strings.TrimSpace(source))), len([]rune(translated))
	if sourceRunes >= 200 && translatedRunes < sourceRunes/6 {
		issues = append(issues, fmt.Sprintf("chunk %d translation is implausibly short", index))
	}
	if sourceRunes >= 200 && translatedRunes > sourceRunes*4 {
		issues = append(issues, fmt.Sprintf("chunk %d translation is implausibly long", index))
	}
	for _, marker := range uniqueStrings(citationMarkerPattern.FindAllString(source, -1)) {
		if !strings.Contains(translated, marker) {
			issues = append(issues, fmt.Sprintf("chunk %d lost citation marker %s", index, marker))
		}
	}
	if strings.Count(translated, "```")%2 != 0 {
		issues = append(issues, fmt.Sprintf("chunk %d has an unclosed Markdown fence", index))
	}
	return issues
}

func translationStructureIssues(index int, source, translated string) []string {
	issues := []string{}
	if !equalInts(markdownHeadingLevels(source), markdownHeadingLevels(translated)) {
		issues = append(issues, fmt.Sprintf("chunk %d changed heading order", index))
	}
	if sourceTables := markdownTableLines(source); sourceTables > 0 && markdownTableLines(translated) < sourceTables {
		issues = append(issues, fmt.Sprintf("chunk %d lost table structure", index))
	}
	for _, marker := range uniqueStrings(footnoteMarkerPattern.FindAllString(source, -1)) {
		if !strings.Contains(translated, marker) {
			issues = append(issues, fmt.Sprintf("chunk %d lost footnote marker %s", index, marker))
		}
	}
	if sourceMath := explicitMathMarkers(source); sourceMath > 0 && explicitMathMarkers(translated) < sourceMath {
		issues = append(issues, fmt.Sprintf("chunk %d lost formula structure", index))
	}
	return issues
}

func markdownHeadingLevels(text string) []int {
	matches := markdownHeadingPattern.FindAllStringSubmatch(text, -1)
	levels := make([]int, 0, len(matches))
	for _, match := range matches {
		levels = append(levels, len(match[1]))
	}
	return levels
}

func markdownTableLines(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.Count(line, "|") >= 2 {
			count++
		}
	}
	return count
}

func explicitMathMarkers(text string) int {
	return strings.Count(text, "$$") + strings.Count(text, `\[`) + strings.Count(text, `\]`)
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
		chunkModel := model
		if chunkModel == "" {
			chunkModel = usedModel
		}
		chunks = append(chunks, TranslationChunk{
			Ordinal:        ordinal,
			SourceText:     source,
			SourceHash:     paperChunkHash(source),
			TranslatedText: translated,
			State:          "complete",
			Attempts:       1,
			Provider:       "openai-compatible",
			Model:          chunkModel,
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
	body.WriteString("source_artifact_hash: " + yamlQuote(job.SourceHash) + "\n")
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
