package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTranslationLanguage = "zh-CN"
	defaultTranslationProfile  = "paper-translate-v1"
	maxTranslationRequestBytes = 32 << 10
)

type translationJobState string

const (
	translationQueued      translationJobState = "queued"
	translationClaimed     translationJobState = "claimed"
	translationFetching    translationJobState = "fetching"
	translationExtracting  translationJobState = "extracting"
	translationTranslating translationJobState = "translating"
	translationValidating  translationJobState = "validating"
)

var errTranslationStateConflict = errors.New("translation job state does not allow this action")
var errTranslationDuplicateSource = errors.New("an active translation already uses this PDF")
var errTranslationRevisionConflict = errors.New("source revision content changed")
var errUnknownTranslationAction = errors.New("unknown translation action")

type TranslationJob struct {
	ID                string                    `json:"id"`
	Source            PaperSource               `json:"source"`
	TargetLanguage    string                    `json:"target_language"`
	Profile           string                    `json:"profile"`
	State             string                    `json:"state"`
	Stage             string                    `json:"stage"`
	ProgressCurrent   int                       `json:"progress_current"`
	ProgressTotal     int                       `json:"progress_total"`
	OutputSlug        string                    `json:"output_slug,omitempty"`
	Provider          string                    `json:"provider,omitempty"`
	Model             string                    `json:"model,omitempty"`
	SourceHash        string                    `json:"source_hash,omitempty"`
	SourceManifest    TranslationSourceManifest `json:"source_manifest,omitempty"`
	SourceSeriesKey   string                    `json:"-"`
	SourceRevisionKey string                    `json:"-"`
	SourceContentKey  string                    `json:"-"`
	Validation        json.RawMessage           `json:"validation,omitempty"`
	Error             string                    `json:"error,omitempty"`
	CreatedAt         int64                     `json:"created_at"`
	UpdatedAt         int64                     `json:"updated_at"`
}

type createTranslationJobRequest struct {
	CaptureID      string `json:"capture_id"`
	TargetLanguage string `json:"target_language"`
	Profile        string `json:"profile"`
}

func handleTranslationJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		createTranslationJob(w, r)
	case http.MethodGet:
		listTranslationJobs(w, r)
	default:
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
	}
}

func handleTranslationJob(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/translation-jobs/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || !validTranslationJobID(parts[0]) {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
			return
		}
		getTranslationJob(w, id)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	if parts[1] == "source" {
		handleTranslationSourceUpload(w, r, id)
		return
	}
	err := applyTranslationJobAction(id, parts[1])
	if errors.Is(err, errUnknownTranslationAction) {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	writeTranslationJobActionResult(w, id, err)
}

func getTranslationJob(w http.ResponseWriter, id string) {
	job, err := queryTranslationJob(id)
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	chunks, err := loadTranslationChunks(id)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		TranslationJob
		Chunks []TranslationChunk `json:"chunks"`
	}{TranslationJob: job, Chunks: chunks})
}

func applyTranslationJobAction(id, action string) error {
	switch action {
	case "cancel":
		return cancelTranslationJob(id)
	case "retry":
		return retryTranslationJob(id)
	case "publish":
		return publishTranslationJob(id)
	default:
		return errUnknownTranslationAction
	}
}

func writeTranslationJobActionResult(w http.ResponseWriter, id string, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	if errors.Is(err, errTranslationStateConflict) {
		httpError(w, err, http.StatusConflict)
		return
	}
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	job, err := queryTranslationJob(id)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, job)
}

func createTranslationJob(w http.ResponseWriter, r *http.Request) {
	var input createTranslationJobRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxTranslationRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		httpError(w, fmt.Errorf("invalid body"), http.StatusBadRequest)
		return
	}
	if !validTranslationJobID(input.CaptureID) {
		httpError(w, fmt.Errorf("valid capture_id is required"), http.StatusBadRequest)
		return
	}
	targetLanguage := strings.TrimSpace(input.TargetLanguage)
	if targetLanguage == "" {
		targetLanguage = defaultTranslationLanguage
	}
	profile := strings.TrimSpace(input.Profile)
	if profile == "" {
		profile = defaultTranslationProfile
	}
	if len([]rune(targetLanguage)) > 32 || len([]rune(profile)) > 80 {
		httpError(w, fmt.Errorf("translation options too long"), http.StatusBadRequest)
		return
	}
	job, created, err := createTranslationJobFromCapture(input.CaptureID, targetLanguage, profile)
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, fmt.Errorf("source capture not found"), http.StatusNotFound)
		return
	}
	if errors.Is(err, errTranslationRevisionConflict) {
		httpError(w, err, http.StatusConflict)
		return
	}
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSONStatus(w, status, job)
}

