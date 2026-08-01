package main

// One-shot vault import: `mdhub-go -import <vault-dir>` copies all
// documents (published or not), images and _comments/ files into Postgres,
// then exits. Idempotent — safe to re-run.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var imageMimes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".bmp":  "image/bmp",
	".avif": "image/avif",
	".ico":  "image/x-icon",
}

func isReservedUploadPath(key string) bool {
	return key == "uploads" || strings.HasPrefix(key, "uploads/")
}

func runImport(dir string) {
	vaultDir = dir
	var docs, images, threads, replies int

	// documents — everything except _comments/
	for _, fp := range scanVaultFiles() {
		if isCommentsFile(fp) {
			continue
		}
		doc, err := parseFile(fp)
		if err != nil {
			log.Printf("parse %s: %v", fp, err)
			continue
		}
		upsert(doc)
		if doc.Published && !doc.CategoryManual && doc.CategoryPath == "" {
			enqueueInsert(doc.Slug)
		}
		if doc.Published {
			enqueueEmbed(doc.Slug)
		}
		docs++
	}

	// images
	root := filepath.Clean(vaultDir)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		mime, ok := imageMimes[strings.ToLower(filepath.Ext(d.Name()))]
		if !ok {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			log.Printf("read image %s: %v", p, err)
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if isReservedUploadPath(key) {
			log.Printf("skip reserved upload image path %s", key)
			return nil
		}
		if _, err := db.Exec(`
			INSERT INTO images (path, data, mime) VALUES ($1,$2,$3)
			ON CONFLICT (path) DO UPDATE SET data=EXCLUDED.data, mime=EXCLUDED.mime, updated_at=now()`,
			key, data, mime); err != nil {
			log.Printf("import image %s: %v", key, err)
			return nil
		}
		images++
		return nil
	})

	// comments — only for slugs that exist in documents (FK constraint)
	for _, fp := range scanVaultFiles() {
		if !isCommentsFile(fp) {
			continue
		}
		raw, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		slug := commentSlug(fp, string(raw))
		var exists bool
		db.QueryRow("SELECT EXISTS(SELECT 1 FROM documents WHERE slug=$1)", slug).Scan(&exists)
		if !exists {
			log.Printf("skip comments for unknown doc: %s", slug)
			continue
		}
		t, r := importComments(slug, string(raw))
		threads += t
		replies += r
	}

	fmt.Printf("import done: %d docs, %d images, %d comment threads, %d replies\n",
		docs, images, threads, replies)
}

// isCommentsFile reports whether fp lives in the vault's _comments/ dir.
func isCommentsFile(fp string) bool {
	rel, err := filepath.Rel(vaultDir, fp)
	if err != nil {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(rel), "_comments/")
}

// commentSlug resolves the note a comments file belongs to: the frontmatter
// `note:` line wins, otherwise the file path relative to _comments/.
func commentSlug(fp, raw string) string {
	fm, _ := splitFrontmatter(raw)
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "note:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "note:")), "\"")
		}
	}
	rel, err := filepath.Rel(filepath.Join(vaultDir, "_comments"), fp)
	if err != nil {
		return strings.TrimSuffix(filepath.Base(fp), ".md")
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), ".md")
}

// importComments replaces all comment threads of a slug with the parsed
// content of its _comments file. Returns thread and reply counts.
func importComments(slug, raw string) (threads, replies int) {
	parsed := parseCommentThreads(raw)
	if len(parsed) == 0 {
		return 0, 0
	}
	tx, err := db.Begin()
	if err != nil {
		log.Printf("comments tx: %v", err)
		return 0, 0
	}
	defer tx.Rollback()

	// delete-then-insert keeps re-runs idempotent (entries cascade)
	if _, err := tx.Exec("DELETE FROM comment_threads WHERE slug=$1", slug); err != nil {
		log.Printf("clear comments %s: %v", slug, err)
		return 0, 0
	}
	for _, t := range parsed {
		created := t.entries[0].at
		if _, err := tx.Exec(`
			INSERT INTO comment_threads (id, slug, quote, prefix, suffix, created_at)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (id) DO NOTHING`,
			t.id, slug, t.quote, t.prefix, t.suffix, created); err != nil {
			log.Printf("insert thread %s: %v", t.id, err)
			continue
		}
		for i, e := range t.entries {
			if _, err := tx.Exec("INSERT INTO comment_entries (thread_id, author, text, created_at) VALUES ($1,$2,$3,$4)",
				t.id, e.author, e.text, e.at); err != nil {
				log.Printf("insert entry %s: %v", t.id, err)
				continue
			}
			if i > 0 {
				replies++
			}
		}
		threads++
	}
	tx.Commit()
	return threads, replies
}

// ---- comments markdown parser (Go port of src/lib/comments.ts) ----

type commentSection struct {
	author string
	at     time.Time
	meta   map[string]string
	text   string
}

type parsedEntry struct {
	author string
	at     time.Time
	text   string
}

type parsedThread struct {
	id      string
	quote   string
	prefix  string
	suffix  string
	entries []parsedEntry
}

var (
	commentHeadingRe = regexp.MustCompile(`^## (.*?) · (\d{4}-\d{2}-\d{2} \d{2}:\d{2})\s*$`)
	commentMetaRe    = regexp.MustCompile(`(?s)^<!--\s*(\{.*?\})\s*-->`)
)

// parseCommentSections splits a _comments file into sections, one per
// "## author · YYYY-MM-DD HH:mm" heading.
func parseCommentSections(md string) []commentSection {
	var sections []commentSection
	var cur *commentSection
	var bodyLines []string

	flush := func() {
		if cur == nil {
			return
		}
		text := strings.TrimSpace(strings.Join(bodyLines, "\n"))
		if m := commentMetaRe.FindStringSubmatch(text); m != nil {
			_ = json.Unmarshal([]byte(m[1]), &cur.meta)
			text = strings.TrimSpace(text[len(m[0]):])
		}
		cur.text = text
		sections = append(sections, *cur)
	}

	for _, line := range strings.Split(md, "\n") {
		if m := commentHeadingRe.FindStringSubmatch(line); m != nil {
			flush()
			at, err := time.ParseInLocation("2006-01-02 15:04", m[2], time.Local)
			if err != nil {
				at = time.Now()
			}
			cur = &commentSection{author: m[1], at: at, meta: map[string]string{}}
			bodyLines = nil
			continue
		}
		if cur != nil {
			bodyLines = append(bodyLines, line)
		}
	}
	flush()
	return sections
}

// parseCommentThreads groups sections into threads: a section whose meta
// carries id+quote opens a thread, one carrying reply appends to it.
func parseCommentThreads(md string) []parsedThread {
	var threads []parsedThread
	byID := map[string]int{}
	for _, s := range parseCommentSections(md) {
		if rid := s.meta["reply"]; rid != "" {
			if i, ok := byID[rid]; ok {
				threads[i].entries = append(threads[i].entries, parsedEntry{s.author, s.at, s.text})
			}
			continue
		}
		id, quote := s.meta["id"], s.meta["quote"]
		if id == "" || quote == "" {
			continue
		}
		byID[id] = len(threads)
		threads = append(threads, parsedThread{
			id:      id,
			quote:   quote,
			prefix:  s.meta["prefix"],
			suffix:  s.meta["suffix"],
			entries: []parsedEntry{{s.author, s.at, s.text}},
		})
	}
	return threads
}
