package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type resolvePaperSourceRequest struct {
	Source string `json:"source"`
}

type PaperSource struct {
	Input        string `json:"input"`
	Kind         string `json:"kind"`
	Identifier   string `json:"identifier,omitempty"`
	Version      string `json:"version,omitempty"`
	CanonicalURL string `json:"canonical_url"`
	ArtifactURL  string `json:"artifact_url"`
	Title        string `json:"title,omitempty"`
}

type TranslationSourceCapture struct {
	ID                 string        `json:"capture_id"`
	Source             PaperSource   `json:"source"`
	Status             string        `json:"status"`
	SizeBytes          int64         `json:"size_bytes"`
	SeriesKey          string        `json:"-"`
	RevisionKey        string        `json:"-"`
	ContentKey         string        `json:"-"`
	Artifact           paperArtifact `json:"-"`
	ExistingJobID      string        `json:"existing_job_id,omitempty"`
	ExistingOutputSlug string        `json:"existing_output_slug,omitempty"`
	PreviousJobID      string        `json:"previous_job_id,omitempty"`
	RevisionConflict   bool          `json:"revision_conflict,omitempty"`
}

type translationSourceCaptureService struct {
	client   *remoteSourceClient
	fetchPDF func(context.Context, *remoteSourceClient, PaperSource) (paperArtifact, error)
}

var sourceCaptureService = newTranslationSourceCaptureService()

var arxivIDPattern = regexp.MustCompile(`^([0-9]{4}\.[0-9]{4,5}|[a-z-]+(?:\.[A-Z]{2})?/[0-9]{7})(v[0-9]+)?$`)
var doiPattern = regexp.MustCompile(`(?i)^10\.[0-9]{4,9}/[-._;()/:A-Z0-9]+$`)

func newTranslationSourceCaptureService() *translationSourceCaptureService {
	return &translationSourceCaptureService{
		client:   newRemoteSourceClient(90 * time.Second),
		fetchPDF: fetchPaperPDF,
	}
}

func resolvePaperSource(input string) (PaperSource, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return PaperSource{}, fmt.Errorf("source is required")
	}
	if match := arxivIDPattern.FindStringSubmatch(raw); match != nil {
		return versionedArxivSource(raw, match)
	}
	if doiPattern.MatchString(raw) {
		return doiSource(raw, raw), nil
	}
	return resolvePaperSourceURL(raw)
}

func resolvePaperSourceURL(raw string) (PaperSource, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return PaperSource{}, fmt.Errorf("source must be an arXiv identifier or HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return PaperSource{}, fmt.Errorf("source URL must use http or https")
	}
	if parsed.User != nil {
		return PaperSource{}, fmt.Errorf("source URL must not contain credentials")
	}
	if source, ok := doiURLSource(raw, parsed); ok {
		return source, nil
	}
	if source, ok, err := arxivURLSource(raw, parsed); ok || err != nil {
		return source, err
	}
	canonical := parsed.String()
	kind := "web"
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".pdf") {
		kind = "pdf"
	}
	return PaperSource{
		Input:        raw,
		Kind:         kind,
		CanonicalURL: canonical,
		ArtifactURL:  canonical,
	}, nil
}

func versionedArxivSource(input string, match []string) (PaperSource, error) {
	if match[2] == "" {
		return PaperSource{}, fmt.Errorf("arXiv source must include an explicit version such as v1")
	}
	return arxivSource(input, match[1], match[2]), nil
}

func doiURLSource(input string, parsed *url.URL) (PaperSource, bool) {
	if !strings.EqualFold(parsed.Hostname(), "doi.org") {
		return PaperSource{}, false
	}
	decoded, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !doiPattern.MatchString(decoded) {
		return PaperSource{}, false
	}
	return doiSource(input, decoded), true
}

func arxivURLSource(input string, parsed *url.URL) (PaperSource, bool, error) {
	host := strings.ToLower(parsed.Hostname())
	if host != "arxiv.org" && host != "www.arxiv.org" {
		return PaperSource{}, false, nil
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) < 2 || (parts[0] != "abs" && parts[0] != "pdf") {
		return PaperSource{}, false, nil
	}
	id := strings.TrimSuffix(strings.Join(parts[1:], "/"), ".pdf")
	match := arxivIDPattern.FindStringSubmatch(id)
	if match == nil {
		return PaperSource{}, false, nil
	}
	source, err := versionedArxivSource(input, match)
	return source, true, err
}

func handleTranslationSourceResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTranslationRequestBytes)
	var input resolvePaperSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpError(w, fmt.Errorf("invalid body"), http.StatusBadRequest)
		return
	}
	capture, err := sourceCaptureService.Capture(r.Context(), input.Source)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := persistTranslationSourceCapture(capture); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if err := annotateExistingTranslation(capture, defaultTranslationLanguage, defaultTranslationProfile); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, capture)
}

