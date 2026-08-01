package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lib/pq"
)

// ---- config ----

var (
	vaultDir   string // only used by the one-shot `-import` command
	pgDSN      = getEnv("MDHUB_PG", "postgres://mdhub:mdhub@localhost:5432/mdhub?sslmode=disable")
	listenAddr = getEnv("MDHUB_LISTEN", ":10002")
	db         *sql.DB
	mu         sync.RWMutex
)

// ---- Document ----

type Document struct {
	Slug           string   `json:"slug"`
	FilePath       string   `json:"file_path"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	RawContent     string   `json:"raw_content,omitempty"`
	Excerpt        string   `json:"excerpt,omitempty"`
	WordCount      int      `json:"word_count"`
	Published      bool     `json:"published"`
	Source         string   `json:"source"`
	CategoryPath   string   `json:"category"`
	CategoryManual bool     `json:"category_manual,omitempty"`
	Tags           []string `json:"tags"`
	Backlinks      []string `json:"backlinks,omitempty"`
	Score          float64  `json:"score,omitempty"`
	Snippet        string   `json:"snippet,omitempty"`
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
	importDir := flag.String("import", "", "one-shot import of a vault directory into Postgres, then exit")
	flag.Parse()

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

	if *importDir != "" {
		startClassifier()
		startEmbedder()
		runImport(*importDir)
		waitClassify()
		waitEmbed()
		return
	}

	// build the in-memory search index from Postgres (the single store)
	loadIndexFromDB()
	loadEmbeddingsFromDB()

	// background LLM categorization worker (no-op without an API key)
	startClassifier()

	// background embedding worker (no-op without MDHUB_EMBED_URL)
	startEmbedder()

	// HTTP API
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", handleSearch)
	mux.HandleFunc("/api/tags", handleTags)
	mux.HandleFunc("/api/universe", handleUniverse)
	mux.HandleFunc("/api/backlinks/", handleBacklinks)
	mux.HandleFunc("/api/documents", handleDocumentList)
	mux.HandleFunc("/api/documents/", handleDocument)
	mux.HandleFunc("/api/images", handleImage)
	mux.HandleFunc("/api/reindex", handleReindex)
	mux.HandleFunc("/api/reclassify", handleReclassify)
	mux.HandleFunc("/api/reembed", handleReembed)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	log.Printf("mdhub-go listening on %s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

// ---- vault scanner (used by the import command only) ----

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

// loadIndexFromDB rebuilds the in-memory search index from all published
// documents stored in Postgres.
func loadIndexFromDB() {
	rows, err := db.Query("SELECT slug, title, content, file_mtime FROM documents WHERE published=true")
	if err != nil {
		log.Printf("load index: %v", err)
		return
	}
	defer rows.Close()

	newIndex := map[string]*searchEntry{}
	for rows.Next() {
		var e searchEntry
		var mtime time.Time
		if err := rows.Scan(&e.slug, &e.title, &e.display, &mtime); err != nil {
			continue
		}
		e.plain = strings.ToLower(e.display)
		e.mtime = mtime.UnixMilli()
		newIndex[e.slug] = &e
	}
	mu.Lock()
	searchIndex = newIndex
	mu.Unlock()
	log.Printf("index loaded, %d published docs", len(newIndex))
}

// deleteDoc removes a document from PG (tags/backlinks/comments cascade)
// and from the in-memory index. Callers must hold mu.
func deleteDoc(slug string) {
	res, err := db.Exec("DELETE FROM documents WHERE slug=$1", slug)
	if err != nil {
		log.Printf("delete %s: %v", slug, err)
		return
	}
	delete(searchIndex, slug)
	delete(embedIndex, slug)
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("removed: %s", slug)
	}
}

func parseFile(fp string) (*Document, error) {
	raw, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	return parseDoc(fileSlug(fp), fp, string(raw)), nil
}

// parseDoc derives title/tags/published/plain content/word_count/excerpt
// from raw markdown (frontmatter included).
func parseDoc(slug, filePath, raw string) *Document {
	fm, body := splitFrontmatter(raw)
	published := false
	source := "user"
	category := ""
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
		if strings.HasPrefix(line, "category:") {
			category = strings.TrimSpace(strings.TrimPrefix(line, "category:"))
			category = sanitizeCategory(strings.Trim(category, "\""))
		}
		if strings.HasPrefix(line, "tags:") {
			tags = parseTags(line)
		}
	}

	fallback := filePath
	if fallback == "" {
		fallback = slug
	}
	title := extractTitle(body, filepath.Base(fallback))
	plain := stripMarkdown(body)

	return &Document{
		Slug:           slug,
		FilePath:       filePath,
		Title:          title,
		Content:        plain,
		RawContent:     raw,
		Excerpt:        excerptOf(plain),
		WordCount:      len([]rune(plain)),
		Published:      published,
		Source:         source,
		CategoryPath:   category,
		CategoryManual: category != "",
		Tags:           tags,
		Backlinks:      extractBacklinks(body),
	}
}

// excerptOf returns the first 200 runes of plain text.
func excerptOf(plain string) string {
	r := []rune(plain)
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}

func fileSlug(fp string) string {
	// Slug is relative to the vault root (the -import dir), matching the
	// old frontend's VAULT_PATH semantics: "_translations/notes/foo".
	rel, err := filepath.Rel(vaultDir, fp)
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
		INSERT INTO documents (slug, file_path, title, content, raw_content, excerpt, word_count, published, source, category_path, category_manual, file_mtime)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
		ON CONFLICT (slug) DO UPDATE SET
			title=EXCLUDED.title, content=EXCLUDED.content,
			raw_content=EXCLUDED.raw_content, excerpt=EXCLUDED.excerpt,
			word_count=EXCLUDED.word_count, published=EXCLUDED.published,
			source=EXCLUDED.source,
			category_manual=EXCLUDED.category_manual,
			category_path = CASE WHEN EXCLUDED.category_path <> '' THEN EXCLUDED.category_path WHEN documents.category_manual AND NOT EXCLUDED.category_manual THEN '' ELSE documents.category_path END,
			file_mtime=now()`,
		doc.Slug, doc.FilePath, doc.Title, doc.Content, doc.RawContent,
		doc.Excerpt, doc.WordCount, doc.Published, doc.Source, doc.CategoryPath, doc.CategoryManual)
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

	// keyword hits (bigram substring matching)
	mu.RLock()
	kwScores := map[string]float64{}
	mtimes := map[string]int64{}
	for slug, e := range searchIndex {
		mtimes[slug] = e.mtime
		if score, ok := scoreEntry(e, terms); ok {
			kwScores[slug] = score
		}
	}
	mu.RUnlock()

	// semantic side: embed the query sentence; failures degrade to pure
	// keyword results (queryVec stays nil)
	var queryVec []float32
	if embedBaseURL != "" {
		client := &http.Client{Timeout: 8 * time.Second}
		if v, err := embedText(client, q); err != nil {
			log.Printf("search embed: %v", err)
		} else {
			queryVec = v
		}
	}

	mu.RLock()
	hits := mergeHits(kwScores, queryVec, embedIndex, mtimes)

	docs := make([]Document, 0, len(hits))
	for _, h := range hits {
		e := searchIndex[h.slug]
		if e == nil {
			continue
		}
		snippet := ""
		if h.kw {
			snippet = makeSnippet(e.display, terms)
		} else {
			// pure semantic hit: plain escaped excerpt, no <mark>
			runes := []rune(e.display)
			if len(runes) > 160 {
				runes = runes[:160]
			}
			snippet = html.EscapeString(string(runes))
		}
		docs = append(docs, Document{
			Slug:    e.slug,
			Title:   e.title,
			Score:   h.score,
			Snippet: snippet,
		})
	}
	mu.RUnlock()
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

