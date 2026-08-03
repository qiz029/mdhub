package main

// Built-in RSS/Atom poller: subscribed feeds are fetched on an interval and
// new entries become sparks (kind='fleeting', source='rss/<feed title>') that
// flow through the same pipeline as every other document — embedding, then
// collisions. Documents are the entry store; dedup is by deterministic slug
// (_sparks/rss/<feed hash>/<entry hash>), so there is no items table.
//
// The poller runs a first round 10s after startup, then on a ticker; feeds
// are fetched serially (politeness + simplicity). A global mutex keeps the
// ticker and manual polls from overlapping. Disabled when
// MDHUB_FEED_INTERVAL is "0". The one-shot -import flow never starts it.
//
// Management API: the whole API is open — authentication, when needed,
// lives at the ingress layer, not inside the app.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

var (
	feedInterval = getEnv("MDHUB_FEED_INTERVAL", "30m") // "0" = poller disabled

	// feedPollMu serializes ticker rounds against manual polls.
	feedPollMu sync.Mutex
	// feedHTTPClient is the production adapter to the shared remote-source
	// safety policy. Tests replace it with an in-memory transport.
	feedHTTPClient = func() *remoteSourceClient {
		return newRemoteSourceClient(feedHTTPTimeout)
	}

	// pollFeedLater runs one feed poll in the background. A var so tests can
	// stub out the goroutine.
	pollFeedLater = func(id string) {
		go func() {
			feedPollMu.Lock()
			defer feedPollMu.Unlock()
			var f feedRow
			err := db.QueryRow("SELECT id, url, etag, last_modified, description FROM feeds WHERE id=$1", id).
				Scan(&f.ID, &f.URL, &f.ETag, &f.LastModified, &f.Description)
			if err != nil {
				log.Printf("load feed %s: %v", id, err)
				return
			}
			if err := pollFeed(f, feedHTTPClient()); err != nil {
				log.Printf("poll feed %s: %v", f.URL, err)
			}
		}()
	}
)

const (
	feedHTTPTimeout = 15 * time.Second
	feedFirstDelay  = 10 * time.Second
	feedMaxNewItems = 20
)

type feedRow struct {
	ID           int64
	URL          string
	ETag         string
	LastModified string
	Description  string
}

// feedPollInterval parses MDHUB_FEED_INTERVAL; "0" (or a zero duration)
// disables the poller, invalid values fall back to 30m.
func feedPollInterval() time.Duration {
	s := strings.TrimSpace(feedInterval)
	if s == "0" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Printf("invalid MDHUB_FEED_INTERVAL %q, using 30m", s)
		return 30 * time.Minute
	}
	return d
}

// startFeedPoller launches the background loop and exposes its lifecycle so
// callers can cancel and wait for it before releasing shared resources.
// Called only from the server branch of main — never from -import.
func startFeedPoller(ctx context.Context) (bool, <-chan struct{}) {
	done := make(chan struct{})
	interval := feedPollInterval()
	if interval <= 0 {
		log.Println("feed poller disabled (MDHUB_FEED_INTERVAL=0)")
		close(done)
		return false, done
	}
	go func() {
		defer close(done)
		first := time.NewTimer(feedFirstDelay)
		defer first.Stop()
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			pollAllFeeds()
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollAllFeeds()
			}
		}
	}()
	log.Printf("feed poller started, interval %s", interval)
	return true, done
}

// pollAllFeeds fetches every enabled feed, one at a time.
func pollAllFeeds() {
	feedPollMu.Lock()
	defer feedPollMu.Unlock()
	rows, err := db.Query("SELECT id, url, etag, last_modified, description FROM feeds WHERE enabled")
	if err != nil {
		log.Printf("list feeds: %v", err)
		return
	}
	var feeds []feedRow
	for rows.Next() {
		var f feedRow
		if err := rows.Scan(&f.ID, &f.URL, &f.ETag, &f.LastModified, &f.Description); err != nil {
			log.Printf("scan feed: %v", err)
			break
		}
		feeds = append(feeds, f)
	}
	rows.Close()

	client := feedHTTPClient()
	for _, f := range feeds {
		if err := pollFeed(f, client); err != nil {
			log.Printf("poll feed %s: %v", f.URL, err)
		}
	}
}

// fetchFeed performs one conditional GET and parses the body. A 304 is not
// an error: it returns the status with a nil feed.
func fetchFeed(client *remoteSourceClient, feedURL, etag, lastModified string) (status int, parsed *gofeed.Feed, newETag, newLastModified string, err error) {
	parsedURL, err := url.Parse(feedURL)
	if err != nil || validateRemoteSourceURL(parsedURL) != nil {
		return 0, nil, "", "", fmt.Errorf("invalid feed url")
	}
	req, err := http.NewRequest("GET", parsedURL.String(), nil)
	if err != nil {
		return 0, nil, "", "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return resp.StatusCode, nil, "", "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return resp.StatusCode, nil, "", "", fmt.Errorf("feed http status %d", resp.StatusCode)
	}
	body, err := readRemoteSourceBody(resp.Body, 8<<20)
	if err != nil {
		return resp.StatusCode, nil, "", "", fmt.Errorf("read feed: %w", err)
	}
	parsed, err = gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return resp.StatusCode, nil, "", "", fmt.Errorf("parse feed: %w", err)
	}
	return resp.StatusCode, parsed, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), nil
}

