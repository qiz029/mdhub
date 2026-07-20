package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// ---- in-memory search index ----
//
// Matching is done in-process rather than with PostgreSQL tsvector:
// PG's default text search parser classifies CJK characters as "blank"
// (space) whenever the platform libc doesn't report them as letters —
// which is the case on macOS regardless of locale/provider, silently
// emptying every tsvector and making Chinese search return nothing.
// A vault is small (hundreds to low thousands of notes), so in-memory
// substring matching over the plain text is simple, fast and portable.
// PostgreSQL remains the store for documents/tags/backlinks.

type searchEntry struct {
	slug    string
	title   string
	plain   string // lowercased, for matching
	display string // original plain text, for snippets
	mtime   int64  // file mtime, ms
}

var searchIndex = map[string]*searchEntry{}

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

// scanVaultFiles walks the vault recursively, skipping hidden directories
// and node_modules — same rules as the Next.js frontend.
func scanVaultFiles() []string {
	var files []string
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
		if strings.HasSuffix(d.Name(), ".md") {
			files = append(files, p)
		}
		return nil
	})
	return files
}

func rescan() {
	log.Printf("rescan starting, vault: %s", vaultDir)
	files := scanVaultFiles()
	log.Printf("scanning %d files...", len(files))

	mu.Lock()
	defer mu.Unlock()

	newIndex := make(map[string]*searchEntry, len(files))
	seen := make(map[string]bool, len(files))
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
		seen[doc.Slug] = true
		newIndex[doc.Slug] = &searchEntry{
			slug:    doc.Slug,
			title:   doc.Title,
			plain:   strings.ToLower(doc.Content),
			display: doc.Content,
			mtime:   fileMtime(fp),
		}
	}
	searchIndex = newIndex
	deleteStaleDocs(seen)
	log.Printf("scan done, %d published docs indexed", len(newIndex))
}

func fileMtime(fp string) int64 {
	if info, err := os.Stat(fp); err == nil {
		return info.ModTime().UnixMilli()
	}
	return 0
}

// deleteStaleDocs removes rows whose files disappeared or are no longer
// published, so results converge with the vault on disk.
func deleteStaleDocs(seen map[string]bool) {
	rows, err := db.Query("SELECT slug FROM documents")
	if err != nil {
		log.Printf("list slugs: %v", err)
		return
	}
	var slugs []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			slugs = append(slugs, s)
		}
	}
	rows.Close()
	for _, s := range slugs {
		if !seen[s] {
			deleteDoc(s)
		}
	}
}

// deleteDoc removes a document from PG (tags/backlinks cascade) and from
// the in-memory index. Callers must hold mu.
func deleteDoc(slug string) {
	res, err := db.Exec("DELETE FROM documents WHERE slug=$1", slug)
	if err != nil {
		log.Printf("delete %s: %v", slug, err)
		return
	}
	delete(searchIndex, slug)
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("removed from index: %s", slug)
	}
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
		Content:   plain,
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
	slug := filepath.ToSlash(rel)          // "translations/foo.md" or "_translations/foo.md"
	slug = strings.TrimSuffix(slug, ".md") // "translations/foo"
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
	terms := buildTerms(q)
	if len(terms) == 0 {
		writeJSON(w, []Document{})
		return
	}

	type hit struct {
		e     *searchEntry
		score float64
	}
	mu.RLock()
	var hits []hit
	for _, e := range searchIndex {
		if score, ok := scoreEntry(e, terms); ok {
			hits = append(hits, hit{e, score})
		}
	}
	mu.RUnlock()

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].e.mtime > hits[j].e.mtime
	})
	if len(hits) > 20 {
		hits = hits[:20]
	}

	docs := make([]Document, 0, len(hits))
	for _, h := range hits {
		docs = append(docs, Document{
			Slug:    h.e.slug,
			Title:   h.e.title,
			Score:   h.score,
			Snippet: makeSnippet(h.e.display, terms),
		})
	}
	writeJSON(w, docs)
}

// buildTerms converts a raw query into match terms: CJK text is expanded
// into unigram+bigram tokens, everything is lowercased. A document matches
// only when it contains every term.
func buildTerms(q string) []string {
	return strings.Fields(strings.ToLower(bigramText(q)))
}

func scoreEntry(e *searchEntry, terms []string) (float64, bool) {
	lowerTitle := strings.ToLower(e.title)
	var score float64
	for _, t := range terms {
		body := strings.Count(e.plain, t)
		title := strings.Count(lowerTitle, t)
		if body+title == 0 {
			return 0, false
		}
		score += float64(body) + 5*float64(title)
	}
	return score, true
}

// makeSnippet returns an HTML-escaped excerpt around the first occurrence
// of the longest (most specific) term, with that term wrapped in <mark>.
func makeSnippet(content string, terms []string) string {
	key := ""
	for _, t := range terms {
		if len(t) > len(key) {
			key = t
		}
	}
	idx := strings.Index(strings.ToLower(content), key)
	if idx < 0 {
		idx = 0
	}
	runes := []rune(content)
	start := utf8.RuneCountInString(content[:idx]) - 40
	if start < 0 {
		start = 0
	}
	end := start + 160
	if end > len(runes) {
		end = len(runes)
	}
	window := string(runes[start:end])
	rel := strings.Index(strings.ToLower(window), key)
	if rel < 0 {
		return html.EscapeString(window)
	}
	return html.EscapeString(window[:rel]) + "<mark>" +
		html.EscapeString(window[rel:rel+len(key)]) + "</mark>" +
		html.EscapeString(window[rel+len(key):])
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
		files := scanVaultFiles()
		current := make(map[string]bool, len(files))
		for _, fp := range files {
			current[fp] = true
			info, err := os.Stat(fp)
			if err != nil {
				continue
			}
			prev, exists := known[fp]
			if exists && !info.ModTime().After(prev) {
				continue
			}
			known[fp] = info.ModTime()

			doc, err := parseFile(fp)
			if err != nil {
				continue
			}
			mu.Lock()
			if doc.Published {
				upsert(doc)
				searchIndex[doc.Slug] = &searchEntry{
					slug:    doc.Slug,
					title:   doc.Title,
					plain:   strings.ToLower(doc.Content),
					display: doc.Content,
					mtime:   fileMtime(fp),
				}
				log.Printf("updated: %s", doc.Slug)
			} else {
				// publish removed (or file unparsable as frontmatter):
				// drop from the index so stale notes stop showing up
				deleteDoc(doc.Slug)
			}
			mu.Unlock()
		}
		// files deleted from disk
		for fp := range known {
			if !current[fp] {
				delete(known, fp)
				mu.Lock()
				deleteDoc(fileSlug(fp))
				mu.Unlock()
			}
		}
	}
}
