package main

import (
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
