package main

// B-tree-style LLM categorization: every published note gets a slash-
// separated category path (e.g. "菜谱/家常") for the frontend's tree sidebar.
// Instead of assigning a path in one shot, each note is INSERTED into the
// existing tree (descending folder by folder, at most 5 levels) and any node
// whose direct children (direct notes + child folders) exceeds
// maxTreeChildren is SPLIT into semantic sub-folders.
// category_path = "" means the note sits at the root node.
//
// Notes with a frontmatter `category:` (category_manual) are pinned: the
// algorithm never moves them, but they still count toward node occupancy.
//
// Overflow checks never propagate upwards: an insert only grows the child
// count of the final placement node, and a split only restructures the
// split node's own subtree, so only that one node needs re-checking.
//
// Classification runs on a background queue, triggered at write time only —
// never on the read path. The worker talks to Postgres directly and does not
// touch the in-memory search index (no mu needed).
// Disabled entirely when MDHUB_LLM_API_KEY is empty.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

const maxTreeChildren = 10

var (
	llmBaseURL = getEnv("MDHUB_LLM_BASE_URL", "https://api.openai.com/v1")
	llmAPIKey  = getEnv("MDHUB_LLM_API_KEY", "") // empty = classification disabled
	llmModel   = getEnv("MDHUB_LLM_MODEL", "gpt-4o-mini")

	treeJobs = newKeyedJobQueue[treeJob]("classify", 500)
)

// treeJob is one unit of categorization work: either inserting a note into
// the tree (split=false, slug set) or splitting an oversized node
// (split=true, splitPath set — note the root node's path is "", so the
// dispatch flag must be explicit).
type treeJob struct {
	slug      string
	splitPath string
	split     bool
}

// sanitizeCategory normalizes a slash-separated category path: trims each
// segment, caps depth at 3 and segment length at 40 runes. Returns "" for
// anything invalid (empty, "..", empty segment, multi-line). Used for
// frontmatter `category:` values (manual categories).
func sanitizeCategory(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "..") || strings.ContainsAny(s, "\r\n") {
		return ""
	}
	var parts []string
	for _, p := range strings.Split(s, "/") {
		p = strings.TrimSpace(p)
		if p == "" {
			return ""
		}
		if r := []rune(p); len(r) > 40 {
			p = string(r[:40])
		}
		parts = append(parts, p)
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, "/")
}

// sanitizeSegment normalizes one single path segment (e.g. an LLM-proposed
// group name): trims, truncates to 40 runes. Returns "" for anything
// invalid (empty, contains "/", ".." or newlines).
func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "/\r\n") || strings.Contains(s, "..") {
		return ""
	}
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	return s
}

// joinPath appends a segment to a tree path (root is "").
func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

// enqueueInsert queues a note for tree insertion.
func enqueueInsert(slug string) {
	enqueueTree(treeJob{slug: slug}, slug)
}

// enqueueSplit queues a node for splitting.
func enqueueSplit(path string) {
	enqueueTree(treeJob{splitPath: path, split: true}, "split:"+path)
}

// enqueueTree queues a job under a dedup key. No-op when the feature is
// disabled; drops (logged) when the queue is full.
func enqueueTree(job treeJob, key string) {
	if llmAPIKey == "" {
		return
	}
	treeJobs.enqueue(key, job)
}

// startClassifier launches the background worker; no-op without an API key.
func startClassifier() {
	if llmAPIKey == "" {
		log.Println("LLM classification disabled (MDHUB_LLM_API_KEY empty)")
		return
	}
	client := &http.Client{Timeout: 30 * time.Second}
	treeJobs.start(func(job treeJob) error {
		if job.split {
			return doSplit(job.splitPath, client)
		}
		return doInsert(job.slug, client)
	})
}

// waitClassify blocks until every queued job has finished. Used by one-shot
// commands (-import) so results land before the process exits.
func waitClassify() {
	treeJobs.wait()
}

// ---- tree statistics ----

// childStats computes the direct children of a tree node from the category
// paths of all published notes: how many notes sit directly in the node and
// the names of its direct child folders. node "" is the root.
func childStats(paths []string, node string) (directNotes int, folders []string) {
	seen := map[string]bool{}
	for _, cp := range paths {
		if cp == node {
			directNotes++
			continue
		}
		rest := cp
		if node != "" {
			prefix := node + "/"
			if !strings.HasPrefix(cp, prefix) {
				continue
			}
			rest = cp[len(prefix):]
		}
		first := strings.SplitN(rest, "/", 2)[0]
		if first != "" && !seen[first] {
			seen[first] = true
			folders = append(folders, first)
		}
	}
	sort.Strings(folders)
	return directNotes, folders
}

// loadPublishedPaths fetches the category_path of every published note.
// A vault is small, so computing tree stats in Go beats recursive SQL.
func loadPublishedPaths() ([]string, error) {
	rows, err := db.Query("SELECT category_path FROM documents WHERE published")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			paths = append(paths, c)
		}
	}
	return paths, rows.Err()
}

