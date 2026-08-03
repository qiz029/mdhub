package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	maxTranslationPDFBytes       int64 = 50 << 20
	maxExtractedPaperBytes             = 32 << 20
	translationChunkRunes              = 6000
	translationPollInterval            = 3 * time.Second
	translationHeartbeatInterval       = 30 * time.Second
)

var errTranslationLeaseLost = errors.New("translation job lease lost")
var errPaperNeedsInput = errors.New("paper source requires a PDF upload")
var errInvalidPaperPDF = errors.New("invalid PDF upload")

type paperArtifact struct {
	Hash string
	MIME string
	Data []byte
}

type translationAgentWorker struct {
	id       string
	provider LLMProvider
	client   *remoteSourceClient
	extract  func(context.Context, []byte) (string, error)
}

func newTranslationAgentWorker(id string) *translationAgentWorker {
	providerClient := &http.Client{Timeout: 5 * time.Minute}
	return &translationAgentWorker{
		id:       id,
		provider: newOpenAIChatProvider(llmBaseURL, llmAPIKey, llmModel, providerClient),
		client:   newRemoteSourceClient(90 * time.Second),
		extract:  extractPDFWithPdftotext,
	}
}

func runTranslationWorker(ctx context.Context, once bool) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	return runTranslationAgent(ctx, once, newTranslationAgentWorker("translation-"+id))
}

func runTranslationAgent(ctx context.Context, once bool, worker *translationAgentWorker) error {
	for {
		job, err := claimTranslationJob(worker.id)
		if errors.Is(err, sql.ErrNoRows) {
			if once {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(translationPollInterval):
				continue
			}
		}
		if err != nil {
			return err
		}
		if processErr := worker.process(ctx, job); processErr != nil {
			if ctx.Err() != nil && errors.Is(processErr, ctx.Err()) {
				if releaseErr := releaseTranslationJob(job.ID, worker.id); releaseErr != nil && !errors.Is(releaseErr, errTranslationLeaseLost) {
					return fmt.Errorf("translation stopped: %v; release job: %w", processErr, releaseErr)
				}
				return ctx.Err()
			}
			if failErr := failTranslationJob(job.ID, worker.id, processErr); failErr != nil && !errors.Is(failErr, errTranslationLeaseLost) {
				return fmt.Errorf("translation failed: %v; record failure: %w", processErr, failErr)
			}
			if once {
				return processErr
			}
		}
		if once {
			return nil
		}
	}
}

func (w *translationAgentWorker) process(ctx context.Context, job TranslationJob) error {
	chunks, state, err := w.loadOrPrepareTranslation(ctx, &job)
	if err != nil {
		return err
	}
	usedModel, completed, err := w.translatePendingChunks(ctx, job, chunks, state)
	if err != nil {
		return err
	}
	return w.validateAndCompleteTranslation(job, chunks, usedModel, completed)
}