// ---- documents API ----

type docListItem struct {
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	Excerpt      string   `json:"excerpt"`
	CategoryPath string   `json:"category"`
	Tags         []string `json:"tags"`
	Updated      int64    `json:"updated"` // file_mtime, Unix ms
}

// handleDocumentList serves GET /api/documents — published docs, newest first.
func handleDocumentList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, fmt.Errorf("method not allowed"), 405)
		return
	}
	rows, err := db.Query(`
		SELECT d.slug, d.title, d.excerpt, d.file_mtime, d.category_path,
			COALESCE(array_agg(dt.tag ORDER BY dt.tag) FILTER (WHERE dt.tag IS NOT NULL), '{}')
		FROM documents d
		LEFT JOIN document_tags dt ON dt.slug = d.slug
		WHERE d.published=true
		GROUP BY d.slug, d.title, d.excerpt, d.file_mtime, d.category_path
		ORDER BY d.file_mtime DESC`)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer rows.Close()

	items := []docListItem{}
	for rows.Next() {
		var it docListItem
		var mtime time.Time
		var tags []string
		if err := rows.Scan(&it.Slug, &it.Title, &it.Excerpt, &mtime, &it.CategoryPath, pq.Array(&tags)); err != nil {
			continue
		}
		if tags == nil {
			tags = []string{}
		}
		it.Tags = tags
		it.Updated = mtime.UnixMilli()
		items = append(items, it)
	}
	writeJSON(w, items)
}

