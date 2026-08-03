package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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

var arxivIDPattern = regexp.MustCompile(`^([0-9]{4}\.[0-9]{4,5}|[a-z-]+(?:\.[A-Z]{2})?/[0-9]{7})(v[0-9]+)?$`)

func resolvePaperSource(input string) (PaperSource, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return PaperSource{}, fmt.Errorf("source is required")
	}

	if match := arxivIDPattern.FindStringSubmatch(raw); match != nil {
		return arxivSource(raw, match[1], match[2]), nil
	}

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

	host := strings.ToLower(parsed.Hostname())
	if host == "arxiv.org" || host == "www.arxiv.org" {
		path := strings.Trim(parsed.EscapedPath(), "/")
		parts := strings.Split(path, "/")
		if len(parts) >= 2 && (parts[0] == "abs" || parts[0] == "pdf") {
			id := strings.TrimSuffix(parts[1], ".pdf")
			if match := arxivIDPattern.FindStringSubmatch(id); match != nil {
				return arxivSource(raw, match[1], match[2]), nil
			}
		}
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
	source, err := resolvePaperSource(input.Source)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, source)
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