// loadOrPrepareTranslation owns the durable source-capture and extraction
// phases. Existing chunks are the resume checkpoint and skip remote work.
func (w *translationAgentWorker) loadOrPrepareTranslation(ctx context.Context, job *TranslationJob) ([]TranslationChunk, translationJobState, error) {
	chunks, err := loadTranslationChunks(job.ID)
	if err != nil {
		return nil, "", err
	}
	if len(chunks) > 0 {
		return chunks, translationClaimed, nil
	}
	if err := advanceTranslationStage(job.ID, w.id, translationClaimed, translationFetching, 0, 0); err != nil {
		return nil, "", err
	}
	artifact, err := loadTranslationArtifact(job.ID)
	if errors.Is(err, sql.ErrNoRows) {
		artifact, err = withTranslationLeaseHeartbeat(ctx, job.ID, w.id, func(operationCtx context.Context) (paperArtifact, error) {
			return fetchPaperPDF(operationCtx, w.client, job.Source)
		})
		if err == nil {
			err = storeTranslationArtifact(job.ID, w.id, artifact)
		}
	}
	if err != nil {
		return nil, "", err
	}
	if err := advanceTranslationStage(job.ID, w.id, translationFetching, translationExtracting, 0, 0); err != nil {
		return nil, "", err
	}
	text, err := withTranslationLeaseHeartbeat(ctx, job.ID, w.id, func(operationCtx context.Context) (string, error) {
		return w.extract(operationCtx, artifact.Data)
	})
	if err != nil {
		return nil, "", fmt.Errorf("extract paper: %w", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, "", fmt.Errorf("extract paper: no text found")
	}
	if len(text) > maxExtractedPaperBytes {
		return nil, "", fmt.Errorf("extracted paper exceeds 32 MB")
	}
	if job.Source.Title == "" {
		job.Source.Title = inferPaperTitle(text, job.Source.Identifier)
	}
	sourceChunks := chunkPaperText(text, translationChunkRunes)
	manifest := buildTranslationSourceManifest(artifact.Hash, sourceChunks)
	if err := prepareTranslationChunks(job.ID, w.id, job.Source.Title, sourceChunks, manifest); err != nil {
		return nil, "", err
	}
	job.SourceHash = artifact.Hash
	job.SourceManifest = manifest
	chunks, err = loadTranslationChunks(job.ID)
	return chunks, translationExtracting, err
}

// translatePendingChunks owns resume-aware progress and chunk checkpoints.
func (w *translationAgentWorker) translatePendingChunks(ctx context.Context, job TranslationJob, chunks []TranslationChunk, from translationJobState) (string, int, error) {
	completed := 0
	for _, chunk := range chunks {
		if chunk.State == "complete" && strings.TrimSpace(chunk.TranslatedText) != "" {
			completed++
		}
	}
	if err := advanceTranslationStage(job.ID, w.id, from, translationTranslating, completed, len(chunks)); err != nil {
		return "", completed, err
	}

	usedModel := job.Model
	if usedModel == "" {
		usedModel = translationModel
	}
	for index := range chunks {
		if chunks[index].State == "complete" && strings.TrimSpace(chunks[index].TranslatedText) != "" {
			continue
		}
		result, err := withTranslationLeaseHeartbeat(ctx, job.ID, w.id, func(operationCtx context.Context) (LLMResult, error) {
			translated, model, translateErr := translatePaperChunk(operationCtx, w.provider, job, chunks[index].SourceText, chunks[index].Ordinal, len(chunks))
			return LLMResult{Content: translated, Model: model}, translateErr
		})
		if err != nil {
			return "", completed, err
		}
		chunks[index].TranslatedText = result.Content
		chunks[index].State = "complete"
		chunks[index].Attempts++
		if result.Model != "" {
			usedModel = result.Model
		}
		chunkModel := result.Model
		if chunkModel == "" {
			chunkModel = usedModel
		}
		chunks[index].Provider = "openai-compatible"
		chunks[index].Model = chunkModel
		completed++
		if err := completeTranslationChunk(job.ID, w.id, chunks[index], chunkModel); err != nil {
			return "", completed, err
		}
	}
	return usedModel, completed, nil
}

// validateAndCompleteTranslation owns the final validation -> durable draft
// transition. No draft is stored until every integrity check passes.
func (w *translationAgentWorker) validateAndCompleteTranslation(job TranslationJob, chunks []TranslationChunk, usedModel string, completed int) error {
	if err := advanceTranslationStage(job.ID, w.id, translationTranslating, translationValidating, completed, len(chunks)); err != nil {
		return err
	}
	job.Provider = "openai-compatible"
	job.Model = usedModel
	markdown, slug := buildTranslationMarkdown(job, chunks)
	report := newTranslationEvidenceGate().Validate(job, chunks, markdown)
	if !report.Complete {
		if err := storeFailedTranslationValidation(job.ID, w.id, report); err != nil {
			return err
		}
		return fmt.Errorf("translation validation failed: %s", strings.Join(report.Issues, "; "))
	}
	draft := parseDoc(slug, "", markdown)
	if err := completeTranslationDraft(job.ID, w.id, draft, job.Provider, job.Model, report); err != nil {
		return fmt.Errorf("complete translation draft: %w", err)
	}
	return nil
}

func translatePaperChunk(ctx context.Context, provider LLMProvider, job TranslationJob, source string, ordinal, total int) (string, string, error) {
	system := fmt.Sprintf(`你是严谨的学术论文翻译 Agent。请把给定论文片段完整、准确地翻译为 %s。
不得摘要、删节、合并论点或补写原文没有的内容。保留标题层级、段落、列表、公式、表格、图注、脚注、链接、引用标记和参考文献编号。术语在各片段间保持一致。只输出译文，不要解释翻译过程。`, job.TargetLanguage)
	result, err := provider.Complete(ctx, LLMRequest{
		System:          system,
		User:            fmt.Sprintf("翻译配置：%s\n片段：%d/%d\n\n%s", job.Profile, ordinal+1, total, source),
		Model:           translationModel,
		Temperature:     0,
		MaxOutputTokens: 8192,
	})
	if err != nil {
		return "", "", fmt.Errorf("translate chunk %d: %w", ordinal, err)
	}
	if result.FinishReason != "" && result.FinishReason != "stop" {
		return "", result.Model, fmt.Errorf("translate chunk %d: incomplete provider response (%s)", ordinal, result.FinishReason)
	}
	translated := strings.TrimSpace(result.Content)
	if translated == "" {
		return "", result.Model, fmt.Errorf("translate chunk %d: empty result", ordinal)
	}
	return translated, result.Model, nil
}

func fetchPaperPDF(ctx context.Context, client *remoteSourceClient, source PaperSource) (paperArtifact, error) {
	parsed, err := url.Parse(source.ArtifactURL)
	if err != nil || validateRemoteSourceURL(parsed) != nil {
		return paperArtifact{}, fmt.Errorf("invalid paper artifact URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return paperArtifact{}, err
	}
	req.Header.Set("User-Agent", "MDHub-Paper-Translator/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return paperArtifact{}, fmt.Errorf("fetch paper: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return paperArtifact{}, fmt.Errorf("%w: source requires authorization", errPaperNeedsInput)
		}
		return paperArtifact{}, fmt.Errorf("fetch paper: http status %d", resp.StatusCode)
	}
	data, err := readRemoteSourceBody(resp.Body, maxTranslationPDFBytes)
	if err != nil {
		return paperArtifact{}, fmt.Errorf("fetch paper body: %w", err)
	}
	artifact, err := paperArtifactFromPDF(data)
	if err != nil {
		return paperArtifact{}, fmt.Errorf("%w: source did not return a PDF", errPaperNeedsInput)
	}
	return artifact, nil
}

func paperArtifactFromPDF(data []byte) (paperArtifact, error) {
	if int64(len(data)) > maxTranslationPDFBytes {
		return paperArtifact{}, fmt.Errorf("%w: file exceeds 50 MB", errInvalidPaperPDF)
	}
	if len(data) < 8 || !bytes.HasPrefix(data, []byte("%PDF-")) ||
		(data[5] != '1' && data[5] != '2') || data[6] != '.' || data[7] < '0' || data[7] > '9' {
		return paperArtifact{}, fmt.Errorf("%w: missing PDF signature", errInvalidPaperPDF)
	}
	trailer := data
	if len(trailer) > 2048 {
		trailer = trailer[len(trailer)-2048:]
	}
	if !bytes.Contains(trailer, []byte("%%EOF")) {
		return paperArtifact{}, fmt.Errorf("%w: incomplete PDF trailer", errInvalidPaperPDF)
	}
	hash := sha256.Sum256(data)
	return paperArtifact{Hash: fmt.Sprintf("%x", hash[:]), MIME: "application/pdf", Data: data}, nil
}

func extractPDFWithPdftotext(ctx context.Context, data []byte) (string, error) {
	file, err := os.CreateTemp("", "mdhub-paper-*.pdf")
	if err != nil {
		return "", err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "pdftotext", "-layout", name, "-")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr := &boundedByteBuffer{limit: 4 << 10}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return "", err
	}
	output, readErr := readBoundedOutput(stdout, maxExtractedPaperBytes)
	if readErr != nil {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
		return "", readErr
	}
	if err := command.Wait(); err != nil {
		message := strings.TrimSpace(truncateRunes(stderr.String(), 300))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("pdftotext: %s", message)
	}
	return string(output), nil
}