// handleDocument dispatches /api/documents/{slug}[...] requests. A slug may
// itself contain slashes (e.g. "translations/foo"); a path ending in
// "/comments" goes to the comments handler, everything else is a document.
func handleDocument(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/documents/")
	if strings.HasSuffix(rest, "/comments") {
		slug := strings.TrimSuffix(rest, "/comments")
		handleComments(w, r, slug)
		return
	}
	slug := strings.TrimSuffix(rest, "/")
	if slug == "" {
		handleDocumentList(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		getDocument(w, r, slug)
	case http.MethodPut:
		putDocument(w, r, slug)
	case http.MethodDelete:
		deleteDocument(w, r, slug)
	default:
		httpError(w, fmt.Errorf("method not allowed"), 405)
	}
}

type docDetail struct {
	Slug         string   `json:"slug"`
	FilePath     string   `json:"file_path"`
	Title        string   `json:"title"`
	RawContent   string   `json:"raw_content"`
	Excerpt      string   `json:"excerpt"`
	WordCount    int      `json:"word_count"`
	Published    bool     `json:"published"`
	Source       string   `json:"source"`
	CategoryPath string   `json:"category"`
	Tags         []string `json:"tags"`
	Backlinks    []string `json:"backlinks"`
	Updated      int64    `json:"updated"` // file_mtime, Unix ms
}

func getDocument(w http.ResponseWriter, r *http.Request, slug string) {
	mu.RLock()
	defer mu.RUnlock()

	var d docDetail
	var mtime time.Time
	err := db.QueryRow(`
		SELECT slug, file_path, title, raw_content, excerpt, word_count, published, source, category_path, file_mtime
		FROM documents WHERE slug=$1`, slug).
		Scan(&d.Slug, &d.FilePath, &d.Title, &d.RawContent, &d.Excerpt,
			&d.WordCount, &d.Published, &d.Source, &d.CategoryPath, &mtime)
	if err != nil {
		httpError(w, fmt.Errorf("not found"), 404)
		return
	}
	d.Updated = mtime.UnixMilli()

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

// putDocument creates or replaces a document from a raw markdown body.
func putDocument(w http.ResponseWriter, r *http.Request, slug string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		httpError(w, err, 400)
		return
	}

	// keep the import-provenance file_path if the doc already exists
	var filePath string
	db.QueryRow("SELECT file_path FROM documents WHERE slug=$1", slug).Scan(&filePath)

	doc := parseDoc(slug, filePath, string(raw))
	upsert(doc)
	if doc.Published && !doc.CategoryManual && doc.CategoryPath == "" {
		enqueueInsert(doc.Slug)
	}

	now := time.Now().UnixMilli()
	if doc.Published {
		enqueueEmbed(doc.Slug)
	} else {
		db.Exec("DELETE FROM embeddings WHERE slug=$1", doc.Slug)
	}
	mu.Lock()
	if doc.Published {
		searchIndex[doc.Slug] = &searchEntry{
			slug:    doc.Slug,
			title:   doc.Title,
			plain:   strings.ToLower(doc.Content),
			display: doc.Content,
			mtime:   now,
		}
	} else {
		delete(searchIndex, doc.Slug)
		delete(embedIndex, doc.Slug)
	}
	mu.Unlock()

	writeJSON(w, map[string]string{"status": "ok"})
}

func deleteDocument(w http.ResponseWriter, r *http.Request, slug string) {
	mu.Lock()
	deleteDoc(slug)
	mu.Unlock()
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- images API ----

func handleImage(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("path")
	if key == "" || strings.Contains(key, "..") {
		httpError(w, fmt.Errorf("invalid path"), 400)
		return
	}
	var data []byte
	var mime string
	err := db.QueryRow("SELECT data, mime FROM images WHERE path=$1", key).Scan(&data, &mime)
	if err != nil {
		httpError(w, fmt.Errorf("not found"), 404)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(data)
}

// ---- comments API ----

type commentEntryJSON struct {
	Author string `json:"author"`
	Time   string `json:"time"` // "YYYY-MM-DD HH:mm", local
	Text   string `json:"text"`
}

type commentThreadJSON struct {
	ID       string             `json:"id"`
	Quote    string             `json:"quote"`
	Prefix   string             `json:"prefix"`
	Suffix   string             `json:"suffix"`
	Comments []commentEntryJSON `json:"comments"`
}

func handleComments(w http.ResponseWriter, r *http.Request, slug string) {
	switch r.Method {
	case http.MethodGet:
		getComments(w, r, slug)
	case http.MethodPost:
		postComment(w, r, slug)
	default:
		httpError(w, fmt.Errorf("method not allowed"), 405)
	}
}

func getComments(w http.ResponseWriter, r *http.Request, slug string) {
	rows, err := db.Query(`
		SELECT t.id, t.quote, t.prefix, t.suffix, e.author, e.text, e.created_at
		FROM comment_threads t
		JOIN comment_entries e ON e.thread_id = t.id
		WHERE t.slug=$1
		ORDER BY t.created_at, e.id`, slug)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer rows.Close()

	threads := []commentThreadJSON{}
	byID := map[string]int{}
	for rows.Next() {
		var id, quote, prefix, suffix, author, text string
		var at time.Time
		if err := rows.Scan(&id, &quote, &prefix, &suffix, &author, &text, &at); err != nil {
			continue
		}
		i, ok := byID[id]
		if !ok {
			threads = append(threads, commentThreadJSON{
				ID: id, Quote: quote, Prefix: prefix, Suffix: suffix,
				Comments: []commentEntryJSON{},
			})
			i = len(threads) - 1
			byID[id] = i
		}
		threads[i].Comments = append(threads[i].Comments, commentEntryJSON{
			Author: author,
			Time:   at.Local().Format("2006-01-02 15:04"),
			Text:   text,
		})
	}
	writeJSON(w, threads)
}

type newComment struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	Anchor *struct {
		Quote  string `json:"quote"`
		Prefix string `json:"prefix"`
		Suffix string `json:"suffix"`
	} `json:"anchor"`
	Reply string `json:"reply"`
}

func postComment(w http.ResponseWriter, r *http.Request, slug string) {
	// target document must exist and be published
	var published bool
	err := db.QueryRow("SELECT published FROM documents WHERE slug=$1", slug).Scan(&published)
	if err != nil || !published {
		httpError(w, fmt.Errorf("not found"), 404)
		return
	}

	var c newComment
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		httpError(w, fmt.Errorf("invalid body"), 400)
		return
	}
	c.Text = strings.TrimSpace(c.Text)
	if c.Text == "" || len([]rune(c.Text)) > 2000 {
		httpError(w, fmt.Errorf("text required, max 2000 chars"), 400)
		return
	}
	c.Author = strings.TrimSpace(c.Author)
	if c.Author == "" {
		c.Author = "用户"
	}
	if a := []rune(c.Author); len(a) > 30 {
		c.Author = string(a[:30])
	}

	if c.Reply != "" {
		var exists bool
		err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM comment_threads WHERE id=$1 AND slug=$2)", c.Reply, slug).Scan(&exists)
		if err != nil || !exists {
			httpError(w, fmt.Errorf("thread not found"), 400)
			return
		}
		if _, err := db.Exec("INSERT INTO comment_entries (thread_id, author, text) VALUES ($1,$2,$3)", c.Reply, c.Author, c.Text); err != nil {
			httpError(w, err, 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "id": c.Reply})
		return
	}

	if c.Anchor == nil || strings.TrimSpace(c.Anchor.Quote) == "" {
		httpError(w, fmt.Errorf("anchor.quote required"), 400)
		return
	}
	id := randomID()
	tx, err := db.Begin()
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO comment_threads (id, slug, quote, prefix, suffix) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING",
		id, slug, c.Anchor.Quote, c.Anchor.Prefix, c.Anchor.Suffix); err != nil {
		httpError(w, err, 500)
		return
	}
	if _, err := tx.Exec("INSERT INTO comment_entries (thread_id, author, text) VALUES ($1,$2,$3)", id, c.Author, c.Text); err != nil {
		httpError(w, err, 500)
		return
	}
	tx.Commit()
	writeJSON(w, map[string]interface{}{"ok": true, "id": id})
}

const idChars = "0123456789abcdefghijklmnopqrstuvwxyz"

// randomID mirrors the TS `Math.random().toString(36).slice(2, 8)`.
func randomID() string {
	b := make([]byte, 6)
	for i := range b {
		b[i] = idChars[rand.IntN(len(idChars))]
	}
	return string(b)
}

func handleReindex(w http.ResponseWriter, r *http.Request) {
	loadIndexFromDB()
	loadEmbeddingsFromDB()
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleReclassify clears the category of every published non-manual doc
// and queues it for tree insertion. POST only; responds with {"queued": N}.
func handleReclassify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), 405)
		return
	}
	rows, err := db.Query(
		"UPDATE documents SET category_path='' WHERE published AND NOT category_manual RETURNING slug")
	if err != nil {
		httpError(w, err, 500)
		return
	}
	queued := 0
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			enqueueInsert(slug)
			queued++
		}
	}
	rows.Close()
	writeJSON(w, map[string]int{"queued": queued})
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