// pollFeed fetches one feed and imports new items. Fetch/parse failures are
// recorded in last_error and returned; the caller moves on to the next feed.
func pollFeed(f feedRow, client *remoteSourceClient) error {
	status, parsed, etag, lastModified, err := fetchFeed(client, f.URL, f.ETag, f.LastModified)
	if err != nil {
		return recordFeedError(f.ID, err)
	}
	if status == http.StatusNotModified {
		if _, err := db.Exec("UPDATE feeds SET last_fetched_at=now() WHERE id=$1", f.ID); err != nil {
			return err
		}
		return nil
	}

	title := strings.TrimSpace(parsed.Title)
	if _, err := db.Exec(
		"UPDATE feeds SET title=$1, etag=$2, last_modified=$3, last_fetched_at=now(), last_error='' WHERE id=$4",
		title, etag, lastModified, f.ID); err != nil {
		return err
	}
	importFeedItems(f.URL, title, f.Description, parsed.Items)
	return nil
}

// recordFeedError stores the failure on the feed row and returns the cause.
func recordFeedError(id int64, cause error) error {
	if _, err := db.Exec("UPDATE feeds SET last_fetched_at=now(), last_error=$1 WHERE id=$2",
		truncateRunes(cause.Error(), 300), id); err != nil {
		return err
	}
	return cause
}

// feedHash is the short deterministic digest used in spark slugs.
func feedHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:6])
}

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// itemToSpark converts one feed entry into a deterministic spark slug plus
// markdown. The slug only depends on the feed URL and the entry's
// guid/link/title, so refetching the same entry lands on the same slug.
// A non-empty feedDesc is appended as a 订阅注 line at the very end — past
// the excerpt window but inside the embedding sample, so the subscriber's
// angle steers the vector without cluttering the sparks list.
// Pure function.
func itemToSpark(feedTitle, feedURL, feedDesc string, item *gofeed.Item) (slug, markdown string) {
	key := item.GUID
	if key == "" {
		key = item.Link
	}
	if key == "" {
		key = item.Title
	}
	slug = "_sparks/rss/" + feedHash(feedURL) + "/" + feedHash(key)

	title := strings.Join(strings.Fields(item.Title), " ")
	body := item.Content
	if body == "" {
		body = item.Description
	}
	text := html.UnescapeString(htmlTagRe.ReplaceAllString(body, " "))
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 2000 {
		text = string(r[:2000])
	}
	if title == "" {
		title = truncateRunes(text, 60)
	}
	if title == "" {
		title = "未命名条目"
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlQuote(title))
	b.WriteString("type: fleeting\n")
	fmt.Fprintf(&b, "source: rss/%s\n", strings.TrimSpace(feedTitle))
	b.WriteString("tags: [rss]\n---\n\n")
	b.WriteString("# " + title + "\n\n")
	b.WriteString(text + "\n")
	if item.Link != "" {
		b.WriteString("\n原文：" + item.Link + "\n")
	}
	published := item.PublishedParsed
	if published == nil {
		published = item.UpdatedParsed
	}
	if published != nil {
		b.WriteString("发布：" + published.Local().Format("2006-01-02 15:04") + "\n")
	}
	if feedDesc != "" {
		b.WriteString("\n订阅注：" + feedDesc + "\n")
	}
	return slug, b.String()
}

// yamlQuote double-quotes a scalar for the frontmatter block.
func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// importFeedItems stores the newest entries of a fetched feed as sparks,
// skipping slugs that already exist (no re-embedding of known entries).
// New sparks go through publishDocument, so they enter the embed → collide
// chain like any other fleeting note.
func importFeedItems(feedURL, feedTitle, feedDesc string, items []*gofeed.Item) {
	if len(items) > feedMaxNewItems {
		items = items[:feedMaxNewItems]
	}
	for _, item := range items {
		slug, markdown := itemToSpark(feedTitle, feedURL, feedDesc, item)
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM documents WHERE slug=$1)", slug).Scan(&exists); err != nil {
			log.Printf("check feed item %s: %v", slug, err)
			continue
		}
		if exists {
			continue
		}
		if err := publishDocument(parseDoc(slug, "", markdown)); err != nil {
			log.Printf("import feed item %s: %v", slug, err)
		}
	}
}

// ---- management API ----