func readBoundedOutput(reader io.Reader, limit int64) ([]byte, error) {
	output, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(output)) > limit {
		return nil, fmt.Errorf("pdftotext output exceeds 32 MB")
	}
	return output, nil
}

type boundedByteBuffer struct {
	data  []byte
	limit int
}

func (b *boundedByteBuffer) Write(value []byte) (int, error) {
	available := b.limit - len(b.data)
	if available > 0 {
		if available > len(value) {
			available = len(value)
		}
		b.data = append(b.data, value[:available]...)
	}
	return len(value), nil
}

func (b *boundedByteBuffer) String() string { return string(b.data) }

func inferPaperTitle(text, fallback string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if len([]rune(line)) >= 8 {
			return truncateRunes(line, 200)
		}
	}
	if fallback != "" {
		return fallback
	}
	return "论文"
}

func loadTranslationArtifact(jobID string) (paperArtifact, error) {
	var artifact paperArtifact
	err := db.QueryRow(`
		SELECT a.hash, a.mime, a.data
		FROM translation_jobs j
		JOIN translation_artifacts a ON a.hash=j.source_artifact_hash
		WHERE j.id=$1`, jobID).Scan(&artifact.Hash, &artifact.MIME, &artifact.Data)
	return artifact, err
}

func storeTranslationArtifact(jobID, workerID string, artifact paperArtifact) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO translation_artifacts (hash, mime, data)
		VALUES ($1,$2,$3) ON CONFLICT (hash) DO NOTHING`, artifact.Hash, artifact.MIME, artifact.Data); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE translation_jobs
		SET source_hash=$1, source_artifact_hash=$1, source_content_key=$2, updated_at=now()
		WHERE id=$3 AND lease_owner=$4 AND state='fetching' AND lease_until > now()`,
		artifact.Hash, "sha256:"+artifact.Hash, jobID, workerID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return errTranslationLeaseLost
	}
	return tx.Commit()
}