// childFolders returns the names of the node's direct child folders.
func childFolders(path string) ([]string, error) {
	paths, err := loadPublishedPaths()
	if err != nil {
		return nil, err
	}
	_, folders := childStats(paths, path)
	return folders, nil
}

// childrenOf returns the node's total direct child count (notes + folders).
func childrenOf(path string) (int, error) {
	paths, err := loadPublishedPaths()
	if err != nil {
		return 0, err
	}
	direct, folders := childStats(paths, path)
	return direct + len(folders), nil
}

// ---- insert ----

// doInsert places one uncategorized note into the tree: descends from the
// root through LLM-chosen folders (at most 5 levels), stores the resulting
// path and queues a split if the placement node overflowed.
func doInsert(slug string, client *http.Client) error {
	var title, content, cp string
	var manual bool
	if err := db.QueryRow(
		"SELECT title, content, category_path, category_manual FROM documents WHERE slug=$1", slug).
		Scan(&title, &content, &cp, &manual); err != nil {
		return err
	}
	if manual || cp != "" {
		return nil // pinned, or already placed (e.g. by a split)
	}

	path := ""
	for i := 0; i < 5; i++ {
		folders, err := childFolders(path)
		if err != nil {
			return err
		}
		if len(folders) == 0 {
			break
		}
		name, ok, err := llmChooseFolder(client, title, content, path, folders)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		path = joinPath(path, name)
	}

	if _, err := db.Exec("UPDATE documents SET category_path=$1 WHERE slug=$2", path, slug); err != nil {
		return err
	}
	log.Printf("inserted %s -> %q", slug, path)

	n, err := childrenOf(path)
	if err != nil {
		return err
	}
	if n > maxTreeChildren {
		enqueueSplit(path)
	}
	return nil
}

// llmChooseFolder asks the LLM which child folder of the current node the
// note belongs in. ok=false means the note should stay at this level.
func llmChooseFolder(client *http.Client, title, content, path string, folders []string) (name string, ok bool, err error) {
	if r := []rune(content); len(r) > 300 {
		content = string(r[:300])
	}
	var user strings.Builder
	user.WriteString("笔记标题：" + title + "\n\n笔记内容摘录：\n" + content + "\n\n")
	if path == "" {
		user.WriteString("当前位于分类树的根节点。")
	} else {
		user.WriteString("当前位于分类节点：" + path)
	}
	user.WriteString("\n该节点的子文件夹：\n")
	for _, f := range folders {
		user.WriteString(f + "\n")
	}
	answer, err := llmChat(client,
		"你的任务是判断一篇笔记应该放进当前节点的哪个子文件夹。只回复其中一个子文件夹名称的原文，不要输出任何其他文字；如果都不合适，回复「留在这层」。",
		user.String())
	if err != nil {
		return "", false, err
	}
	name, ok = parseFolderChoice(answer, folders)
	return name, ok, nil
}

// parseFolderChoice interprets the LLM's answer: an exact folder name from
// the list means descend into it; 「留在这层」 or anything unrecognized
// means stop at the current node.
func parseFolderChoice(answer string, folders []string) (string, bool) {
	a := strings.TrimSpace(answer)
	if a == "" || a == "留在这层" {
		return "", false
	}
	for _, f := range folders {
		if a == f {
			return f, true
		}
	}
	return "", false
}

// ---- split ----

type splitNote struct {
	slug    string
	title   string
	content string
}

type splitGroup struct {
	Name  string   `json:"name"`
	Slugs []string `json:"slugs"`
}

