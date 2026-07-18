package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	_ "github.com/lib/pq"
)

// ---- config ----

var (
	vaultDir   = getEnv("MDHUB_VAULT", mustHome()+"/Documents/Obsidian Vault/_translations")
	pgDSN      = getEnv("MDHUB_PG", "postgres://mdhub:mdhub@localhost:5432/mdhub?sslmode=disable")
	listenAddr = getEnv("MDHUB_LISTEN", ":10002")
	db         *sql.DB
	mu         sync.RWMutex
)

// ---- Document ----

type Document struct {
	Slug      string   `json:"slug"`
	FilePath  string   `json:"file_path"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	WordCount int      `json:"word_count"`
	Published bool     `json:"published"`
	Source    string   `json:"source"`
	Tags      []string `json:"tags"`
	Backlinks []string `json:"backlinks,omitempty"`
	Score     float64  `json:"score,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
}

// ---- main ----

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC: %v", r)
			os.Exit(1)
		}
	}()
	var err error
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("starting mdhub-go")
	db, err = sql.Open("postgres", pgDSN)
	if err != nil {
		log.Fatal("pg open:", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)

	if err := db.Ping(); err != nil {
		log.Fatal("pg ping:", err)
	}
	log.Println("connected to postgres")

	// initial index — run async so HTTP starts immediately
	go func() {
		rescan()
	}()

	// start fsnotify watcher
	go watchVault()

	// HTTP API
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", handleSearch)
	mux.HandleFunc("/api/tags", handleTags)
	mux.HandleFunc("/api/backlinks/", handleBacklinks)
	mux.HandleFunc("/api/documents/", handleDocument)
	mux.HandleFunc("/api/reindex", handleReindex)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	log.Printf("mdhub-go listening on %s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

// ---- vault scanner ----

func rescan() {
	log.Printf("rescan starting, vault: %s", vaultDir)
	files, err := filepath.Glob(vaultDir + "/*.md")
	if err != nil {
		log.Printf("scan error: %v", err)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	log.Printf("scanning %d files...", len(files))
	for _, fp := range files {
		doc, err := parseFile(fp)
		if err != nil {
			log.Printf("parse %s: %v", fp, err)
			continue
		}
		if !doc.Published {
			continue
		}
		upsert(doc)
	}
	log.Printf("scan done")
}

func parseFile(fp string) (*Document, error) {
	raw, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	content := string(raw)

	fm, body := splitFrontmatter(content)
	published := false
	source := "user"
	var tags []string

	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "publish:") && strings.Contains(line, "true") {
			published = true
		}
		if strings.HasPrefix(line, "source:") {
			source = strings.TrimSpace(strings.TrimPrefix(line, "source:"))
			source = strings.Trim(source, "\"")
		}
		if strings.HasPrefix(line, "tags:") {
			tags = parseTags(line)
		}
	}

	title := extractTitle(body, filepath.Base(fp))
	plain := stripMarkdown(body)

	slug := fileSlug(fp)

	return &Document{
		Slug:      slug,
		FilePath:  fp,
		Title:     title,
		Content:   bigramText(plain),
		WordCount: len([]rune(plain)),
		Published: published,
		Source:    source,
		Tags:      tags,
		Backlinks: extractBacklinks(body),
	}, nil
}

func fileSlug(fp string) string {
	// Compute slug relative to Obsidian vault root so MDHub can resolve it
	vaultRoot := filepath.Dir(vaultDir) // e.g. ~/Documents/Obsidian Vault
	rel, err := filepath.Rel(vaultRoot, fp)
	if err != nil {
		// fallback
		return strings.TrimSuffix(filepath.Base(fp), ".md")
	}
	slug := filepath.ToSlash(rel)           // "translations/foo.md" or "_translations/foo.md"
	slug = strings.TrimSuffix(slug, ".md")  // "translations/foo"
	return slug
}

// ---- Postgres ----

func upsert(doc *Document) {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("tx begin: %v", err)
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO documents (slug, file_path, title, content, word_count, published, source, file_mtime)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (slug) DO UPDATE SET
			title=EXCLUDED.title, content=EXCLUDED.content,
			word_count=EXCLUDED.word_count, published=EXCLUDED.published,
			source=EXCLUDED.source, file_mtime=now()`,
		doc.Slug, doc.FilePath, doc.Title, doc.Content,
		doc.WordCount, doc.Published, doc.Source)
	if err != nil {
		log.Printf("upsert doc %s: %v", doc.Slug, err)
		return
	}

	// tags
	tx.Exec("DELETE FROM document_tags WHERE slug=$1", doc.Slug)
	for _, tag := range doc.Tags {
		tx.Exec("INSERT INTO tags (name) VALUES ($1) ON CONFLICT DO NOTHING", tag)
		tx.Exec("INSERT INTO document_tags (slug, tag) VALUES ($1,$2) ON CONFLICT DO NOTHING", doc.Slug, tag)
	}

	// backlinks
	tx.Exec("DELETE FROM backlinks WHERE source_slug=$1", doc.Slug)
	for _, target := range doc.Backlinks {
		targetSlug := resolveSlug(target)
		if targetSlug != "" {
			tx.Exec("INSERT INTO backlinks (source_slug, target_slug) VALUES ($1,$2) ON CONFLICT DO NOTHING", doc.Slug, targetSlug)
		}
	}

	tx.Commit()
}

func resolveSlug(ref string) string {
	var slug string
	err := db.QueryRow("SELECT slug FROM documents WHERE slug=$1", ref).Scan(&slug)
	if err == nil {
		return slug
	}
	// try fuzzy: match against title
	err = db.QueryRow("SELECT slug FROM documents WHERE lower(title) LIKE $1 LIMIT 1", "%"+strings.ToLower(ref)+"%").Scan(&slug)
	if err == nil {
		return slug
	}
	return ""
}

// ---- HTTP handlers ----

func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, []Document{})
		return
	}

	// bigram the query
	qBigram := bigramText(q)
	mu.RLock()
	defer mu.RUnlock()

	rows, err := db.Query(`
		SELECT slug, title, 
			ts_rank(content_tsv, plainto_tsquery('simple', $1)) AS score,
			ts_headline('simple', content, plainto_tsquery('simple', $1),
				'MaxWords=40, MinWords=25, StartSel=<mark>, StopSel=</mark>') AS snippet
		FROM documents
		WHERE published=true AND content_tsv @@ plainto_tsquery('simple', $1)
		ORDER BY score DESC
		LIMIT 20`, qBigram)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		var score float64
		var snippet sql.NullString
		if err := rows.Scan(&d.Slug, &d.Title, &score, &snippet); err != nil {
			continue
		}
		d.Score = score
		if snippet.Valid {
			d.Snippet = snippet.String
		}
		docs = append(docs, d)
	}
	if docs == nil {
		docs = []Document{}
	}
	writeJSON(w, docs)
}

func handleTags(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	mu.RLock()
	defer mu.RUnlock()

	if tag != "" {
		// list docs for tag
		rows, err := db.Query(`
			SELECT d.slug, d.title FROM documents d
			JOIN document_tags dt ON d.slug = dt.slug
			WHERE dt.tag=$1 AND d.published=true
			ORDER BY d.file_mtime DESC`, tag)
		if err != nil {
			httpError(w, err, 500)
			return
		}
		defer rows.Close()
		var docs []Document
		for rows.Next() {
			var d Document
			rows.Scan(&d.Slug, &d.Title)
			docs = append(docs, d)
		}
		if docs == nil {
			docs = []Document{}
		}
		writeJSON(w, docs)
		return
	}

	// list all tags with counts
	rows, err := db.Query(`
		SELECT t.name, COUNT(dt.slug) 
		FROM tags t 
		JOIN document_tags dt ON t.name = dt.tag
		JOIN documents d ON d.slug = dt.slug AND d.published=true
		GROUP BY t.name
		ORDER BY COUNT(dt.slug) DESC`)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer rows.Close()

	type TagCount struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	var tags []TagCount
	for rows.Next() {
		var tc TagCount
		rows.Scan(&tc.Name, &tc.Count)
		tags = append(tags, tc)
	}
	if tags == nil {
		tags = []TagCount{}
	}
	writeJSON(w, tags)
}

func handleBacklinks(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/backlinks/")
	slug = strings.TrimSuffix(slug, "/")
	if slug == "" {
		writeJSON(w, []Document{})
		return
	}

	mu.RLock()
	defer mu.RUnlock()

	rows, err := db.Query(`
		SELECT d.slug, d.title FROM documents d
		JOIN backlinks b ON d.slug = b.source_slug
		WHERE b.target_slug=$1 AND d.published=true`, slug)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		rows.Scan(&d.Slug, &d.Title)
		docs = append(docs, d)
	}
	if docs == nil {
		docs = []Document{}
	}
	writeJSON(w, docs)
}

func handleDocument(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/documents/")
	slug = strings.TrimSuffix(slug, "/")
	if slug == "" {
		writeJSON(w, []Document{})
		return
	}

	mu.RLock()
	defer mu.RUnlock()

	var d Document
	err := db.QueryRow(`
		SELECT slug, file_path, title, word_count, published, source
		FROM documents WHERE slug=$1`, slug).
		Scan(&d.Slug, &d.FilePath, &d.Title, &d.WordCount, &d.Published, &d.Source)
	if err != nil {
		httpError(w, fmt.Errorf("not found"), 404)
		return
	}

	// tags
	rows, _ := db.Query("SELECT tag FROM document_tags WHERE slug=$1", slug)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			rows.Scan(&t)
			d.Tags = append(d.Tags, t)
		}
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}

	// backlinks
	blRows, _ := db.Query("SELECT source_slug FROM backlinks WHERE target_slug=$1", slug)
	if blRows != nil {
		defer blRows.Close()
		for blRows.Next() {
			var s string
			blRows.Scan(&s)
			d.Backlinks = append(d.Backlinks, s)
		}
	}
	if d.Backlinks == nil {
		d.Backlinks = []string{}
	}

	writeJSON(w, d)
}

func handleReindex(w http.ResponseWriter, r *http.Request) {
	// wipe old data so slug changes don't leave orphans
	db.Exec("DELETE FROM backlinks")
	db.Exec("DELETE FROM document_tags")
	db.Exec("DELETE FROM documents")
	rescan()
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, err error, code int) {
	w.WriteHeader(code)
	writeJSON(w, map[string]string{"error": err.Error()})
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func mustHome() string {
	h, _ := os.UserHomeDir()
	return h
}

// ---- frontmatter parser ----

var fmRe = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n`)

func splitFrontmatter(raw string) (fm string, body string) {
	m := fmRe.FindStringSubmatch(raw)
	if len(m) == 2 {
		return m[1], raw[len(m[0]):]
	}
	return "", raw
}

func parseTags(line string) []string {
	line = strings.TrimPrefix(line, "tags:")
	line = strings.Trim(line, " []\"")
	if line == "" {
		return nil
	}
	parts := strings.Split(line, ",")
	var tags []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "\"")
		if p != "" {
			tags = append(tags, p)
		}
	}
	return tags
}

