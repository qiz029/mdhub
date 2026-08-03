package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidateRemoteSourceURLRejectsUnsafeForms(t *testing.T) {
	for _, raw := range []string{
		"ftp://example.com/feed",
		"https://user:secret@example.com/feed",
		"https:///missing-host",
	} {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !errors.Is(validateRemoteSourceURL(parsed), errRemoteSourceDisallowed) {
			t.Fatalf("unsafe URL accepted: %q", raw)
		}
	}
	if err := validateRemoteSourceURL(nil); !errors.Is(err, errRemoteSourceDisallowed) {
		t.Fatalf("nil URL error = %v", err)
	}
	parsed, _ := url.Parse("https://example.com/feed")
	if err := validateRemoteSourceURL(parsed); err != nil {
		t.Fatalf("public URL rejected: %v", err)
	}
}

func TestRemoteSourceHTTPClientBlocksPrivateDestinationsAndProxies(t *testing.T) {
	client := newRemoteSourceClient(time.Second)
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport=%T", client.http.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("remote source transport inherited an environment proxy")
	}
	for _, raw := range []string{
		"http://127.0.0.1/source",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/source",
	} {
		request, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if response != nil {
			response.Body.Close()
		}
		if !errors.Is(err, errRemoteSourceDisallowed) {
			t.Fatalf("destination %q error = %v", raw, err)
		}
	}
}

func TestRemoteSourceRedirectPolicyRevalidatesEveryHop(t *testing.T) {
	client := newRemoteSourceClient(time.Second)
	unsafeURL, _ := url.Parse("ftp://example.com/file")
	request := &http.Request{URL: unsafeURL}
	if !errors.Is(client.http.CheckRedirect(request, nil), errRemoteSourceDisallowed) {
		t.Fatal("unsafe redirect was accepted")
	}
	via := make([]*http.Request, maxRemoteSourceRedirects)
	safeURL, _ := url.Parse("https://example.com/file")
	request.URL = safeURL
	if err := client.http.CheckRedirect(request, via); err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("redirect limit error = %v", err)
	}
}

func TestReadRemoteSourceBodyEnforcesHardLimit(t *testing.T) {
	data, err := readRemoteSourceBody(strings.NewReader("paper"), 5)
	if err != nil || string(data) != "paper" {
		t.Fatalf("data=%q error=%v", data, err)
	}
	if _, err := readRemoteSourceBody(strings.NewReader("123456"), 5); err == nil {
		t.Fatal("oversized remote source was accepted")
	}
}