type feedItem struct {
	ID            int64  `json:"id"`
	URL           string `json:"url"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Enabled       bool   `json:"enabled"`
	LastFetchedAt int64  `json:"last_fetched_at"` // Unix ms, 0 = never fetched
	LastError     string `json:"last_error"`
	Sparks        int    `json:"sparks"`
	CreatedAt     int64  `json:"created_at"` // Unix ms
}

// handleFeeds dispatches /api/feeds: GET lists subscriptions, POST
// subscribes a new feed.
func handleFeeds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		listFeeds(w, r)
	case http.MethodPost:
		createFeed(w, r)
	default:
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
	}
}

func listFeeds(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT id, url, title, description, enabled, last_fetched_at, last_error, created_at
		FROM feeds ORDER BY created_at DESC`)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []feedItem{}
	for rows.Next() {
		var it feedItem
		var fetchedAt sql.NullTime
		var createdAt time.Time
		if err := rows.Scan(&it.ID, &it.URL, &it.Title, &it.Description, &it.Enabled, &fetchedAt, &it.LastError, &createdAt); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		if fetchedAt.Valid {
			it.LastFetchedAt = fetchedAt.Time.UnixMilli()
		}
		it.CreatedAt = createdAt.UnixMilli()
		if err := db.QueryRow("SELECT COUNT(*) FROM documents WHERE slug LIKE $1",
			"_sparks/rss/"+feedHash(it.URL)+"/%").Scan(&it.Sparks); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// createFeed subscribes a feed: the URL is fetched and parsed once up front
// so bad feeds are rejected with the actual error, then the row is inserted
// and a first full poll runs in the background.
func createFeed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCommentBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("invalid body"), http.StatusBadRequest)
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	body.Description = sanitizeFeedDescription(body.Description)
	u, err := url.Parse(body.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		httpError(w, fmt.Errorf("invalid feed url"), http.StatusBadRequest)
		return
	}

	client := feedHTTPClient()
	_, parsed, _, _, err := fetchFeed(client, body.URL, "", "")
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(parsed.Title)

	var id int64
	err = db.QueryRow(
		"INSERT INTO feeds (url, title, description) VALUES ($1,$2,$3) ON CONFLICT (url) DO NOTHING RETURNING id",
		body.URL, title, body.Description).Scan(&id)
	if err == sql.ErrNoRows {
		httpError(w, fmt.Errorf("feed already subscribed"), http.StatusConflict)
		return
	}
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	pollFeedLater(strconv.FormatInt(id, 10))
	writeJSONStatus(w, http.StatusCreated, map[string]interface{}{"id": id, "title": title})
}

// handleFeed dispatches /api/feeds/{id}[/poll] requests.
func handleFeed(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/feeds/"), "/")
	if id == "" {
		handleFeeds(w, r)
		return
	}
	if strings.HasSuffix(id, "/poll") {
		handleFeedPoll(w, r, strings.TrimSuffix(id, "/poll"))
		return
	}
	switch r.Method {
	case http.MethodPost:
		updateFeed(w, r, id)
	case http.MethodDelete:
		deleteFeed(w, r, id)
	default:
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
	}
}

// sanitizeFeedDescription normalizes a subscriber-written feed description:
// single line, trimmed, capped at 200 runes.
func sanitizeFeedDescription(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, 200)
}

// updateFeed partially updates a subscription: body may carry
// {"enabled": bool} and/or {"description": "..."}. Changing the description
// only affects entries fetched afterwards; existing sparks are not rewritten.
func updateFeed(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Enabled     *bool   `json:"enabled"`
		Description *string `json:"description"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCommentBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("invalid body"), http.StatusBadRequest)
		return
	}

	sets := []string{}
	args := []interface{}{}
	if body.Enabled != nil {
		sets = append(sets, fmt.Sprintf("enabled=$%d", len(args)+1))
		args = append(args, *body.Enabled)
	}
	if body.Description != nil {
		sets = append(sets, fmt.Sprintf("description=$%d", len(args)+1))
		args = append(args, sanitizeFeedDescription(*body.Description))
	}
	if len(sets) == 0 {
		httpError(w, fmt.Errorf("nothing to update"), http.StatusBadRequest)
		return
	}
	args = append(args, id)

	result, err := db.Exec(fmt.Sprintf("UPDATE feeds SET %s WHERE id=$%d",
		strings.Join(sets, ", "), len(args)), args...)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if updated, _ := result.RowsAffected(); updated == 0 {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleFeedPoll triggers an immediate background fetch of one feed.
func handleFeedPoll(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM feeds WHERE id=$1)", id).Scan(&exists); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if !exists {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	pollFeedLater(id)
	writeJSON(w, map[string]string{"status": "ok"})
}

// deleteFeed unsubscribes a feed. Imported sparks are kept — once an entry
// is in the vault, it is yours.
func deleteFeed(w http.ResponseWriter, r *http.Request, id string) {
	result, err := db.Exec("DELETE FROM feeds WHERE id=$1", id)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if deleted, _ := result.RowsAffected(); deleted == 0 {
		httpError(w, fmt.Errorf("not found"), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}