func extractTitle(body, fallback string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "# ") {
		return strings.TrimPrefix(lines[0], "# ")
	}
	base := strings.TrimSuffix(fallback, ".md")
	return base
}

func extractBacklinks(body string) []string {
	re := regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	matches := re.FindAllStringSubmatch(body, -1)
	var links []string
	seen := map[string]bool{}
	for _, m := range matches {
		ref := strings.SplitN(m[1], "|", 2)[0]
		ref = strings.SplitN(ref, "#", 2)[0]
		if !seen[ref] {
			links = append(links, ref)
			seen[ref] = true
		}
	}
	return links
}

// ---- markdown stripper ----

var (
	mdLink   = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]+\)`)
	mdImg    = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	mdCode   = regexp.MustCompile("(?s)```.*?```")
	mdInline = regexp.MustCompile("`[^`]+`")
	mdH      = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	mdBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalic = regexp.MustCompile(`\*([^*]+)\*`)
	mdUL     = regexp.MustCompile(`(?m)^[-*+]\s+`)
	mdOL     = regexp.MustCompile(`(?m)^\d+\.\s+`)
	mdHR     = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
	mdBlock  = regexp.MustCompile(`(?m)^>\s?`)
	mdTable  = regexp.MustCompile(`\|[^|]+\|`)
	mdFront  = regexp.MustCompile(`(?s)^---\s*\n.*?\n---\s*\n`)
)

