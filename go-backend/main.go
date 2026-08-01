package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"image"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	"github.com/lib/pq"
	"golang.org/x/image/draw"
)

const imageTranscodeWorkerEnv = "MDHUB_INTERNAL_IMAGE_TRANSCODE_WORKER"

func init() {
	if os.Getenv(imageTranscodeWorkerEnv) != "1" {
		return
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxImageUploadBytes+1))
	if err == nil && int64(len(data)) > maxImageUploadBytes {
		err = fmt.Errorf("worker input exceeds 20 MB")
	}
	mime := detectUploadImageMIME(data)
	if err == nil && mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
		err = fmt.Errorf("worker received unsupported image type")
	}
	var output []byte
	if err == nil {
		output, err = transcodeStaticImage(data, mime)
	}
	if err == nil {
		_, err = os.Stdout.Write(output)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// ---- config ----

var (
	vaultDir   string // only used by the one-shot `-import` command
	pgDSN      = getEnv("MDHUB_PG", "postgres://mdhub:mdhub@localhost:5432/mdhub?sslmode=disable")
	listenAddr = getEnv("MDHUB_LISTEN", "127.0.0.1:10002")
	editToken  = getEnv("MDHUB_EDIT_TOKEN", "")
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
	Kind           string   `json:"kind"` // "note" | "fleeting"
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

const maxDocumentBytes int64 = 32 << 20

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
	if *importDir == "" {
		if err := validateIngressConfig(listenAddr, editToken); err != nil {
			log.Fatal(err)
		}
	}
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
		startCollide()
		runImport(*importDir)
		waitClassify()
		waitEmbed()
		waitCollide()
		return
	}

	// build the in-memory search index from Postgres (the single store)
	loadIndexFromDB()
	loadEmbeddingsFromDB()

	// background LLM categorization worker (no-op without an API key)
	startClassifier()

	// background embedding worker (no-op without MDHUB_EMBED_URL)
	startEmbedder()

	// background collision worker (no-op without MDHUB_EMBED_URL)
	startCollide()

	// HTTP API
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", handleSearch)
	mux.HandleFunc("/api/tags", handleTags)
	mux.HandleFunc("/api/universe", handleUniverse)
	mux.HandleFunc("/api/related", handleRelatedDocuments)
	mux.HandleFunc("/api/backlinks/", handleBacklinks)
	mux.HandleFunc("/api/documents", handleDocumentList)
	mux.HandleFunc("/api/documents/", handleDocument)
	mux.HandleFunc("/api/sparks", handleSparks)
	mux.HandleFunc("/api/collisions", handleCollisions)
	mux.HandleFunc("/api/collisions/", handleCollision)
	mux.HandleFunc("/api/recollide", handleRecollide)
	mux.HandleFunc("/api/images", handleImage)
	mux.HandleFunc("/api/reindex", handleReindex)
	mux.HandleFunc("/api/reclassify", handleReclassify)
	mux.HandleFunc("/api/reembed", handleReembed)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	log.Printf("mdhub-go listening on %s", listenAddr)
	log.Fatal(server.ListenAndServe())
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
			log.Printf("scan search index: %v", err)
			return
		}
		e.plain = strings.ToLower(e.display)
		e.mtime = mtime.UnixMilli()
		newIndex[e.slug] = &e
	}
	if err := rows.Err(); err != nil {
		log.Printf("read search index: %v", err)
		return
	}
	mu.Lock()
	searchIndex = newIndex
	mu.Unlock()
	log.Printf("index loaded, %d published docs", len(newIndex))
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
	kind := "note"
	source := "user"
	category := ""
	var tags []string

	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := frontmatterValue(line, "publish"); ok {
			published = strings.EqualFold(strings.Trim(value, "\"'"), "true")
		}
		if value, ok := frontmatterValue(line, "type"); ok {
			if strings.EqualFold(strings.Trim(value, "\"'"), "fleeting") {
				kind = "fleeting"
			}
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
		Kind:           kind,
		Source:         source,
		CategoryPath:   category,
		CategoryManual: category != "",
		Tags:           tags,
		Backlinks:      extractBacklinks(body),
	}
}