func createTranslationJobFromCapture(captureID, targetLanguage, profile string) (TranslationJob, bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return TranslationJob{}, false, err
	}
	defer tx.Rollback()
	capture, err := loadTranslationSourceCapture(tx.QueryRow(`SELECT
		id, source_input, source_kind, canonical_url, artifact_url, source_identifier,
		source_version, source_title, series_key, revision_key, content_key,
		COALESCE(artifact_hash, ''), status, size_bytes
		FROM translation_source_captures WHERE id=$1 AND expires_at > now()`, captureID))
	if err != nil {
		return TranslationJob{}, false, err
	}
	if capture.Status == "captured" && capture.Artifact.Hash == "" {
		return TranslationJob{}, false, fmt.Errorf("captured source has no artifact")
	}
	if err := lockTranslationSourceCapture(tx, capture, targetLanguage, profile); err != nil {
		return TranslationJob{}, false, err
	}
	existing, err := findExistingTranslationJob(tx, capture, targetLanguage, profile)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return TranslationJob{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TranslationJob{}, false, err
	}
	job, err := insertCapturedTranslationJob(tx, capture, targetLanguage, profile)
	if err != nil {
		return TranslationJob{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return TranslationJob{}, false, err
	}
	return job, true, nil
}

func lockTranslationSourceCapture(tx *sql.Tx, capture TranslationSourceCapture, targetLanguage, profile string) error {
	for _, key := range sourceCaptureLockKeys(capture, targetLanguage, profile) {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return err
		}
	}
	return nil
}

func findExistingTranslationJob(tx *sql.Tx, capture TranslationSourceCapture, targetLanguage, profile string) (TranslationJob, error) {
	existing, err := scanTranslationJob(tx.QueryRow("SELECT "+translationJobColumns+` FROM translation_jobs
		WHERE state <> 'cancelled' AND target_language=$1 AND profile=$2
		  AND (($3 <> '' AND source_revision_key=$3) OR ($4 <> '' AND source_content_key=$4))
		ORDER BY CASE WHEN state='published' THEN 0 ELSE 1 END, created_at DESC LIMIT 1`,
		targetLanguage, profile, capture.RevisionKey, capture.ContentKey))
	if err != nil {
		return TranslationJob{}, err
	}
	if existing.SourceRevisionKey == capture.RevisionKey &&
		existing.SourceContentKey != "" && capture.ContentKey != "" &&
		existing.SourceContentKey != capture.ContentKey {
		return TranslationJob{}, fmt.Errorf("%w: %s", errTranslationRevisionConflict, existing.ID)
	}
	return existing, nil
}

func insertCapturedTranslationJob(tx *sql.Tx, capture TranslationSourceCapture, targetLanguage, profile string) (TranslationJob, error) {
	id, err := randomID()
	if err != nil {
		return TranslationJob{}, err
	}
	state := "queued"
	if capture.Status == "needs_input" {
		state = "needs_input"
	}
	job := TranslationJob{
		ID: id, Source: capture.Source, SourceHash: capture.Artifact.Hash,
		TargetLanguage: targetLanguage, Profile: profile, State: state, Stage: state,
		SourceSeriesKey: capture.SeriesKey, SourceRevisionKey: capture.RevisionKey, SourceContentKey: capture.ContentKey,
	}
	var artifactHash any
	if capture.Artifact.Hash != "" {
		artifactHash = capture.Artifact.Hash
	}
	var createdAt, updatedAt time.Time
	err = tx.QueryRow(`INSERT INTO translation_jobs
		(id, source_input, source_kind, canonical_url, artifact_url,
		 source_identifier, source_version, source_title, source_hash, source_artifact_hash,
		 source_capture_id, source_series_key, source_revision_key, source_content_key,
		 target_language, profile, state, stage)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)
		RETURNING created_at, updated_at`,
		job.ID, capture.Source.Input, capture.Source.Kind, capture.Source.CanonicalURL,
		capture.Source.ArtifactURL, capture.Source.Identifier, capture.Source.Version,
		capture.Source.Title, capture.Artifact.Hash, artifactHash, capture.ID,
		capture.SeriesKey, capture.RevisionKey, capture.ContentKey, targetLanguage, profile, state,
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		return TranslationJob{}, err
	}
	job.CreatedAt = createdAt.UnixMilli()
	job.UpdatedAt = updatedAt.UnixMilli()
	return job, nil
}

