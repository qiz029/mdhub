package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildTermsCJK(t *testing.T) {
	terms := buildTerms("红烧肉")
	for _, want := range []string{"红", "红烧", "烧", "烧肉", "肉"} {
		if !slices.Contains(terms, want) {
			t.Errorf("buildTerms(红烧肉) = %v, missing %q", terms, want)
		}
	}
}

func TestBuildTermsCaseFold(t *testing.T) {
	terms := buildTerms("Hello")
	if !slices.Contains(terms, "hello") {
		t.Errorf("buildTerms(Hello) = %v, want lowercased term", terms)
	}
}

func TestScoreEntryRequiresAllTerms(t *testing.T) {
	e := &searchEntry{
		title: "红烧肉菜谱",
		plain: strings.ToLower("家庭聚餐必备的红烧肉做法，先用冷水焯水"),
	}
	if _, ok := scoreEntry(e, buildTerms("红烧肉")); !ok {
		t.Error("expected match for 红烧肉")
	}
	if _, ok := scoreEntry(e, buildTerms("烤鸭")); ok {
		t.Error("expected no match for 烤鸭")
	}
	// mixed CJK + latin, all terms must be present
	if _, ok := scoreEntry(e, buildTerms("红烧 abc")); ok {
		t.Error("expected no match when one term is absent")
	}
}

func TestScoreEntryTitleBoost(t *testing.T) {
	inTitle := &searchEntry{title: "红烧肉", plain: "家常菜"}
	inBody := &searchEntry{title: "家常菜", plain: "红烧肉红烧肉"}
	sTitle, ok1 := scoreEntry(inTitle, buildTerms("红烧"))
	sBody, ok2 := scoreEntry(inBody, buildTerms("红烧"))
	if !ok1 || !ok2 {
		t.Fatal("both entries should match")
	}
	if sTitle <= sBody {
		t.Errorf("title hit should outrank body hits: title=%v body=%v", sTitle, sBody)
	}
}

func TestMakeSnippetEscapesAndMarks(t *testing.T) {
	content := "前言 <script>alert(1)</script> 家庭聚餐必备的红烧肉做法 后记"
	snip := makeSnippet(content, buildTerms("红烧肉"))
	if strings.Contains(snip, "<script>") {
		t.Error("snippet must HTML-escape content")
	}
	if !strings.Contains(snip, "<mark>红烧</mark>") {
		t.Errorf("snippet should mark the term, got: %s", snip)
	}
}

func TestParseDocRequiresExactPublishBoolean(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		published bool
	}{
		{name: "true", value: "true", published: true},
		{name: "quoted true", value: `"true"`, published: true},
		{name: "uppercase true", value: "TRUE", published: true},
		{name: "false", value: "false", published: false},
		{name: "substring", value: "untrue", published: false},
		{name: "suffix", value: "true-ish", published: false},
		{name: "empty", value: "", published: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseDoc("note", "", "---\npublish: "+tt.value+"\n---\n# Note")
			if doc.Published != tt.published {
				t.Fatalf("Published = %v, want %v", doc.Published, tt.published)
			}
		})
	}
}

func TestParseDocKind(t *testing.T) {
	tests := []struct {
		name  string
		value string
		kind  string
	}{
		{name: "fleeting", value: "fleeting", kind: "fleeting"},
		{name: "quoted fleeting", value: `"fleeting"`, kind: "fleeting"},
		{name: "explicit note", value: "note", kind: "note"},
		{name: "substring", value: "fleetingx", kind: "note"},
		{name: "empty", value: "", kind: "note"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseDoc("note", "", "---\ntype: "+tt.value+"\n---\n# Note")
			if doc.Kind != tt.kind {
				t.Fatalf("Kind = %q, want %q", doc.Kind, tt.kind)
			}
		})
	}
	// no frontmatter type at all -> note
	if doc := parseDoc("note", "", "# Note"); doc.Kind != "note" {
		t.Fatalf("Kind = %q, want note", doc.Kind)
	}
}

func TestHTTPErrorHidesInternalDetailsAndSetsHeaders(t *testing.T) {
	response := httptest.NewRecorder()
	httpError(response, fmt.Errorf("pq: secret database detail"), http.StatusInternalServerError)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS header = %q, want *", got)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "internal server error" {
		t.Fatalf("error = %q, want generic message", body["error"])
	}
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatal("internal error detail leaked in response")
	}
}