// frontmatterValue returns the exact scalar value for a top-level key. The
// parser intentionally supports only MDHub's small frontmatter contract; in
// particular, substring matches such as `publish: untrue` must never make a
// document public.
func frontmatterValue(line, key string) (string, bool) {
	name, value, ok := strings.Cut(line, ":")
	if !ok || strings.TrimSpace(name) != key {
		return "", false
	}
	return strings.TrimSpace(value), true
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
			if err := rows.Scan(&d.Slug, &d.Title); err != nil {
				httpError(w, err, http.StatusInternalServerError)
				return
			}
			docs = append(docs, d)
		}
		if err := rows.Err(); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
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
		if err := rows.Scan(&tc.Name, &tc.Count); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		tags = append(tags, tc)
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
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
		if err := rows.Scan(&d.Slug, &d.Title); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
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
	Kind         string   `json:"kind"`
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
		SELECT d.slug, d.title, d.excerpt, d.file_mtime, d.category_path, d.kind,
			COALESCE(array_agg(dt.tag ORDER BY dt.tag) FILTER (WHERE dt.tag IS NOT NULL), '{}')
		FROM documents d
		LEFT JOIN document_tags dt ON dt.slug = d.slug
		WHERE d.published=true
		GROUP BY d.slug, d.title, d.excerpt, d.file_mtime, d.category_path, d.kind
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
		if err := rows.Scan(&it.Slug, &it.Title, &it.Excerpt, &mtime, &it.CategoryPath, &it.Kind, pq.Array(&tags)); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		if tags == nil {
			tags = []string{}
		}
		it.Tags = tags
		it.Updated = mtime.UnixMilli()
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
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
		if !requireEditAccess(w, r) {
			return
		}
		putDocument(w, r, slug)
	case http.MethodDelete:
		if !requireEditAccess(w, r) {
			return
		}
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
	Kind         string   `json:"kind"`
	Source       string   `json:"source"`
	CategoryPath string   `json:"category"`
	Tags         []string `json:"tags"`
	Backlinks    []string `json:"backlinks"`
	Updated      int64    `json:"updated"` // file_mtime, Unix ms
}

func getDocument(w http.ResponseWriter, r *http.Request, slug string) {
	var d docDetail
	var mtime time.Time
	err := db.QueryRow(`
		SELECT slug, file_path, title, raw_content, excerpt, word_count, published, kind, source, category_path, file_mtime
		FROM documents WHERE slug=$1`, slug).
		Scan(&d.Slug, &d.FilePath, &d.Title, &d.RawContent, &d.Excerpt,
			&d.WordCount, &d.Published, &d.Kind, &d.Source, &d.CategoryPath, &mtime)
	if err != nil {
		httpError(w, fmt.Errorf("not found"), 404)
		return
	}
	// sparks are publicly readable (see collide.go); unpublished notes stay
	// hidden behind the edit token
	if !d.Published && d.Kind != "fleeting" && !hasEditAccess(r) {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	d.Updated = mtime.UnixMilli()

	// tags
	rows, err := db.Query("SELECT tag FROM document_tags WHERE slug=$1", slug)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		d.Tags = append(d.Tags, tag)
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}

	// backlinks
	blRows, err := db.Query("SELECT source_slug FROM backlinks WHERE target_slug=$1", slug)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer blRows.Close()
	for blRows.Next() {
		var source string
		if err := blRows.Scan(&source); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		d.Backlinks = append(d.Backlinks, source)
	}
	if err := blRows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if d.Backlinks == nil {
		d.Backlinks = []string{}
	}

	writeJSON(w, d)
}

