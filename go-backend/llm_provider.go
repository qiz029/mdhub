package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	llmBaseURL = getEnv("MDHUB_LLM_BASE_URL", "https://api.openai.com/v1")
	llmAPIKey  = getEnv("MDHUB_LLM_API_KEY", "")
	llmModel   = getEnv("MDHUB_LLM_MODEL", "gpt-4o-mini")
)

const defaultLLMResponseBytes int64 = 1 << 20

// LLMRequest is the provider-level completion contract. Task-specific prompt
// construction, chunking, retries and validation stay with the calling module.
type LLMRequest struct {
	System           string
	User             string
	Model            string
	Temperature      float64
	MaxOutputTokens  int
	MaxResponseBytes int64
}

type LLMResult struct {
	Content string
	Model   string
}

type LLMProvider interface {
	Complete(context.Context, LLMRequest) (LLMResult, error)
}

type LLMErrorKind string

const (
	LLMErrorTransport      LLMErrorKind = "transport"
	LLMErrorHTTPStatus     LLMErrorKind = "http_status"
	LLMErrorResponseLimit  LLMErrorKind = "response_limit"
	LLMErrorInvalidPayload LLMErrorKind = "invalid_payload"
)

type LLMProviderError struct {
	Kind       LLMErrorKind
	StatusCode int
	Err        error
}

func (e *LLMProviderError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("llm %s (status %d): %v", e.Kind, e.StatusCode, e.Err)
	}
	return fmt.Sprintf("llm %s: %v", e.Kind, e.Err)
}

func (e *LLMProviderError) Unwrap() error { return e.Err }

func (e *LLMProviderError) Retryable() bool {
	return e.Kind == LLMErrorTransport ||
		(e.Kind == LLMErrorHTTPStatus && (e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500))
}

type openAIChatProvider struct {
	baseURL      string
	apiKey       string
	defaultModel string
	client       *http.Client
	responseMax  int64
	maxAttempts  int
	retryBase    time.Duration
}

func newOpenAIChatProvider(baseURL, apiKey, defaultModel string, client *http.Client) *openAIChatProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &openAIChatProvider{
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		defaultModel: defaultModel,
		client:       client,
		responseMax:  defaultLLMResponseBytes,
		maxAttempts:  3,
		retryBase:    250 * time.Millisecond,
	}
}

func (p *openAIChatProvider) Complete(ctx context.Context, input LLMRequest) (LLMResult, error) {
	attempts := p.maxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := p.completeOnce(ctx, input)
		if err == nil {
			return result, nil
		}
		lastErr = err
		var providerErr *LLMProviderError
		if !errors.As(err, &providerErr) || !providerErr.Retryable() || attempt == attempts-1 {
			return LLMResult{}, err
		}
		delay := p.retryBase * time.Duration(1<<attempt)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return LLMResult{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return LLMResult{}, lastErr
}

func (p *openAIChatProvider) completeOnce(ctx context.Context, input LLMRequest) (LLMResult, error) {
	model := strings.TrimSpace(input.Model)
	if model == "" {
		model = p.defaultModel
	}
	body := map[string]any{
		"model":       model,
		"temperature": input.Temperature,
		"messages": []map[string]string{
			{"role": "system", "content": input.System},
			{"role": "user", "content": input.User},
		},
	}
	if input.MaxOutputTokens > 0 {
		body["max_tokens"] = input.MaxOutputTokens
	}
	reqBody, err := json.Marshal(body)
	if err != nil {
		return LLMResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return LLMResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return LLMResult{}, &LLMProviderError{Kind: LLMErrorTransport, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return LLMResult{}, &LLMProviderError{Kind: LLMErrorHTTPStatus, StatusCode: resp.StatusCode, Err: fmt.Errorf("request rejected")}
	}

	responseMax := input.MaxResponseBytes
	if responseMax <= 0 {
		responseMax = p.responseMax
	}
	if responseMax <= 0 {
		responseMax = defaultLLMResponseBytes
	}
	encoded, err := io.ReadAll(io.LimitReader(resp.Body, responseMax+1))
	if err != nil {
		return LLMResult{}, &LLMProviderError{Kind: LLMErrorInvalidPayload, Err: err}
	}
	if int64(len(encoded)) > responseMax {
		return LLMResult{}, &LLMProviderError{Kind: LLMErrorResponseLimit, Err: fmt.Errorf("response exceeds %d bytes", responseMax)}
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return LLMResult{}, &LLMProviderError{Kind: LLMErrorInvalidPayload, Err: fmt.Errorf("decode: %w", err)}
	}
	if len(out.Choices) == 0 {
		return LLMResult{}, &LLMProviderError{Kind: LLMErrorInvalidPayload, Err: fmt.Errorf("returned no choices")}
	}
	if out.Model == "" {
		out.Model = model
	}
	return LLMResult{Content: out.Choices[0].Message.Content, Model: out.Model}, nil
}

// llmChat preserves the existing short-task interface while classification
// and collision callers migrate onto the shared provider implementation.
func llmChat(client *http.Client, system, user string) (string, error) {
	provider := newOpenAIChatProvider(llmBaseURL, llmAPIKey, llmModel, client)
	result, err := provider.Complete(context.Background(), LLMRequest{
		System: system,
		User:   user,
	})
	return result.Content, err
}