func loadTranslationSourceCapture(row rowScanner) (TranslationSourceCapture, error) {
	var capture TranslationSourceCapture
	err := row.Scan(
		&capture.ID, &capture.Source.Input, &capture.Source.Kind, &capture.Source.CanonicalURL,
		&capture.Source.ArtifactURL, &capture.Source.Identifier, &capture.Source.Version,
		&capture.Source.Title, &capture.SeriesKey, &capture.RevisionKey, &capture.ContentKey,
		&capture.Artifact.Hash, &capture.Status, &capture.SizeBytes,
	)
	return capture, err
}

func listTranslationJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := queryTranslationJobs("")
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, jobs)
}

const translationJobColumns = `
	id, source_input, source_kind, canonical_url, artifact_url,
	source_identifier, source_version, source_title,
	source_hash, source_manifest, source_series_key, source_revision_key, source_content_key,
	target_language, profile, state, stage, progress_current, progress_total,
	output_slug, provider, model, validation_report, error_summary,
	created_at, updated_at`

type rowScanner interface {
	Scan(...any) error
}

func scanTranslationJob(row rowScanner) (TranslationJob, error) {
	var job TranslationJob
	var validation []byte
	var manifest []byte
	var createdAt, updatedAt time.Time
	err := row.Scan(
		&job.ID, &job.Source.Input, &job.Source.Kind, &job.Source.CanonicalURL,
		&job.Source.ArtifactURL, &job.Source.Identifier, &job.Source.Version,
		&job.Source.Title, &job.SourceHash, &manifest, &job.SourceSeriesKey, &job.SourceRevisionKey,
		&job.SourceContentKey, &job.TargetLanguage, &job.Profile, &job.State, &job.Stage,
		&job.ProgressCurrent, &job.ProgressTotal, &job.OutputSlug, &job.Provider,
		&job.Model, &validation, &job.Error, &createdAt, &updatedAt,
	)
	if err != nil {
		return TranslationJob{}, err
	}
	if len(validation) > 0 {
		job.Validation = append(json.RawMessage(nil), validation...)
	}
	if len(manifest) > 0 {
		if err := json.Unmarshal(manifest, &job.SourceManifest); err != nil {
			return TranslationJob{}, fmt.Errorf("decode source manifest: %w", err)
		}
	}
	job.CreatedAt = createdAt.UnixMilli()
	job.UpdatedAt = updatedAt.UnixMilli()
	return job, nil
}