// putDocument creates or replaces a document from a raw markdown body.
func putDocument(w http.ResponseWriter, r *http.Request, slug string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpError(w, fmt.Errorf("document exceeds 32 MB"), http.StatusRequestEntityTooLarge)
			return
		}
		httpError(w, err, 400)
		return
	}

	// keep the import-provenance file_path if the doc already exists
	var filePath string
	if err := db.QueryRow("SELECT file_path FROM documents WHERE slug=$1", slug).Scan(&filePath); err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpError(w, fmt.Errorf("read document provenance: %w", err), http.StatusInternalServerError)
		return
	}

	doc := parseDoc(slug, filePath, string(raw))
	if err := publishDocument(doc); err != nil {
		httpError(w, fmt.Errorf("write document %s: %w", slug, err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

func deleteDocument(w http.ResponseWriter, r *http.Request, slug string) {
	removed, err := removeDocument(slug)
	if err != nil {
		httpError(w, fmt.Errorf("delete document %s: %w", slug, err), http.StatusInternalServerError)
		return
	}
	if removed {
		log.Printf("removed: %s", slug)
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- images API ----

const (
	maxImageUploadBytes   int64 = 20 << 20
	optimizeAboveBytes          = 5 << 20
	maxImageDimension           = 2560
	maxDecodedImagePixels       = 20_000_000
)

var imageTranscodeSlots = make(chan struct{}, 1)

var uploadImageExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"image/avif": ".avif",
}

func detectUploadImageMIME(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(data) >= 3 && bytes.Equal(data[:3], []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		limit := min(len(data), 32)
		for offset := 8; offset+4 <= limit; offset += 4 {
			brand := string(data[offset : offset+4])
			if brand == "avif" || brand == "avis" {
				return "image/avif"
			}
		}
	}
	return ""
}

func uploadedImagePath(data []byte, mime string) string {
	sum := sha256.Sum256(data)
	hash := fmt.Sprintf("%x", sum)
	return "uploads/" + hash[:2] + "/" + hash + uploadImageExtensions[mime]
}

type imageUploadResponse struct {
	Path string `json:"path"`
	Href string `json:"href"`
	MIME string `json:"mime"`
	Size int    `json:"size"`
}

type parsedImageUpload struct {
	data []byte
	mime string
	path string
}

func isContentAddressedUploadPath(key string, data []byte, mime string) bool {
	if _, supported := uploadImageExtensions[mime]; !supported {
		return false
	}
	return key == uploadedImagePath(data, mime)
}

func editTokenValid(r *http.Request) bool {
	providedHash := sha256.Sum256([]byte(r.Header.Get("X-MDHub-Edit-Token")))
	expectedHash := sha256.Sum256([]byte(editToken))
	return editToken != "" && subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}

func isLoopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateIngressConfig(address, token string) error {
	if !isLoopbackListenAddress(address) && token == "" {
		return fmt.Errorf("MDHUB_EDIT_TOKEN is required when MDHUB_LISTEN is not loopback")
	}
	return nil
}

func hasEditAccess(r *http.Request) bool {
	if editToken == "" {
		return isLoopbackListenAddress(listenAddr)
	}
	return editTokenValid(r)
}

func requireEditAccess(w http.ResponseWriter, r *http.Request) bool {
	if hasEditAccess(r) {
		return true
	}
	if editToken == "" {
		httpError(w, fmt.Errorf("editing disabled; configure MDHUB_EDIT_TOKEN"), http.StatusServiceUnavailable)
		return false
	}
	httpError(w, fmt.Errorf("invalid edit token"), http.StatusUnauthorized)
	return false
}

func handleImage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		getImage(w, r)
	case http.MethodPost:
		uploadImage(w, r)
	default:
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
	}
}

func getImage(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if isContentAddressedUploadPath(key, data, mime) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=300")
	}
	w.Write(data)
}

func uploadImage(w http.ResponseWriter, r *http.Request) {
	if editToken == "" {
		httpError(w, fmt.Errorf("image uploads disabled; configure MDHUB_EDIT_TOKEN"), http.StatusServiceUnavailable)
		return
	}
	if !editTokenValid(r) {
		httpError(w, fmt.Errorf("invalid edit token"), http.StatusUnauthorized)
		return
	}

	upload, code, err := parseImageUpload(w, r)
	if err != nil {
		httpError(w, err, code)
		return
	}

	result, err := db.Exec(`
		INSERT INTO images (path, data, mime) VALUES ($1, $2, $3)
		ON CONFLICT (path) DO NOTHING`, upload.path, upload.data, upload.mime)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if inserted, _ := result.RowsAffected(); inserted > 0 {
		status = http.StatusCreated
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(imageUploadResponse{
		Path: upload.path,
		Href: "/" + upload.path,
		MIME: upload.mime,
		Size: len(upload.data),
	})
}

func parseImageUpload(w http.ResponseWriter, r *http.Request) (parsedImageUpload, int, error) {
	// Allow room for multipart headers while keeping the file itself capped at
	// 20 MiB. MaxBytesReader also prevents oversized bodies from spilling an
	// unbounded amount of data to temporary files.
	r.Body = http.MaxBytesReader(w, r.Body, maxImageUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(maxImageUploadBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return parsedImageUpload{}, http.StatusRequestEntityTooLarge, fmt.Errorf("image upload exceeds 20 MB")
		}
		return parsedImageUpload{}, http.StatusBadRequest, fmt.Errorf("invalid multipart upload")
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return parsedImageUpload{}, http.StatusBadRequest, fmt.Errorf("missing image file")
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxImageUploadBytes+1))
	if err != nil {
		return parsedImageUpload{}, http.StatusBadRequest, fmt.Errorf("read image: %w", err)
	}
	if int64(len(data)) > maxImageUploadBytes {
		return parsedImageUpload{}, http.StatusRequestEntityTooLarge, fmt.Errorf("image upload exceeds 20 MB")
	}
	mime := detectUploadImageMIME(data)
	if mime == "" {
		return parsedImageUpload{}, http.StatusUnsupportedMediaType, fmt.Errorf("unsupported image type; use PNG, JPEG, GIF, WebP, or AVIF")
	}
	data, mime, err = optimizeUploadedImage(data, mime)
	if err != nil {
		return parsedImageUpload{}, http.StatusUnprocessableEntity, err
	}
	if int64(len(data)) > maxImageUploadBytes {
		return parsedImageUpload{}, http.StatusRequestEntityTooLarge, fmt.Errorf("processed image exceeds 20 MB")
	}
	key := uploadedImagePath(data, mime)
	return parsedImageUpload{data: data, mime: mime, path: key}, http.StatusOK, nil
}

func optimizeUploadedImage(data []byte, mime string) ([]byte, string, error) {
	var (
		config image.Config
		err    error
	)
	switch mime {
	case "image/avif":
		config, err = avif.DecodeConfig(bytes.NewReader(data))
	case "image/gif":
		config, err = gif.DecodeConfig(bytes.NewReader(data))
	case "image/webp":
		config, err = webp.DecodeConfig(bytes.NewReader(data))
	default:
		config, _, err = image.DecodeConfig(bytes.NewReader(data))
	}
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return nil, "", fmt.Errorf("invalid %s image data", strings.TrimPrefix(mime, "image/"))
	}
	if mime == "image/webp" && isAnimatedWebP(data) {
		return nil, "", fmt.Errorf("animated WebP is not supported; use GIF or a static WebP")
	}
	if int64(config.Width)*int64(config.Height) > maxDecodedImagePixels {
		return nil, "", fmt.Errorf("image dimensions are too large")
	}
	if mime == "image/gif" || mime == "image/avif" {
		return data, mime, nil
	}
	if len(data) <= optimizeAboveBytes && max(config.Width, config.Height) <= maxImageDimension {
		return data, mime, nil
	}
	select {
	case imageTranscodeSlots <- struct{}{}:
		defer func() { <-imageTranscodeSlots }()
	case <-time.After(30 * time.Second):
		return nil, "", fmt.Errorf("image optimizer is busy; try again")
	}

	encoded, err := transcodeImageWithTimeout(data)
	if err != nil {
		return nil, "", err
	}
	return encoded, "image/webp", nil
}

