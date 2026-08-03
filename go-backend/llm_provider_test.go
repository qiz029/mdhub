package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestOpenAIChatProviderRetriesTypedTransientErrors(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		status := http.StatusTooManyRequests
		body := `{}`
		if attempts == 2 {
			status = http.StatusOK
			body = `{"choices":[{"message":{"content":"ok"}}]}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	provider := newOpenAIChatProvider("https://llm.example/v1", "", "model", client)
	provider.retryBase = 0
	result, err := provider.Complete(context.Background(), LLMRequest{User: "paper"})
	if err != nil || result.Content != "ok" || attempts != 2 {
		t.Fatalf("result=%#v attempts=%d err=%v", result, attempts, err)
	}
}

func TestOpenAIChatProviderReturnsTypedNonRetryableLimitError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[]}`))}, nil
	})}
	provider := newOpenAIChatProvider("https://llm.example/v1", "", "model", client)
	_, err := provider.Complete(context.Background(), LLMRequest{MaxResponseBytes: 4})
	var providerErr *LLMProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != LLMErrorResponseLimit || providerErr.Retryable() {
		t.Fatalf("error = %#v", err)
	}
}

func TestOpenAIChatProviderCompletesWithRequestOptions(t *testing.T) {
	var requestBody map[string]any
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://llm.example/v1/chat/completions" {
			t.Fatalf("url = %q", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"translated"}}]}`)),
		}, nil
	})}

	provider := newOpenAIChatProvider("https://llm.example/v1", "secret", "default-model", client)
	result, err := provider.Complete(context.Background(), LLMRequest{
		System:          "translate fully",
		User:            "paper text",
		Model:           "translation-model",
		Temperature:     0.2,
		MaxOutputTokens: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "translated" || result.Model != "translation-model" {
		t.Fatalf("result = %#v", result)
	}
	if requestBody["model"] != "translation-model" {
		t.Fatalf("model = %#v", requestBody["model"])
	}
	if requestBody["max_tokens"] != float64(4096) {
		t.Fatalf("max_tokens = %#v", requestBody["max_tokens"])
	}
	messages, ok := requestBody["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v", requestBody["messages"])
	}
}