func TestRandomIDUsesLongURLSafeIdentifiers(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id, err := randomID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 12 || strings.Trim(id, idChars) != "" {
			t.Fatalf("invalid id %q", id)
		}
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNormalizeNewComment(t *testing.T) {
	tests := []struct {
		name    string
		comment newComment
		wantErr bool
	}{
		{name: "thread", comment: newComment{Text: " hello ", Anchor: &commentAnchor{Quote: " quote "}}},
		{name: "reply", comment: newComment{Author: "Agent", Text: "reply", Reply: "thread-id"}},
		{name: "missing text", comment: newComment{Anchor: &commentAnchor{Quote: "quote"}}, wantErr: true},
		{name: "missing anchor", comment: newComment{Text: "hello"}, wantErr: true},
		{name: "long reply", comment: newComment{Text: "hello", Reply: strings.Repeat("x", 21)}, wantErr: true},
		{name: "long quote", comment: newComment{Text: "hello", Anchor: &commentAnchor{Quote: strings.Repeat("x", 501)}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := normalizeNewComment(&tt.comment)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.comment.Author == "" {
				t.Fatal("author default was not applied")
			}
		})
	}
}

func TestValidateIngressConfig(t *testing.T) {
	tests := []struct {
		name    string
		address string
		token   string
		wantErr bool
	}{
		{name: "IPv4 loopback", address: "127.0.0.1:10002"},
		{name: "IPv6 loopback", address: "[::1]:10002"},
		{name: "localhost", address: "localhost:10002"},
		{name: "public with token", address: ":10002", token: "secret"},
		{name: "public without token", address: ":10002", wantErr: true},
		{name: "specific public IP", address: "192.0.2.1:10002", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIngressConfig(tt.address, tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEditAccessRequiresConfiguredToken(t *testing.T) {
	previousToken, previousAddress := editToken, listenAddr
	t.Cleanup(func() { editToken, listenAddr = previousToken, previousAddress })
	request := httptest.NewRequest(http.MethodPut, "/api/documents/note", nil)

	listenAddr, editToken = "127.0.0.1:10002", ""
	if !hasEditAccess(request) {
		t.Fatal("loopback compatibility access was rejected")
	}
	editToken = "secret"
	if hasEditAccess(request) {
		t.Fatal("missing configured token was accepted")
	}
	request.Header.Set("X-MDHub-Edit-Token", "secret")
	if !hasEditAccess(request) {
		t.Fatal("matching configured token was rejected")
	}
}

func TestScanVaultFilesRecursive(t *testing.T) {
	dir := t.TempDir()
	old := vaultDir
	vaultDir = dir
	defer func() { vaultDir = old }()

	write := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md")
	write("sub/b.md")
	write(".hidden/c.md")
	write("node_modules/d.md")
	write("notmd.txt")

	got := scanVaultFiles()
	if len(got) != 2 {
		t.Fatalf("scanVaultFiles = %v, want 2 files", got)
	}
	for _, f := range got {
		if strings.Contains(f, ".hidden") || strings.Contains(f, "node_modules") {
			t.Errorf("should skip hidden/node_modules: %s", f)
		}
	}
}

const sampleComments = `---
note: notes/travel
---

## 用户 · 2026-07-20 21:30
<!-- {"id":"k3x9ab","quote":"路线包括大理","prefix":"前文","suffix":"后文"} -->
住宿定了吗？

## Hermes · 2026-07-20 21:45
<!-- {"reply":"k3x9ab"} -->
今晚看。

## 用户 · 2026-07-21 09:05
<!-- {"reply":"no-such-thread"} -->
孤儿回复应被丢弃

## 路人 · 2026-07-21 10:00
没有 meta 的 section 应被丢弃
`

func TestParseCommentThreads(t *testing.T) {
	threads := parseCommentThreads(sampleComments)
	if len(threads) != 1 {
		t.Fatalf("parseCommentThreads = %d threads, want 1", len(threads))
	}
	th := threads[0]
	if th.id != "k3x9ab" || th.quote != "路线包括大理" || th.prefix != "前文" || th.suffix != "后文" {
		t.Errorf("thread anchor wrong: %+v", th)
	}
	if len(th.entries) != 2 {
		t.Fatalf("thread has %d entries, want 2 (opening + reply)", len(th.entries))
	}
	if th.entries[0].author != "用户" || th.entries[0].text != "住宿定了吗？" {
		t.Errorf("opening entry wrong: %+v", th.entries[0])
	}
	if th.entries[1].author != "Hermes" || th.entries[1].text != "今晚看。" {
		t.Errorf("reply entry wrong: %+v", th.entries[1])
	}
}

func TestParseCommentSectionTime(t *testing.T) {
	secs := parseCommentSections(sampleComments)
	if len(secs) == 0 {
		t.Fatal("no sections parsed")
	}
	got := secs[0].at.Format("2006-01-02 15:04")
	if got != "2026-07-20 21:30" {
		t.Errorf("section time = %q, want %q", got, "2026-07-20 21:30")
	}
}

func TestCommentSlug(t *testing.T) {
	dir := t.TempDir()
	old := vaultDir
	vaultDir = dir
	defer func() { vaultDir = old }()

	// frontmatter note: wins
	fp := filepath.Join(dir, "_comments", "whatever.md")
	if got := commentSlug(fp, "---\nnote: notes/travel\n---\n"); got != "notes/travel" {
		t.Errorf("commentSlug frontmatter = %q, want notes/travel", got)
	}
	// otherwise derive from path relative to _comments/
	fp = filepath.Join(dir, "_comments", "translations", "foo.md")
	if got := commentSlug(fp, "no frontmatter"); got != "translations/foo" {
		t.Errorf("commentSlug path = %q, want translations/foo", got)
	}
}