func transcodeImageWithTimeout(data []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate image worker: %w", err)
	}
	cmd := exec.CommandContext(ctx, executable)
	cmd.Env = appendWithoutEnv(os.Environ(), imageTranscodeWorkerEnv, "1")
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("image transcode timed out")
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("image transcode failed: %s", message)
	}
	if int64(len(output)) > maxImageUploadBytes {
		return nil, fmt.Errorf("processed image exceeds 20 MB")
	}
	return output, nil
}

func appendWithoutEnv(environment []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func transcodeStaticImage(data []byte, mime string) ([]byte, error) {
	var (
		source image.Image
		err    error
	)
	if mime == "image/webp" {
		source, err = webp.Decode(bytes.NewReader(data))
	} else {
		source, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	if longest := max(width, height); longest > maxImageDimension {
		width = max(1, width*maxImageDimension/longest)
		height = max(1, height*maxImageDimension/longest)
	}
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(destination, destination.Bounds(), source, source.Bounds(), draw.Over, nil)

	var encoded bytes.Buffer
	if err := webp.Encode(&encoded, destination, webp.Options{Quality: 82, Method: 4}); err != nil {
		return nil, fmt.Errorf("encode WebP: %w", err)
	}
	return encoded.Bytes(), nil
}

func isAnimatedWebP(data []byte) bool {
	if len(data) < 12 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		return false
	}
	for offset := 12; offset+8 <= len(data); {
		chunkSize := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if bytes.Equal(data[offset:offset+4], []byte("ANIM")) || bytes.Equal(data[offset:offset+4], []byte("ANMF")) {
			return true
		}
		next := uint64(offset) + 8 + chunkSize + chunkSize%2
		if next > uint64(len(data)) || next <= uint64(offset) {
			return false
		}
		offset = int(next)
	}
	return false
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
		JOIN documents d ON d.slug = t.slug
		WHERE t.slug=$1 AND (d.published OR $2)
		ORDER BY t.created_at, e.id`, slug, hasEditAccess(r))
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
			httpError(w, err, http.StatusInternalServerError)
			return
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
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, threads)
}

type newComment struct {
	Author string         `json:"author"`
	Text   string         `json:"text"`
	Anchor *commentAnchor `json:"anchor"`
	Reply  string         `json:"reply"`
}

type commentAnchor struct {
	Quote  string `json:"quote"`
	Prefix string `json:"prefix"`
	Suffix string `json:"suffix"`
}

const maxCommentBodyBytes int64 = 16 << 10

func postComment(w http.ResponseWriter, r *http.Request, slug string) {
	var published bool
	err := db.QueryRow("SELECT published FROM documents WHERE slug=$1", slug).Scan(&published)
	if err != nil || !published {
		httpError(w, fmt.Errorf("not found"), 404)
		return
	}

	c, status, err := decodeNewComment(w, r)
	if err != nil {
		httpError(w, err, status)
		return
	}
	if err := normalizeNewComment(&c); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if c.Reply != "" {
		postCommentReply(w, slug, c)
		return
	}
	postCommentThread(w, slug, c)
}

func decodeNewComment(w http.ResponseWriter, r *http.Request) (newComment, int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCommentBodyBytes)
	var comment newComment
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&comment); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return newComment{}, http.StatusRequestEntityTooLarge, fmt.Errorf("comment body exceeds 16 KB")
		}
		return newComment{}, http.StatusBadRequest, fmt.Errorf("invalid body")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return newComment{}, http.StatusBadRequest, fmt.Errorf("invalid body")
	}
	return comment, http.StatusOK, nil
}

func normalizeNewComment(comment *newComment) error {
	comment.Text = strings.TrimSpace(comment.Text)
	if comment.Text == "" || len([]rune(comment.Text)) > 2000 {
		return fmt.Errorf("text required, max 2000 chars")
	}
	comment.Author = strings.TrimSpace(comment.Author)
	if comment.Author == "" {
		comment.Author = "用户"
	}
	comment.Author = truncateRunes(comment.Author, 30)
	if comment.Reply != "" {
		if len([]rune(comment.Reply)) > 20 {
			return fmt.Errorf("invalid reply id")
		}
		return nil
	}
	if comment.Anchor == nil || strings.TrimSpace(comment.Anchor.Quote) == "" {
		return fmt.Errorf("anchor.quote required")
	}
	comment.Anchor.Quote = strings.TrimSpace(comment.Anchor.Quote)
	if len([]rune(comment.Anchor.Quote)) > 500 {
		return fmt.Errorf("anchor.quote max 500 chars")
	}
	comment.Anchor.Prefix = truncateRunes(comment.Anchor.Prefix, 80)
	comment.Anchor.Suffix = truncateRunes(comment.Anchor.Suffix, 80)
	return nil
}

func postCommentReply(w http.ResponseWriter, slug string, comment newComment) {
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM comment_threads WHERE id=$1 AND slug=$2)", comment.Reply, slug).Scan(&exists); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if !exists {
		httpError(w, fmt.Errorf("thread not found"), http.StatusBadRequest)
		return
	}
	if _, err := db.Exec("INSERT INTO comment_entries (thread_id, author, text) VALUES ($1,$2,$3)", comment.Reply, comment.Author, comment.Text); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "id": comment.Reply})
}

func postCommentThread(w http.ResponseWriter, slug string, comment newComment) {
	id, err := randomID()
	if err != nil {
		httpError(w, fmt.Errorf("generate comment id: %w", err), http.StatusInternalServerError)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO comment_threads (id, slug, quote, prefix, suffix) VALUES ($1,$2,$3,$4,$5)",
		id, slug, comment.Anchor.Quote, comment.Anchor.Prefix, comment.Anchor.Suffix); err != nil {
		httpError(w, err, 500)
		return
	}
	if _, err := tx.Exec("INSERT INTO comment_entries (thread_id, author, text) VALUES ($1,$2,$3)", id, comment.Author, comment.Text); err != nil {
		httpError(w, err, 500)
		return
	}
	if err := tx.Commit(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "id": id})
}

const idChars = "0123456789abcdefghijklmnopqrstuvwxyz"

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// randomID uses operating-system entropy so thread IDs cannot be predicted or
// deliberately collided by another commenter.
func randomID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	id := make([]byte, len(random))
	for i, value := range random {
		id[i] = idChars[int(value)%len(idChars)]
	}
	return string(id), nil
}

func handleReindex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	if !requireEditAccess(w, r) {
		return
	}
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
	if !requireEditAccess(w, r) {
		return
	}
	rows, err := db.Query(
		"UPDATE documents SET category_path='' WHERE published AND NOT category_manual RETURNING slug")
	if err != nil {
		httpError(w, err, 500)
		return
	}
	defer rows.Close()
	queued := 0
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			enqueueInsert(slug)
			queued++
		}
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]int{"queued": queued})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v interface{}) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode JSON response: %v", err)
	}
}

func httpError(w http.ResponseWriter, err error, code int) {
	message := err.Error()
	if code >= http.StatusInternalServerError {
		log.Printf("internal HTTP error: %v", err)
		message = "internal server error"
	}
	writeJSONStatus(w, code, map[string]string{"error": message})
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
