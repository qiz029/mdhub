package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSanitizeCategory(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"菜谱/家常", "菜谱/家常"},
		{" 菜谱 / 家常 ", "菜谱/家常"},
		{"技术/编程/Go", "技术/编程/Go"},
		{"a/b/c/d/e", "a/b/c"}, // depth capped at 3
		{"a/../b", ""},         // path traversal rejected
		{"a//b", ""},           // empty segment rejected
		{"", ""},               // empty rejected
		{"  ", ""},             // whitespace-only rejected
		{"菜谱/家常\n技术", ""},      // multi-line rejected
		{strings.Repeat("长", 50), strings.Repeat("长", 40)}, // segment truncated to 40 runes
	}
	for _, c := range cases {
		if got := sanitizeCategory(c.in); got != c.want {
			t.Errorf("sanitizeCategory(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeSegment(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"菜谱", "菜谱"},
		{" 菜谱 ", "菜谱"},
		{"a/b", ""},  // slash rejected
		{"a..b", ""}, // ".." rejected
		{"", ""},     // empty rejected
		{"  ", ""},   // whitespace-only rejected
		{"a\nb", ""}, // newline rejected
		{"a\rb", ""}, // carriage return rejected
		{strings.Repeat("组", 50), strings.Repeat("组", 40)}, // truncated to 40 runes
	}
	for _, c := range cases {
		if got := sanitizeSegment(c.in); got != c.want {
			t.Errorf("sanitizeSegment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestChildStats(t *testing.T) {
	paths := []string{
		"",         // root note 1
		"",         // root note 2
		"菜谱",       // direct note in 菜谱
		"菜谱/家常",    // nested under 菜谱/家常
		"菜谱/家常/汤",  // deeper still
		"菜谱/面点",    // another 菜谱 subfolder
		"技术",       // direct note in 技术
		"技术/Go/进阶", // nested under 技术/Go
	}

	t.Run("root", func(t *testing.T) {
		direct, folders := childStats(paths, "")
		if direct != 2 {
			t.Errorf("direct = %d, want 2", direct)
		}
		if !reflect.DeepEqual(folders, []string{"技术", "菜谱"}) {
			t.Errorf("folders = %v, want [技术 菜谱]", folders)
		}
	})

	t.Run("mid-level", func(t *testing.T) {
		direct, folders := childStats(paths, "菜谱")
		if direct != 1 {
			t.Errorf("direct = %d, want 1", direct)
		}
		if !reflect.DeepEqual(folders, []string{"家常", "面点"}) {
			t.Errorf("folders = %v, want [家常 面点]", folders)
		}
	})

	t.Run("nested leaf", func(t *testing.T) {
		direct, folders := childStats(paths, "菜谱/家常")
		if direct != 1 {
			t.Errorf("direct = %d, want 1", direct)
		}
		if !reflect.DeepEqual(folders, []string{"汤"}) {
			t.Errorf("folders = %v, want [汤]", folders)
		}
	})

	t.Run("empty node", func(t *testing.T) {
		direct, folders := childStats(paths, "不存在")
		if direct != 0 || len(folders) != 0 {
			t.Errorf("got (%d, %v), want (0, [])", direct, folders)
		}
	})
}

func TestParseFolderChoice(t *testing.T) {
	folders := []string{"菜谱", "技术"}
	cases := []struct {
		answer   string
		wantName string
		wantOK   bool
	}{
		{"菜谱", "菜谱", true},
		{" 技术 \n", "技术", true}, // trimmed
		{"留在这层", "", false},
		{" 留在这层 ", "", false},
		{"随笔", "", false},  // not in list
		{"菜谱。", "", false}, // not an exact match
		{"", "", false},
	}
	for _, c := range cases {
		name, ok := parseFolderChoice(c.answer, folders)
		if name != c.wantName || ok != c.wantOK {
			t.Errorf("parseFolderChoice(%q) = (%q, %v), want (%q, %v)",
				c.answer, name, ok, c.wantName, c.wantOK)
		}
	}
}

func TestValidateSplit(t *testing.T) {
	slugs := []string{"a", "b", "c"}

	t.Run("ok", func(t *testing.T) {
		groups := []splitGroup{
			{Name: "组一", Slugs: []string{"a", "b"}},
			{Name: " 组二 ", Slugs: []string{"c"}}, // name gets trimmed in place
		}
		if err := validateSplit(groups, slugs); err != nil {
			t.Fatalf("validateSplit: %v", err)
		}
		if groups[1].Name != "组二" {
			t.Errorf("name not sanitized back: %q", groups[1].Name)
		}
	})

	t.Run("duplicate assignment", func(t *testing.T) {
		groups := []splitGroup{
			{Name: "组一", Slugs: []string{"a", "b"}},
			{Name: "组二", Slugs: []string{"b", "c"}},
		}
		if err := validateSplit(groups, slugs); err == nil {
			t.Error("expected error for slug assigned twice")
		}
	})

	t.Run("missing slug", func(t *testing.T) {
		groups := []splitGroup{
			{Name: "组一", Slugs: []string{"a"}},
			{Name: "组二", Slugs: []string{"b"}},
		}
		if err := validateSplit(groups, slugs); err == nil {
			t.Error("expected error for uncovered slug")
		}
	})

	t.Run("unknown slug", func(t *testing.T) {
		groups := []splitGroup{
			{Name: "组一", Slugs: []string{"a", "b", "c"}},
			{Name: "组二", Slugs: []string{"zzz"}},
		}
		if err := validateSplit(groups, slugs); err == nil {
			t.Error("expected error for unknown slug")
		}
	})

	t.Run("too few groups", func(t *testing.T) {
		groups := []splitGroup{{Name: "组一", Slugs: slugs}}
		if err := validateSplit(groups, slugs); err == nil {
			t.Error("expected error for 1 group")
		}
	})

	t.Run("too many groups", func(t *testing.T) {
		groups := make([]splitGroup, 7)
		for i := range groups {
			groups[i] = splitGroup{Name: strings.Repeat("组", i+1), Slugs: []string{"a"}}
		}
		if err := validateSplit(groups, slugs); err == nil {
			t.Error("expected error for 7 groups")
		}
	})

	t.Run("duplicate group names", func(t *testing.T) {
		groups := []splitGroup{
			{Name: "组一", Slugs: []string{"a"}},
			{Name: " 组一 ", Slugs: []string{"b", "c"}}, // same after sanitize
		}
		if err := validateSplit(groups, slugs); err == nil {
			t.Error("expected error for duplicate group names")
		}
	})

	t.Run("invalid group name", func(t *testing.T) {
		groups := []splitGroup{
			{Name: "a/b", Slugs: []string{"a"}},
			{Name: "组二", Slugs: []string{"b", "c"}},
		}
		if err := validateSplit(groups, slugs); err == nil {
			t.Error("expected error for name containing slash")
		}
	})

	t.Run("empty group", func(t *testing.T) {
		groups := []splitGroup{
			{Name: "组一", Slugs: nil},
			{Name: "组二", Slugs: []string{"a", "b", "c"}},
		}
		if err := validateSplit(groups, slugs); err == nil {
			t.Error("expected error for empty group")
		}
	})
}

// fakeLLM returns an httptest server mimicking the OpenAI chat endpoint.
func fakeLLM(t *testing.T, status int, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("missing bearer auth")
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "test-model" {
			t.Errorf("bad model %q", req.Model)
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	// point the package-level LLM config at the fake server
	oldURL, oldKey, oldModel := llmBaseURL, llmAPIKey, llmModel
	llmBaseURL, llmAPIKey, llmModel = srv.URL, "test-key", "test-model"
	t.Cleanup(func() { llmBaseURL, llmAPIKey, llmModel = oldURL, oldKey, oldModel })
	return srv
}

func TestLLMChatOK(t *testing.T) {
	srv := fakeLLM(t, 200, "菜谱")
	got, err := llmChat(srv.Client(), "sys", "user")
	if err != nil {
		t.Fatalf("llmChat: %v", err)
	}
	if got != "菜谱" {
		t.Errorf("content = %q, want 菜谱", got)
	}
}

func TestLLMChatRejectsNon2xx(t *testing.T) {
	srv := fakeLLM(t, 500, "菜谱")
	if _, err := llmChat(srv.Client(), "s", "u"); err == nil {
		t.Error("expected error for http 500")
	}
}

func TestLLMChatRejectsBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()
	old := llmBaseURL
	llmBaseURL = srv.URL
	t.Cleanup(func() { llmBaseURL = old })

	if _, err := llmChat(srv.Client(), "s", "u"); err == nil {
		t.Error("expected error for undecodable body")
	}
}

func TestLLMChatRejectsNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{}})
	}))
	defer srv.Close()
	old := llmBaseURL
	llmBaseURL = srv.URL
	t.Cleanup(func() { llmBaseURL = old })

	if _, err := llmChat(srv.Client(), "s", "u"); err == nil {
		t.Error("expected error for empty choices")
	}
}

