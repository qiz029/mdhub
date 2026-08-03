package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTranslationLanguage = "zh-CN"
	defaultTranslationProfile  = "paper-translate-v1"
	maxTranslationRequestBytes = 32 << 10
)

var errTranslationStateConflict = errors.New("translation job state does not allow this action")

type TranslationJob struct {
	ID              string          `json:"id"`
	Source          PaperSource     `json:"source"`
	TargetLanguage  string          `json:"target_language"`
	Profile         string          `json:"profile"`
	State           string          `json:"state"`
	Stage           string          `json:"stage"`
	ProgressCurrent int             `json:"progress_current"`
	ProgressTotal   int             `json:"progress_total"`
	OutputSlug      string          `json:"output_slug,omitempty"`
	Provider        string          `json:"provider,omitempty"`
	Model           string          `json:"model,omitempty"`
	Validation      json.RawMessage `json:"validation,omitempty"`
	Error           string          `json:"error,omitempty"`
	CreatedAt       int64           `json:"created_at"`
	UpdatedAt       int64           `json:"updated_at"`
}

type createTranslationJobRequest struct {
	Source         string `json:"source"`
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
	if len(parts) == 1 && r.Method == http.MethodGet {
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
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	var err error
	switch parts[1] {
	case "cancel":
		err = cancelTranslationJob(id)
	case "retry":
		err = retryTranslationJob(id)
	case "publish":
		err = publishTranslationJob(id)
	default:
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
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
	source, err := resolvePaperSource(input.Source)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	// Generic HTTP(S) URLs are admitted because signed/download endpoints do
	// not necessarily end in .pdf. The worker verifies the response signature
	// and moves HTML/paywalled sources to needs_input.
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
	id, err := randomID()
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	job := TranslationJob{
		ID:             id,
		Source:         source,
		TargetLanguage: targetLanguage,
		Profile:        profile,
		State:          "queued",
		Stage:          "queued",
	}
	var createdAt, updatedAt time.Time
	err = db.QueryRow(`
		INSERT INTO translation_jobs
			(id, source_input, source_kind, canonical_url, artifact_url,
			 source_identifier, source_version, target_language, profile)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING created_at, updated_at`,
		job.ID, source.Input, source.Kind, source.CanonicalURL, source.ArtifactURL,
		source.Identifier, source.Version, targetLanguage, profile,
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	job.CreatedAt = createdAt.UnixMilli()
	job.UpdatedAt = updatedAt.UnixMilli()
	writeJSONStatus(w, http.StatusCreated, job)
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
	target_language, profile, state, stage, progress_current, progress_total,
	output_slug, provider, model, validation_report, error_summary,
	created_at, updated_at`

type rowScanner interface {
	Scan(...any) error
}

func scanTranslationJob(row rowScanner) (TranslationJob, error) {
	var job TranslationJob
	var validation []byte
	var createdAt, updatedAt time.Time
	err := row.Scan(
		&job.ID, &job.Source.Input, &job.Source.Kind, &job.Source.CanonicalURL,
		&job.Source.ArtifactURL, &job.Source.Identifier, &job.Source.Version,
		&job.Source.Title, &job.TargetLanguage, &job.Profile, &job.State, &job.Stage,
		&job.ProgressCurrent, &job.ProgressTotal, &job.OutputSlug, &job.Provider,
		&job.Model, &validation, &job.Error, &createdAt, &updatedAt,
	)
	if err != nil {
		return TranslationJob{}, err
	}
	if len(validation) > 0 {
		job.Validation = append(json.RawMessage(nil), validation...)
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
		WHERE id=$1 AND state NOT IN ('published','cancelled')`, id)
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
		WHERE id=$1 AND state IN ('failed','needs_input')`, id)
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

func publishTranslationJob(id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := scanTranslationJob(tx.QueryRow("SELECT "+translationJobColumns+" FROM translation_jobs WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return err
	}
	if job.State == "published" {
		return nil
	}
	if job.State != "draft_ready" || job.OutputSlug == "" {
		return errTranslationStateConflict
	}
	var raw string
	if err := tx.QueryRow("SELECT raw_content FROM documents WHERE slug=$1 FOR UPDATE", job.OutputSlug).Scan(&raw); err != nil {
		return err
	}
	doc := parseDoc(job.OutputSlug, "", setMarkdownPublished(raw))
	if err := upsertDocumentTx(tx, doc); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE translation_jobs
		SET state='published', stage='published', updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("publish translation job: expected one row, got %d", rows)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	projectPublishedDocument(doc)
	return nil
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