func stripMarkdown(raw string) string {
	s := raw
	s = mdFront.ReplaceAllString(s, "")
	s = mdImg.ReplaceAllString(s, " ")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdCode.ReplaceAllString(s, " ")
	s = mdInline.ReplaceAllString(s, " ")
	s = mdH.ReplaceAllString(s, "")
	s = mdBold.ReplaceAllString(s, "$1")
	s = mdItalic.ReplaceAllString(s, "$1")
	s = mdUL.ReplaceAllString(s, "")
	s = mdOL.ReplaceAllString(s, "")
	s = mdHR.ReplaceAllString(s, "")
	s = mdBlock.ReplaceAllString(s, "")
	s = mdTable.ReplaceAllString(s, " ")

	// collapse whitespace
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ---- Chinese bigram tokenizer ----

func bigramText(text string) string {
	var out strings.Builder
	runes := []rune(text)

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		// CJK character
		if unicode.Is(unicode.Han, r) {
			out.WriteRune(r)
			out.WriteByte(' ')
			// also output bigram
			if i+1 < len(runes) && unicode.Is(unicode.Han, runes[i+1]) {
				out.WriteRune(r)
				out.WriteRune(runes[i+1])
				out.WriteByte(' ')
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		} else if r == ' ' || r == '\t' || r == '\n' {
			out.WriteByte(' ')
		} else {
			out.WriteByte(' ')
		}
	}
	return strings.TrimSpace(out.String())
}

func init() {
	// ensure CJK characters work in regex
	_ = utf8.RuneSelf
}

// ---- fsnotify ----

func watchVault() {
	// poll-based: check mtime every 30s, simpler than fsnotify cross-platform
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// track known files and their mtimes
	known := map[string]time.Time{}

	for range ticker.C {
		files, err := filepath.Glob(vaultDir + "/*.md")
		if err != nil {
			continue
		}
		changed := false
		for _, fp := range files {
			info, err := os.Stat(fp)
			if err != nil {
				continue
			}
			prev, exists := known[fp]
			if !exists || info.ModTime().After(prev) {
				known[fp] = info.ModTime()
				changed = true
				doc, err := parseFile(fp)
				if err != nil {
					continue
				}
				if doc.Published {
					mu.Lock()
					upsert(doc)
					mu.Unlock()
					log.Printf("updated: %s", doc.Slug)
				}
			}
		}
		if changed {
			log.Println("vault change detected, reindexed")
		}
	}
}