// doSplit partitions the movable (non-manual) notes of one oversized node
// into LLM-proposed sub-folders. Pinned notes stay put. If the node is
// still oversized afterwards (e.g. too many pinned notes) it only logs —
// re-queueing would loop forever.
func doSplit(path string, client *http.Client) error {
	rows, err := db.Query(
		"SELECT slug, title, content FROM documents WHERE published AND category_path=$1 AND NOT category_manual", path)
	if err != nil {
		return err
	}
	var notes []splitNote
	for rows.Next() {
		var n splitNote
		if rows.Scan(&n.slug, &n.title, &n.content) == nil {
			notes = append(notes, n)
		}
	}
	rows.Close()
	if len(notes) < 2 {
		log.Printf("split %q: only %d movable notes, cannot split", path, len(notes))
		return nil
	}

	slugs := make([]string, len(notes))
	for i, n := range notes {
		slugs[i] = n.slug
	}

	groups, err := llmSplitNotes(client, path, notes, "")
	if err != nil {
		return err
	}
	if verr := validateSplit(groups, slugs); verr != nil {
		// one retry with the reason appended to the prompt
		groups, err = llmSplitNotes(client, path, notes, verr.Error())
		if err != nil {
			return err
		}
		if verr2 := validateSplit(groups, slugs); verr2 != nil {
			log.Printf("split %q: invalid grouping after retry, dropped: %v", path, verr2)
			return nil
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, g := range groups {
		dest := joinPath(path, g.Name)
		for _, slug := range g.Slugs {
			if _, err := tx.Exec(
				"UPDATE documents SET category_path=$1 WHERE slug=$2 AND category_path=$3 AND NOT category_manual",
				dest, slug, path); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("split %q into %d groups", path, len(groups))

	n, err := childrenOf(path)
	if err != nil {
		return err
	}
	if n > maxTreeChildren {
		log.Printf("split %q: node still has %d direct children (> %d), giving up", path, n, maxTreeChildren)
	}
	return nil
}

// llmSplitNotes asks the LLM to partition the notes of one tree node into
// 2–6 semantic sub-folders. retryReason, when non-empty, describes why the
// previous answer was rejected and is appended to the prompt.
func llmSplitNotes(client *http.Client, path string, notes []splitNote, retryReason string) ([]splitGroup, error) {
	var user strings.Builder
	if path == "" {
		user.WriteString("分类树根节点")
	} else {
		user.WriteString("分类节点：" + path)
	}
	user.WriteString(" 下的笔记列表：\n\n")
	for _, n := range notes {
		content := n.content
		if r := []rune(content); len(r) > 4000 {
			content = string(r[:4000])
		}
		fmt.Fprintf(&user, "slug: %s\n标题: %s\n内容:\n%s\n\n---\n\n", n.slug, n.title, content)
	}
	if retryReason != "" {
		user.WriteString("你上一次的输出不合法：" + retryReason + "\n请重新输出。\n")
	}
	answer, err := llmChat(client,
		"你的任务是把一个分类节点下的笔记划分成 2 到 6 个子文件夹。只输出 JSON，格式为 {\"groups\":[{\"name\":\"组名\",\"slugs\":[\"slug1\",\"slug2\"]}]}，不要输出任何其他文字。要求：每篇笔记的 slug 必须出现且只能出现一次；组名用简短的中文；分组按主题语义聚合。",
		user.String())
	if err != nil {
		return nil, err
	}
	return parseSplitGroups(answer)
}

// parseSplitGroups extracts the {"groups":[...]} payload from the LLM's
// answer, tolerating code fences and surrounding chatter.
func parseSplitGroups(answer string) ([]splitGroup, error) {
	a := strings.TrimSpace(answer)
	a = strings.TrimPrefix(a, "```json")
	a = strings.TrimPrefix(a, "```")
	a = strings.TrimSuffix(a, "```")
	a = strings.TrimSpace(a)
	start := strings.Index(a, "{")
	end := strings.LastIndex(a, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in answer: %q", answer)
	}
	var out struct {
		Groups []splitGroup `json:"groups"`
	}
	if err := json.Unmarshal([]byte(a[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("bad split JSON: %w", err)
	}
	return out.Groups, nil
}

// validateSplit checks an LLM grouping against the input notes: 2–6 groups,
// each group name a valid single path segment (stored back sanitized into
// the group), no empty groups, every input slug covered exactly once, no
// unknown slugs, no duplicate group names. The error message is written for
// the LLM — it is fed back into the retry prompt.
func validateSplit(groups []splitGroup, inputSlugs []string) error {
	if len(groups) < 2 || len(groups) > 6 {
		return fmt.Errorf("组数 %d 不在 2~6 之间", len(groups))
	}
	want := map[string]bool{}
	for _, s := range inputSlugs {
		want[s] = true
	}
	seenNames := map[string]bool{}
	assigned := map[string]int{}
	for i := range groups {
		name := sanitizeSegment(groups[i].Name)
		if name == "" {
			return fmt.Errorf("组名 %q 非法", groups[i].Name)
		}
		if seenNames[name] {
			return fmt.Errorf("组名 %q 重复", name)
		}
		seenNames[name] = true
		groups[i].Name = name
		if len(groups[i].Slugs) == 0 {
			return fmt.Errorf("组 %q 为空", name)
		}
		for _, s := range groups[i].Slugs {
			if !want[s] {
				return fmt.Errorf("未知 slug %q", s)
			}
			assigned[s]++
		}
	}
	for _, s := range inputSlugs {
		switch assigned[s] {
		case 0:
			return fmt.Errorf("slug %q 未被分组", s)
		case 1:
		default:
			return fmt.Errorf("slug %q 被重复分组", s)
		}
	}
	return nil
}

// ---- generic LLM call ----

// llmChat performs one OpenAI-compatible chat completion and returns the
// assistant message content. Pure apart from the HTTP call, so it is
// testable against httptest (via the llmBaseURL/llmAPIKey/llmModel vars).
func llmChat(client *http.Client, system, user string) (string, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"model":       llmModel,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", strings.TrimRight(llmBaseURL, "/")+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+llmAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("llm http status %d", resp.StatusCode)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("llm decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}