func TestLLMChooseFolder(t *testing.T) {
	srv := fakeLLM(t, 200, "菜谱")
	name, ok, err := llmChooseFolder(srv.Client(), "红烧肉", "做法……", "", []string{"菜谱", "技术"})
	if err != nil {
		t.Fatalf("llmChooseFolder: %v", err)
	}
	if !ok || name != "菜谱" {
		t.Errorf("got (%q, %v), want (菜谱, true)", name, ok)
	}
}

func TestLLMChooseFolderStay(t *testing.T) {
	srv := fakeLLM(t, 200, "留在这层")
	_, ok, err := llmChooseFolder(srv.Client(), "t", "c", "技术", []string{"Go", "Rust"})
	if err != nil {
		t.Fatalf("llmChooseFolder: %v", err)
	}
	if ok {
		t.Error("expected ok=false for 留在这层")
	}
}

func TestLLMSplitNotesOK(t *testing.T) {
	srv := fakeLLM(t, 200, `{"groups":[{"name":"肉类","slugs":["a","b"]},{"name":"汤类","slugs":["c"]}]}`)
	notes := []splitNote{
		{slug: "a", title: "红烧肉", content: "……"},
		{slug: "b", title: "糖醋排骨", content: "……"},
		{slug: "c", title: "冬瓜汤", content: "……"},
	}
	groups, err := llmSplitNotes(srv.Client(), "菜谱", notes, "")
	if err != nil {
		t.Fatalf("llmSplitNotes: %v", err)
	}
	if len(groups) != 2 || groups[0].Name != "肉类" || groups[1].Slugs[0] != "c" {
		t.Errorf("unexpected groups: %+v", groups)
	}
	if err := validateSplit(groups, []string{"a", "b", "c"}); err != nil {
		t.Errorf("validateSplit after llmSplitNotes: %v", err)
	}
}

func TestLLMSplitNotesCodeFence(t *testing.T) {
	srv := fakeLLM(t, 200, "```json\n{\"groups\":[{\"name\":\"甲\",\"slugs\":[\"a\"]}]}\n```")
	groups, err := llmSplitNotes(srv.Client(), "", []splitNote{{slug: "a"}}, "")
	if err != nil {
		t.Fatalf("llmSplitNotes: %v", err)
	}
	if len(groups) != 1 || groups[0].Name != "甲" {
		t.Errorf("unexpected groups: %+v", groups)
	}
}

func TestLLMSplitNotesBadJSON(t *testing.T) {
	srv := fakeLLM(t, 200, "这不是 JSON")
	if _, err := llmSplitNotes(srv.Client(), "", []splitNote{{slug: "a"}}, ""); err == nil {
		t.Error("expected error for non-JSON answer")
	}
}