func arxivSource(input, identifier, version string) PaperSource {
	versionedID := identifier + version
	return PaperSource{
		Input:        input,
		Kind:         "arxiv",
		Identifier:   identifier,
		Version:      version,
		CanonicalURL: "https://arxiv.org/abs/" + versionedID,
		ArtifactURL:  "https://arxiv.org/pdf/" + versionedID,
	}
}

func doiSource(input, identifier string) PaperSource {
	identifier = strings.ToLower(identifier)
	canonical := "https://doi.org/" + identifier
	return PaperSource{
		Input:        input,
		Kind:         "doi",
		Identifier:   identifier,
		CanonicalURL: canonical,
		ArtifactURL:  canonical,
	}
}

func (service *translationSourceCaptureService) Capture(ctx context.Context, input string) (*TranslationSourceCapture, error) {
	source, err := resolvePaperSource(input)
	if err != nil {
		return nil, err
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	capture := &TranslationSourceCapture{ID: id, Source: source, Status: "needs_input"}
	capture.SeriesKey, capture.RevisionKey = paperSourceKeys(source)
	artifact, err := service.fetchPDF(ctx, service.client, source)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return capture, nil
	}
	capture.Artifact = artifact
	capture.Status = "captured"
	capture.SizeBytes = int64(len(artifact.Data))
	capture.ContentKey = "sha256:" + artifact.Hash
	if source.Kind != "arxiv" {
		capture.RevisionKey = capture.ContentKey
		if source.Kind == "pdf" {
			capture.SeriesKey = capture.ContentKey
		}
	}
	return capture, nil
}

func paperSourceKeys(source PaperSource) (string, string) {
	switch source.Kind {
	case "arxiv":
		series := "arxiv:" + strings.ToLower(source.Identifier)
		return series, series + ":" + strings.ToLower(source.Version)
	case "doi":
		key := "doi:" + strings.ToLower(source.Identifier)
		return key, key
	default:
		hash := sha256.Sum256([]byte(source.CanonicalURL))
		key := fmt.Sprintf("url:%x", hash[:])
		return key, key
	}
}

func persistTranslationSourceCapture(capture *TranslationSourceCapture) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var artifactHash any
	if capture.Artifact.Hash != "" {
		if _, err := tx.Exec(`INSERT INTO translation_artifacts (hash, mime, data)
			VALUES ($1,$2,$3) ON CONFLICT (hash) DO NOTHING`, capture.Artifact.Hash, capture.Artifact.MIME, capture.Artifact.Data); err != nil {
			return err
		}
		artifactHash = capture.Artifact.Hash
	}
	_, err = tx.Exec(`INSERT INTO translation_source_captures
		(id, source_input, source_kind, canonical_url, artifact_url, source_identifier,
		 source_version, source_title, series_key, revision_key, content_key,
		 artifact_hash, status, size_bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		capture.ID, capture.Source.Input, capture.Source.Kind, capture.Source.CanonicalURL,
		capture.Source.ArtifactURL, capture.Source.Identifier, capture.Source.Version,
		capture.Source.Title, capture.SeriesKey, capture.RevisionKey, capture.ContentKey,
		artifactHash, capture.Status, capture.SizeBytes)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func annotateExistingTranslation(capture *TranslationSourceCapture, targetLanguage, profile string) error {
	var existingRevision, existingContent string
	err := db.QueryRow(`SELECT id, output_slug, source_revision_key, source_content_key FROM translation_jobs
		WHERE state <> 'cancelled' AND target_language=$1 AND profile=$2
		  AND (($3 <> '' AND source_revision_key=$3) OR ($4 <> '' AND source_content_key=$4))
		ORDER BY CASE WHEN state='published' THEN 0 ELSE 1 END, created_at DESC LIMIT 1`,
		targetLanguage, profile, capture.RevisionKey, capture.ContentKey,
	).Scan(&capture.ExistingJobID, &capture.ExistingOutputSlug, &existingRevision, &existingContent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	capture.RevisionConflict = existingRevision == capture.RevisionKey && existingContent != "" &&
		capture.ContentKey != "" && existingContent != capture.ContentKey
	if capture.ExistingJobID != "" || capture.SeriesKey == "" {
		return nil
	}
	err = db.QueryRow(`SELECT id FROM translation_jobs
		WHERE state <> 'cancelled' AND target_language=$1 AND profile=$2
		  AND source_series_key=$3 AND source_revision_key<>$4
		ORDER BY created_at DESC LIMIT 1`, targetLanguage, profile, capture.SeriesKey, capture.RevisionKey).
		Scan(&capture.PreviousJobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func sourceCaptureLockKeys(capture TranslationSourceCapture, targetLanguage, profile string) []string {
	keys := []string{}
	for _, identity := range []string{capture.RevisionKey, capture.ContentKey} {
		if identity != "" {
			keys = append(keys, targetLanguage+"\x00"+profile+"\x00"+identity)
		}
	}
	sort.Strings(keys)
	return uniqueStrings(keys)
}
