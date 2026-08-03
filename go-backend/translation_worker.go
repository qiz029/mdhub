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
	"net"
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

type paperArtifact struct {
	Hash string
	MIME string
	Data []byte
}

type translationAgentWorker struct {
	id       string
	provider LLMProvider
	client   *http.Client
	extract  func(context.Context, []byte) (string, error)
}

func newTranslationAgentWorker(id string) *translationAgentWorker {
	providerClient := &http.Client{Timeout: 5 * time.Minute}
	return &translationAgentWorker{
		id:       id,
		provider: newOpenAIChatProvider(llmBaseURL, llmAPIKey, llmModel, providerClient),
		client:   newPaperHTTPClient(),
		extract:  extractPDFWithPdftotext,
	}
}

func runTranslationWorker(ctx context.Context, once bool) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	worker := newTranslationAgentWorker("translation-" + id)
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
		if err := worker.process(ctx, job); err != nil {
			if failErr := failTranslationJob(job.ID, worker.id, err); failErr != nil && !errors.Is(failErr, errTranslationLeaseLost) {
				return fmt.Errorf("translation failed: %v; record failure: %w", err, failErr)
			}
			if once {
				return err
			}
		}
		if once {
			return nil
		}
	}
}

func (w *translationAgentWorker) process(ctx context.Context, job TranslationJob) error {
	chunks, err := loadTranslationChunks(job.ID)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		if err := updateTranslationStage(job.ID, w.id, "fetching", "fetching", 0, 0); err != nil {
			return err
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
			return err
		}
		if err := updateTranslationStage(job.ID, w.id, "extracting", "extracting", 0, 0); err != nil {
			return err
		}
		text, err := withTranslationLeaseHeartbeat(ctx, job.ID, w.id, func(operationCtx context.Context) (string, error) {
			return w.extract(operationCtx, artifact.Data)
		})
		if err != nil {
			return fmt.Errorf("extract paper: %w", err)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return fmt.Errorf("extract paper: no text found")
		}
		if len(text) > maxExtractedPaperBytes {
			return fmt.Errorf("extracted paper exceeds 32 MB")
		}
		if job.Source.Title == "" {
			job.Source.Title = inferPaperTitle(text, job.Source.Identifier)
			if _, err := db.Exec("UPDATE translation_jobs SET source_title=$1, updated_at=now() WHERE id=$2 AND lease_owner=$3",
				job.Source.Title, job.ID, w.id); err != nil {
				return err
			}
		}
		if err := insertTranslationChunks(job.ID, chunkPaperText(text, translationChunkRunes)); err != nil {
			return err
		}
		chunks, err = loadTranslationChunks(job.ID)
		if err != nil {
			return err
		}
	}

	completed := 0
	for _, chunk := range chunks {
		if chunk.State == "complete" && strings.TrimSpace(chunk.TranslatedText) != "" {
			completed++
		}
	}
	if err := updateTranslationStage(job.ID, w.id, "translating", "translating", completed, len(chunks)); err != nil {
		return err
	}

	usedModel := translationModel
	for index := range chunks {
		if chunks[index].State == "complete" && strings.TrimSpace(chunks[index].TranslatedText) != "" {
			continue
		}
		result, err := withTranslationLeaseHeartbeat(ctx, job.ID, w.id, func(operationCtx context.Context) (LLMResult, error) {
			translated, model, translateErr := translatePaperChunk(operationCtx, w.provider, job, chunks[index].SourceText, chunks[index].Ordinal, len(chunks))
			return LLMResult{Content: translated, Model: model}, translateErr
		})
		if err != nil {
			return err
		}
		chunks[index].TranslatedText = result.Content
		chunks[index].State = "complete"
		chunks[index].Attempts++
		if result.Model != "" {
			usedModel = result.Model
		}
		completed++
		if err := completeTranslationChunk(job.ID, w.id, chunks[index], completed, len(chunks)); err != nil {
			return err
		}
	}

	if err := updateTranslationStage(job.ID, w.id, "validating", "validating", completed, len(chunks)); err != nil {
		return err
	}
	report := validateTranslationChunks(chunks)
	if !report.Complete {
		if err := storeFailedTranslationValidation(job.ID, w.id, report); err != nil {
			return err
		}
		return fmt.Errorf("translation validation failed: %s", strings.Join(report.Issues, "; "))
	}
	job.Provider = "openai-compatible"
	job.Model = usedModel
	markdown, slug := buildTranslationMarkdown(job, chunks)
	if err := publishDocument(parseDoc(slug, "", markdown)); err != nil {
		return fmt.Errorf("store translation draft: %w", err)
	}
	return completeTranslationJob(job.ID, w.id, slug, job.Provider, job.Model, report)
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
	translated := strings.TrimSpace(result.Content)
	if translated == "" {
		return "", result.Model, fmt.Errorf("translate chunk %d: empty result", ordinal)
	}
	return translated, result.Model, nil
}

func newPaperHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range addresses {
			if disallowedPaperIP(candidate.IP) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		}
		return nil, fmt.Errorf("paper source resolved only to disallowed addresses")
	}
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect uses unsupported scheme")
		}
		return nil
	}
	return client
}

func disallowedPaperIP(ip net.IP) bool {
	return ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func fetchPaperPDF(ctx context.Context, client *http.Client, source PaperSource) (paperArtifact, error) {
	parsed, err := url.Parse(source.ArtifactURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
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
		return paperArtifact{}, fmt.Errorf("fetch paper: http status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTranslationPDFBytes+1))
	if err != nil {
		return paperArtifact{}, fmt.Errorf("fetch paper body: %w", err)
	}
	if int64(len(data)) > maxTranslationPDFBytes {
		return paperArtifact{}, fmt.Errorf("paper PDF exceeds 50 MB")
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return paperArtifact{}, fmt.Errorf("%w: source did not return a PDF", errPaperNeedsInput)
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
	result, err := tx.Exec(`UPDATE translation_jobs SET source_hash=$1, source_artifact_hash=$1, updated_at=now()
		WHERE id=$2 AND lease_owner=$3`, artifact.Hash, jobID, workerID)
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

func insertTranslationChunks(jobID string, sourceChunks []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	rows, err := db.Query(`SELECT ordinal, source_text, source_hash, translated_text, state, attempts
		FROM translation_chunks WHERE job_id=$1 ORDER BY ordinal`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chunks := []TranslationChunk{}
	for rows.Next() {
		var chunk TranslationChunk
		if err := rows.Scan(&chunk.Ordinal, &chunk.SourceText, &chunk.SourceHash,
			&chunk.TranslatedText, &chunk.State, &chunk.Attempts); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func updateTranslationStage(jobID, workerID, state, stage string, current, total int) error {
	result, err := db.Exec(`UPDATE translation_jobs
		SET state=$1, stage=$2, progress_current=$3, progress_total=$4,
		    lease_until=now()+interval '2 minutes', updated_at=now()
		WHERE id=$5 AND lease_owner=$6`, state, stage, current, total, jobID, workerID)
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

func completeTranslationChunk(jobID, workerID string, chunk TranslationChunk, current, total int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE translation_chunks
		SET translated_text=$1, state='complete', attempts=attempts+1, updated_at=now()
		WHERE job_id=$2 AND ordinal=$3`, chunk.TranslatedText, jobID, chunk.Ordinal); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE translation_jobs
		SET progress_current=$1, progress_total=$2, lease_until=now()+interval '2 minutes', updated_at=now()
		WHERE id=$3 AND lease_owner=$4`, current, total, jobID, workerID)
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

func completeTranslationJob(jobID, workerID, slug, provider, model string, report TranslationValidationReport) error {
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return err
	}
	result, err := db.Exec(`UPDATE translation_jobs
		SET state='draft_ready', stage='draft_ready', output_slug=$1, provider=$2, model=$3,
		    validation_report=$4, error_summary='', lease_owner='', lease_until=NULL, updated_at=now()
		WHERE id=$5 AND lease_owner=$6`, slug, provider, model, reportJSON, jobID, workerID)
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
			SET translated_text='', state='pending', updated_at=now()
			WHERE job_id=$1 AND ordinal=$2`, jobID, ordinal); err != nil {
			return err
		}
	}
	result, err := tx.Exec(`UPDATE translation_jobs
		SET validation_report=$1, updated_at=now() WHERE id=$2 AND lease_owner=$3`,
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
		WHERE id=$3 AND lease_owner=$4`, state, truncateRunes(cause.Error(), 500), jobID, workerID)
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