func queryTranslationJobs(id string) ([]TranslationJob, error) {
	query := "SELECT " + translationJobColumns + " FROM translation_jobs"
	var (
		rows *sql.Rows
		err  error
	)
	if id == "" {
		rows, err = db.Query(query + " ORDER BY created_at DESC LIMIT 100")
	} else {
		rows, err = db.Query(query+" WHERE id=$1", id)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []TranslationJob{}
	for rows.Next() {
		job, err := scanTranslationJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func queryTranslationJob(id string) (TranslationJob, error) {
	return scanTranslationJob(db.QueryRow("SELECT "+translationJobColumns+" FROM translation_jobs WHERE id=$1", id))
}

func validTranslationJobID(id string) bool {
	if id == "" || len(id) > 80 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

func cancelTranslationJob(id string) error {
	result, err := db.Exec(`UPDATE translation_jobs
		SET state='cancelled', stage='cancelled', lease_owner='', lease_until=NULL, updated_at=now()
		WHERE id=$1 AND state IN ('queued','claimed','fetching','extracting','translating','validating','failed','needs_input')`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return translationActionMiss(id)
	}
	return nil
}

func retryTranslationJob(id string) error {
	result, err := db.Exec(`UPDATE translation_jobs
		SET state='queued', stage='queued', error_summary='', lease_owner='', lease_until=NULL, updated_at=now()
		WHERE id=$1 AND state='failed'`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return translationActionMiss(id)
	}
	return nil
}

func handleTranslationSourceUpload(w http.ResponseWriter, r *http.Request, id string) {
	artifact, status, err := parseTranslationPDFUpload(w, r)
	if err != nil {
		httpError(w, err, status)
		return
	}
	if err := attachTranslationPDF(id, artifact); errors.Is(err, sql.ErrNoRows) {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	} else if errors.Is(err, errTranslationStateConflict) {
		httpError(w, err, http.StatusConflict)
		return
	} else if errors.Is(err, errTranslationDuplicateSource) {
		httpError(w, err, http.StatusConflict)
		return
	} else if errors.Is(err, errTranslationRevisionConflict) {
		httpError(w, err, http.StatusConflict)
		return
	} else if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	job, err := queryTranslationJob(id)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, job)
}

func parseTranslationPDFUpload(w http.ResponseWriter, r *http.Request) (paperArtifact, int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTranslationPDFBytes+(1<<20))
	if err := r.ParseMultipartForm(maxTranslationPDFBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return paperArtifact{}, http.StatusRequestEntityTooLarge, fmt.Errorf("PDF upload exceeds 50 MB")
		}
		return paperArtifact{}, http.StatusBadRequest, fmt.Errorf("invalid multipart upload")
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return paperArtifact{}, http.StatusBadRequest, fmt.Errorf("missing PDF file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTranslationPDFBytes+1))
	if err != nil {
		return paperArtifact{}, http.StatusBadRequest, fmt.Errorf("read PDF: %w", err)
	}
	artifact, err := paperArtifactFromPDF(data)
	if err != nil {
		if int64(len(data)) > maxTranslationPDFBytes {
			return paperArtifact{}, http.StatusRequestEntityTooLarge, fmt.Errorf("PDF upload exceeds 50 MB")
		}
		return paperArtifact{}, http.StatusUnsupportedMediaType, err
	}
	return artifact, http.StatusOK, nil
}

// attachTranslationPDF is the needs_input recovery interface. The artifact,
// cleared checkpoints, and queued job state commit as one transition.
func attachTranslationPDF(id string, artifact paperArtifact) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	target, err := loadTranslationAttachmentTarget(tx, id)
	if err != nil {
		return err
	}
	if target.state != "needs_input" {
		return errTranslationStateConflict
	}
	if _, err := tx.Exec(`INSERT INTO translation_artifacts (hash, mime, data)
		VALUES ($1,$2,$3) ON CONFLICT (hash) DO NOTHING`, artifact.Hash, artifact.MIME, artifact.Data); err != nil {
		return err
	}
	contentKey := "sha256:" + artifact.Hash
	if target.sourceKind != "arxiv" {
		target.revisionKey = contentKey
	}
	if err := rejectDuplicateTranslationArtifact(tx, id, target, contentKey); err != nil {
		return err
	}
	if err := promoteTranslationSourceCapture(tx, target.captureID, artifact, contentKey, target.revisionKey); err != nil {
		return err
	}
	if err := queueAttachedTranslation(tx, id, artifact.Hash, target.revisionKey, contentKey); err != nil {
		return err
	}
	return tx.Commit()
}

type translationAttachmentTarget struct {
	state, targetLanguage, profile, sourceKind, revisionKey string
	captureID                                               sql.NullString
}

func loadTranslationAttachmentTarget(tx *sql.Tx, id string) (translationAttachmentTarget, error) {
	var target translationAttachmentTarget
	err := tx.QueryRow(`SELECT state, target_language, profile, source_capture_id,
		source_kind, source_revision_key FROM translation_jobs WHERE id=$1 FOR UPDATE`, id).
		Scan(&target.state, &target.targetLanguage, &target.profile, &target.captureID,
			&target.sourceKind, &target.revisionKey)
	return target, err
}

func rejectDuplicateTranslationArtifact(tx *sql.Tx, id string, target translationAttachmentTarget, contentKey string) error {
	lockKey := target.targetLanguage + "\x00" + target.profile + "\x00" + contentKey
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return err
	}
	var existingID string
	err := tx.QueryRow(`SELECT id FROM translation_jobs
		WHERE id<>$1 AND state<>'cancelled' AND target_language=$2 AND profile=$3
		  AND source_content_key=$4 LIMIT 1`, id, target.targetLanguage, target.profile, contentKey).Scan(&existingID)
	if err == nil {
		return fmt.Errorf("%w: %s", errTranslationDuplicateSource, existingID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func promoteTranslationSourceCapture(tx *sql.Tx, captureID sql.NullString, artifact paperArtifact, contentKey, revisionKey string) error {
	if !captureID.Valid || captureID.String == "" {
		return nil
	}
	result, err := tx.Exec(`UPDATE translation_source_captures
		SET artifact_hash=$1, status='captured', size_bytes=$2,
		    content_key=$3, revision_key=$4
		WHERE id=$5`, artifact.Hash, len(artifact.Data), contentKey, revisionKey, captureID.String)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("source capture not found")
	}
	return nil
}

func queueAttachedTranslation(tx *sql.Tx, id, artifactHash, revisionKey, contentKey string) error {
	if _, err := tx.Exec("DELETE FROM translation_chunks WHERE job_id=$1", id); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE translation_jobs
		SET source_kind='pdf', source_hash=$1, source_artifact_hash=$1,
		    source_revision_key=$2, source_content_key=$3, source_manifest=NULL,
		    state='queued', stage='queued', progress_current=0, progress_total=0,
		    output_slug='', validation_report=NULL, error_summary='',
		    lease_owner='', lease_until=NULL, updated_at=now()
		WHERE id=$4 AND state='needs_input'`, artifactHash, revisionKey, contentKey, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTranslationStateConflict
	}
	return nil
}

func publishTranslationJob(id string) error {
	var doc *Document
	return commitAndProjectDocument(func() (*Document, error) {
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		job, err := scanTranslationJob(tx.QueryRow("SELECT "+translationJobColumns+" FROM translation_jobs WHERE id=$1 FOR UPDATE", id))
		if err != nil {
			return nil, err
		}
		if job.State == "published" {
			return nil, nil
		}
		if job.State != "draft_ready" || job.OutputSlug == "" {
			return nil, errTranslationStateConflict
		}
		var raw string
		if err := tx.QueryRow("SELECT raw_content FROM documents WHERE slug=$1 FOR UPDATE", job.OutputSlug).Scan(&raw); err != nil {
			return nil, err
		}
		doc = parseDoc(job.OutputSlug, "", setMarkdownPublished(raw))
		if err := upsertDocumentTx(tx, doc); err != nil {
			return nil, err
		}
		result, err := tx.Exec(`UPDATE translation_jobs
			SET state='published', stage='published', updated_at=now() WHERE id=$1`, id)
		if err != nil {
			return nil, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if rows != 1 {
			return nil, fmt.Errorf("publish translation job: expected one row, got %d", rows)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return doc, nil
	})
}

func translationActionMiss(id string) error {
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM translation_jobs WHERE id=$1)", id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errTranslationStateConflict
	}
	return sql.ErrNoRows
}

func setMarkdownPublished(raw string) string {
	newline := "\n"
	if strings.Contains(raw, "\r\n") {
		newline = "\r\n"
	}
	opener := "---" + newline
	if !strings.HasPrefix(raw, opener) {
		return "---" + newline + "publish: true" + newline + "---" + newline + raw
	}
	rest := raw[len(opener):]
	closing := newline + "---"
	end := strings.Index(rest, closing)
	if end < 0 {
		return "---" + newline + "publish: true" + newline + "---" + newline + raw
	}
	header := rest[:end]
	lines := strings.Split(header, newline)
	found := false
	for index, line := range lines {
		key, _, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "publish" {
			lines[index] = "publish: true"
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, "publish: true")
	}
	return opener + strings.Join(lines, newline) + rest[end:]
}

func claimTranslationJob(workerID string) (TranslationJob, error) {
	row := db.QueryRow(`
		WITH candidate AS (
			SELECT id FROM translation_jobs
			WHERE state='queued'
			   OR (state IN ('claimed','fetching','extracting','translating','validating')
			       AND lease_until < now())
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE translation_jobs
		SET state='claimed',
		    stage=CASE WHEN stage='queued' THEN 'claimed' ELSE stage END,
		    lease_owner=$1,
		    lease_until=now() + interval '2 minutes',
		    updated_at=now()
		WHERE id=(SELECT id FROM candidate)
		RETURNING `+translationJobColumns, workerID)
	return scanTranslationJob(row)
}
