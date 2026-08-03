package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolvePaperSourceNormalizesArxivAbstractURL(t *testing.T) {
	source, err := resolvePaperSource("https://arxiv.org/abs/2401.01234v3")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != "arxiv" || source.Identifier != "2401.01234" || source.Version != "v3" {
		t.Fatalf("source = %#v", source)
	}
	if source.CanonicalURL != "https://arxiv.org/abs/2401.01234v3" {
		t.Fatalf("canonical url = %q", source.CanonicalURL)
	}
	if source.ArtifactURL != "https://arxiv.org/pdf/2401.01234v3" {
		t.Fatalf("artifact url = %q", source.ArtifactURL)
	}
}

func TestPaperFetcherRejectsPrivateAndMetadataAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1"} {
		if !disallowedPaperIP(net.ParseIP(raw)) {
			t.Fatalf("address %s should be rejected", raw)
		}
	}
	if disallowedPaperIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
}

func TestTranslationSourceResolveEndpointPreviewsDirectPDF(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/translation-sources/resolve",
		strings.NewReader(`{"source":"https://papers.example/research.pdf"}`))
	response := httptest.NewRecorder()
	handleTranslationSourceResolve(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"kind":"pdf"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestResolvePaperSourceRejectsEmbeddedCredentials(t *testing.T) {
	if _, err := resolvePaperSource("https://user:secret@example.com/paper.pdf"); err == nil {
		t.Fatal("expected credentials to be rejected")
	}
}