// prepareTranslationChunks owns the extracting-stage checkpoint. Source
// metadata and every chunk commit together under the active worker lease, so
// cancellation or lease loss cannot leave a partially prepared job.
func prepareTranslationChunks(jobID, workerID, sourceTitle string, sourceChunks []string, manifest TranslationSourceManifest) error {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE translation_jobs
		SET source_title=CASE WHEN source_title='' THEN $1 ELSE source_title END,
		    progress_total=$2, source_manifest=$3,
		    lease_until=now()+interval '2 minutes', updated_at=now()
		WHERE id=$4 AND lease_owner=$5 AND state='extracting' AND lease_until > now()`,
		sourceTitle, len(sourceChunks), manifestJSON, jobID, workerID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTranslationLeaseLost
	}
	for ordinal, source := range sourceChunks {
		if _, err := tx.Exec(`INSERT INTO translation_chunks (job_id, ordinal, source_text, source_hash)
			VALUES ($1,$2,$3,$4) ON CONFLICT (job_id, ordinal) DO NOTHING`,
			jobID, ordinal, source, paperChunkHash(source)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func loadTranslationChunks(jobID string) ([]TranslationChunk, error) {
	rows, err := db.Query(`SELECT ordinal, source_text, source_hash, translated_text, state, attempts, provider, model
		FROM translation_chunks WHERE job_id=$1 ORDER BY ordinal`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chunks := []TranslationChunk{}
	for rows.Next() {
		var chunk TranslationChunk
		if err := rows.Scan(&chunk.Ordinal, &chunk.SourceText, &chunk.SourceHash,
			&chunk.TranslatedText, &chunk.State, &chunk.Attempts, &chunk.Provider, &chunk.Model); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func advanceTranslationStage(jobID, workerID string, from, to translationJobState, current, total int) error {
	result, err := db.Exec(`UPDATE translation_jobs
		SET state=$1, stage=$2, progress_current=$3, progress_total=$4,
		    lease_until=now()+interval '2 minutes', updated_at=now()
		WHERE id=$5 AND lease_owner=$6 AND state=$7 AND lease_until > now()`,
		string(to), string(to), current, total, jobID, workerID, string(from))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTranslationLeaseLost
	}
	return nil
}

func completeTranslationChunk(jobID, workerID string, chunk TranslationChunk, model string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE translation_chunks
		SET translated_text=$1, state='complete', attempts=attempts+1,
		    provider='openai-compatible', model=$2, updated_at=now()
		WHERE job_id=$3 AND ordinal=$4`, chunk.TranslatedText, model, jobID, chunk.Ordinal); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE translation_jobs
		SET progress_current=(SELECT count(*) FROM translation_chunks WHERE job_id=$1 AND state='complete'),
		    progress_total=(SELECT count(*) FROM translation_chunks WHERE job_id=$1),
		    provider='openai-compatible', model=CASE WHEN $3<>'' THEN $3 ELSE model END,
		    lease_until=now()+interval '2 minutes', updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND state='translating' AND lease_until > now()`, jobID, workerID, model)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTranslationLeaseLost
	}
	return tx.Commit()
}

// completeTranslationDraft owns the validating -> draft_ready transition.
// The draft and job state commit together so callers can never observe one
// without the other. Runtime projections are updated only after the durable
// transaction succeeds.
func completeTranslationDraft(jobID, workerID string, draft *Document, provider, model string, report TranslationValidationReport) error {
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return commitAndProjectDocument(func() (*Document, error) {
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err := upsertDocumentTx(tx, draft); err != nil {
			return nil, err
		}
		result, err := tx.Exec(`UPDATE translation_jobs
			SET state='draft_ready', stage='draft_ready', output_slug=$1, provider=$2, model=$3,
			    validation_report=$4, error_summary='', lease_owner='', lease_until=NULL, updated_at=now()
			WHERE id=$5 AND lease_owner=$6 AND state='validating' AND lease_until > now()`, draft.Slug, provider, model, reportJSON, jobID, workerID)
		if err != nil {
			return nil, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if rows != 1 {
			return nil, errTranslationLeaseLost
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return draft, nil
	})
}

func storeFailedTranslationValidation(jobID, workerID string, report TranslationValidationReport) error {
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, ordinal := range report.InvalidChunks {
		if _, err := tx.Exec(`UPDATE translation_chunks
			SET translated_text='', state='pending', provider='', model='', updated_at=now()
			WHERE job_id=$1 AND ordinal=$2`, jobID, ordinal); err != nil {
			return err
		}
	}
	result, err := tx.Exec(`UPDATE translation_jobs
		SET validation_report=$1, updated_at=now()
		WHERE id=$2 AND lease_owner=$3 AND state='validating' AND lease_until > now()`,
		reportJSON, jobID, workerID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTranslationLeaseLost
	}
	return tx.Commit()
}

func failTranslationJob(jobID, workerID string, cause error) error {
	state := "failed"
	if errors.Is(cause, errPaperNeedsInput) {
		state = "needs_input"
	}
	result, err := db.Exec(`UPDATE translation_jobs
		SET state=$1, stage=$1, error_summary=$2,
		    lease_owner='', lease_until=NULL, updated_at=now()
		WHERE id=$3 AND lease_owner=$4 AND lease_until > now()
		  AND state IN ('claimed','fetching','extracting','translating','validating')`, state, truncateRunes(cause.Error(), 500), jobID, workerID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTranslationLeaseLost
	}
	return nil
}

// releaseTranslationJob makes an interrupted attempt immediately claimable
// without turning a process shutdown into a business failure. Persisted stage,
// artifact, and chunks remain the resume checkpoint.
func releaseTranslationJob(jobID, workerID string) error {
	result, err := db.Exec(`UPDATE translation_jobs
		SET state='queued', lease_owner='', lease_until=NULL, updated_at=now()
		WHERE id=$1 AND lease_owner=$2
		  AND state IN ('claimed','fetching','extracting','translating','validating')`, jobID, workerID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTranslationLeaseLost
	}
	return nil
}

func withTranslationLeaseHeartbeat[T any](ctx context.Context, jobID, workerID string, operation func(context.Context) (T, error)) (T, error) {
	operationCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(translationHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-operationCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if err := renewTranslationLease(operationCtx, jobID, workerID); err != nil {
					cancel()
					heartbeatDone <- err
					return
				}
			}
		}
	}()
	value, operationErr := operation(operationCtx)
	cancel()
	heartbeatErr := <-heartbeatDone
	if heartbeatErr != nil {
		var zero T
		return zero, heartbeatErr
	}
	return value, operationErr
}

func renewTranslationLease(ctx context.Context, jobID, workerID string) error {
	result, err := db.ExecContext(ctx, `UPDATE translation_jobs
		SET lease_until=now()+interval '2 minutes', updated_at=now()
		WHERE id=$1 AND lease_owner=$2
		  AND lease_until > now()
		  AND state IN ('claimed','fetching','extracting','translating','validating')`, jobID, workerID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTranslationLeaseLost
	}
	return nil
}
