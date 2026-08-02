package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mmcdole/gofeed"
)

const rssFixture = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Test Feed</title>
<item>
<title>First Post</title>
<link>https://example.com/first</link>
<guid>urn:1</guid>
<pubDate>Sat, 01 Aug 2026 10:00:00 GMT</pubDate>
<description><![CDATA[<p>Hello <b>world</b></p>]]></description>
</item>
</channel></rss>`

func feedServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func stubPollFeedLater(t *testing.T) {
	t.Helper()
	previous := pollFeedLater
	pollFeedLater = func(string) {}
	t.Cleanup(func() { pollFeedLater = previous })
}

func TestItemToSpark(t *testing.T) {
	feedTitle, feedURL := "Test Feed", "https://example.com/feed.xml"

	t.Run("deterministic slug from guid", func(t *testing.T) {
		item := &gofeed.Item{GUID: "urn:1", Title: "First", Link: "https://example.com/first"}
		slug1, _ := itemToSpark(feedTitle, feedURL, "", item)
		slug2, _ := itemToSpark(feedTitle, feedURL, "", item)
		if slug1 != slug2 {
			t.Fatalf("slug not deterministic: %q vs %q", slug1, slug2)
		}
		want := "_sparks/rss/" + feedHash(feedURL) + "/" + feedHash("urn:1")
		if slug1 != want {
			t.Fatalf("slug = %q, want %q", slug1, want)
		}
	})

	t.Run("slug falls back to link then title", func(t *testing.T) {
		noGUID := &gofeed.Item{Title: "T", Link: "https://example.com/x"}
		slug, _ := itemToSpark(feedTitle, feedURL, "", noGUID)
		if !strings.HasSuffix(slug, feedHash("https://example.com/x")) {
			t.Fatalf("link fallback slug = %q", slug)
		}
		titleOnly := &gofeed.Item{Title: "Only Title"}
		slug, _ = itemToSpark(feedTitle, feedURL, "", titleOnly)
		if !strings.HasSuffix(slug, feedHash("Only Title")) {
			t.Fatalf("title fallback slug = %q", slug)
		}
	})

	t.Run("html stripped and whitespace collapsed", func(t *testing.T) {
		item := &gofeed.Item{GUID: "g", Title: "T", Content: "<p>Hello <b>world</b></p>\n<p>again &amp; more</p>"}
		_, markdown := itemToSpark(feedTitle, feedURL, "", item)
		if strings.Contains(markdown, "<p>") || strings.Contains(markdown, "<b>") || strings.Contains(markdown, "&amp;") {
			t.Fatalf("HTML not stripped:\n%s", markdown)
		}
		if !strings.Contains(markdown, "Hello world again & more") {
			t.Fatalf("text mangled:\n%s", markdown)
		}
	})

	t.Run("body truncated to 2000 runes", func(t *testing.T) {
		item := &gofeed.Item{GUID: "g", Title: "T", Description: strings.Repeat("长", 3000)}
		_, markdown := itemToSpark(feedTitle, feedURL, "", item)
		if !strings.Contains(markdown, strings.Repeat("长", 2000)) || strings.Contains(markdown, strings.Repeat("长", 2001)) {
			t.Fatalf("truncation wrong, markdown len = %d runes", len([]rune(markdown)))
		}
	})

	t.Run("markdown parses as a fleeting rss spark", func(t *testing.T) {
		when := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
		item := &gofeed.Item{
			GUID: "urn:1", Title: "First Post", Link: "https://example.com/first",
			Description: "body", PublishedParsed: &when,
		}
		slug, markdown := itemToSpark(feedTitle, feedURL, "", item)
		doc := parseDoc(slug, "", markdown)
		if doc.Kind != "fleeting" || doc.Title != "First Post" || doc.Source != "rss/Test Feed" {
			t.Fatalf("doc = kind:%q title:%q source:%q", doc.Kind, doc.Title, doc.Source)
		}
		if len(doc.Tags) != 1 || doc.Tags[0] != "rss" {
			t.Fatalf("tags = %v", doc.Tags)
		}
		if !strings.Contains(markdown, "原文：https://example.com/first") ||
			!strings.Contains(markdown, "发布：2026-08-01") {
			t.Fatalf("link/date footer missing:\n%s", markdown)
		}
	})

	t.Run("feed description appended after the footer", func(t *testing.T) {
		when := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
		item := &gofeed.Item{
			GUID: "urn:1", Title: "First Post", Link: "https://example.com/first",
			Description: strings.Repeat("内容", 150), PublishedParsed: &when,
		}
		_, markdown := itemToSpark(feedTitle, feedURL, "魔兽世界，关注团本机制设计", item)
		note := strings.Index(markdown, "订阅注：魔兽世界，关注团本机制设计")
		footer := strings.Index(markdown, "发布：2026-08-01")
		if note < 0 || footer < 0 || note < footer {
			t.Fatalf("订阅注 missing or before the footer:\n%s", markdown)
		}
		// with a realistic-length body the note stays outside the excerpt
		// window (first 200 runes) but inside the embedding sample
		if doc := parseDoc("_sparks/rss/x/y", "", markdown); strings.Contains(doc.Excerpt, "订阅注") {
			t.Fatalf("订阅注 leaked into excerpt: %q", doc.Excerpt)
		}
	})

	t.Run("empty feed description adds no note line", func(t *testing.T) {
		item := &gofeed.Item{GUID: "urn:1", Title: "T", Description: "body"}
		_, markdown := itemToSpark(feedTitle, feedURL, "", item)
		if strings.Contains(markdown, "订阅注") {
			t.Fatalf("unexpected 订阅注:\n%s", markdown)
		}
	})
}

func TestSanitizeFeedDescription(t *testing.T) {
	if got := sanitizeFeedDescription(" 魔兽世界，\n关注团本机制设计\r\n "); got != "魔兽世界， 关注团本机制设计" {
		t.Fatalf("single-line = %q", got)
	}
	if got := sanitizeFeedDescription(strings.Repeat("长", 250)); len([]rune(got)) != 200 {
		t.Fatalf("truncation = %d runes, want 200", len([]rune(got)))
	}
	if got := sanitizeFeedDescription(" \t\n "); got != "" {
		t.Fatalf("blank = %q, want empty", got)
	}
}

func TestPollFeed(t *testing.T) {
	t.Run("imports new items", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolatePublicationState(t)
		srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(rssFixture))
		})
		f := feedRow{ID: 1, URL: srv.URL}
		slug, _ := itemToSpark("Test Feed", srv.URL, "", &gofeed.Item{GUID: "urn:1", Title: "First Post"})

		mock.ExpectExec("UPDATE feeds SET title").
			WithArgs("Test Feed", "", "", int64(1)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM documents WHERE slug=$1)")).
			WithArgs(slug).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO documents").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM document_tags WHERE slug=$1")).
			WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO tags").WithArgs("rss").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO document_tags").WithArgs(slug, "rss").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM backlinks WHERE source_slug=$1")).
			WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM embeddings WHERE slug=$1")).
			WithArgs(slug).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		if err := pollFeed(f, srv.Client()); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("304 only touches last_fetched_at", func(t *testing.T) {
		mock := withMockDatabase(t)
		srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("If-None-Match") != `"v1"` {
				t.Errorf("If-None-Match = %q", r.Header.Get("If-None-Match"))
			}
			w.WriteHeader(http.StatusNotModified)
		})
		f := feedRow{ID: 2, URL: srv.URL, ETag: `"v1"`}
		mock.ExpectExec(regexp.QuoteMeta("UPDATE feeds SET last_fetched_at=now() WHERE id=$1")).
			WithArgs(int64(2)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		if err := pollFeed(f, srv.Client()); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("http error lands in last_error", func(t *testing.T) {
		mock := withMockDatabase(t)
		srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		f := feedRow{ID: 3, URL: srv.URL}
		mock.ExpectExec(regexp.QuoteMeta("UPDATE feeds SET last_fetched_at=now(), last_error=$1 WHERE id=$2")).
			WithArgs("feed http status 500", int64(3)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := pollFeed(f, srv.Client())
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Fatalf("err = %v, want http status error", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFeedPollInterval(t *testing.T) {
	previous := feedInterval
	t.Cleanup(func() { feedInterval = previous })

	feedInterval = "45m"
	if got := feedPollInterval(); got != 45*time.Minute {
		t.Fatalf("interval = %v", got)
	}
	feedInterval = "bogus"
	if got := feedPollInterval(); got != 30*time.Minute {
		t.Fatalf("invalid interval = %v, want 30m fallback", got)
	}
	feedInterval = "0"
	if got := feedPollInterval(); got != 0 {
		t.Fatalf("disabled interval = %v, want 0", got)
	}
	if startFeedPoller() {
		t.Fatal("poller started despite MDHUB_FEED_INTERVAL=0")
	}
	feedInterval = "45m"
	if !startFeedPoller() {
		t.Fatal("poller did not start with a positive interval")
	}
}

func TestListFeeds(t *testing.T) {
	mock := withMockDatabase(t)
	feedURL := "https://example.com/feed.xml"
	mock.ExpectQuery("FROM feeds").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "url", "title", "description", "enabled", "last_fetched_at", "last_error", "created_at",
		}).AddRow(int64(1), feedURL, "Test Feed", "魔兽世界，关注团本机制设计", true, time.Unix(100, 0), "boom", time.Unix(50, 0)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM documents WHERE slug LIKE $1")).
		WithArgs("_sparks/rss/" + feedHash(feedURL) + "/%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

	response := httptest.NewRecorder()
	handleFeeds(response, httptest.NewRequest(http.MethodGet, "/api/feeds", nil))

	body := response.Body.String()
	for _, want := range []string{
		`"id":1`, `"title":"Test Feed"`, `"description":"魔兽世界，关注团本机制设计"`, `"enabled":true`,
		`"last_error":"boom"`, `"sparks":7`, `"last_fetched_at":100000`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %d %q", want, response.Code, body)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFeed(t *testing.T) {
	t.Run("subscribes a reachable feed", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		stubPollFeedLater(t)
		srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(rssFixture))
		})
		mock.ExpectQuery("INSERT INTO feeds").
			WithArgs(srv.URL, "Test Feed", "").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(5)))

		request := httptest.NewRequest(http.MethodPost, "/api/feeds",
			strings.NewReader(fmt.Sprintf(`{"url":%q}`, srv.URL)))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeeds(response, request)

		if response.Code != http.StatusCreated ||
			!strings.Contains(response.Body.String(), `"id":5`) ||
			!strings.Contains(response.Body.String(), `"title":"Test Feed"`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("subscribes with a description", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		stubPollFeedLater(t)
		srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(rssFixture))
		})
		mock.ExpectQuery("INSERT INTO feeds").
			WithArgs(srv.URL, "Test Feed", "魔兽世界， 关注团本机制设计").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(6)))

		request := httptest.NewRequest(http.MethodPost, "/api/feeds",
			strings.NewReader(fmt.Sprintf(`{"url":%q,"description":" 魔兽世界，\n关注团本机制设计 "}`, srv.URL)))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeeds(response, request)

		if response.Code != http.StatusCreated {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unparseable feed is 400 with the error message", func(t *testing.T) {
		withMockDatabase(t)
		isolateEditAccess(t)
		srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("this is not a feed"))
		})
		request := httptest.NewRequest(http.MethodPost, "/api/feeds",
			strings.NewReader(fmt.Sprintf(`{"url":%q}`, srv.URL)))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeeds(response, request)

		if response.Code != http.StatusBadRequest ||
			!strings.Contains(response.Body.String(), "parse feed") {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
	})

	t.Run("invalid url is 400", func(t *testing.T) {
		withMockDatabase(t)
		isolateEditAccess(t)
		request := httptest.NewRequest(http.MethodPost, "/api/feeds",
			strings.NewReader(`{"url":"ftp://example.com/feed"}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeeds(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("duplicate url is 409", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(rssFixture))
		})
		mock.ExpectQuery("INSERT INTO feeds").
			WithArgs(srv.URL, "Test Feed", "").
			WillReturnRows(sqlmock.NewRows([]string{"id"})) // ON CONFLICT DO NOTHING

		request := httptest.NewRequest(http.MethodPost, "/api/feeds",
			strings.NewReader(fmt.Sprintf(`{"url":%q}`, srv.URL)))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeeds(response, request)

		if response.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409, body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("edit token required", func(t *testing.T) {
		withMockDatabase(t)
		isolateEditAccess(t)
		request := httptest.NewRequest(http.MethodPost, "/api/feeds",
			strings.NewReader(`{"url":"https://example.com/feed.xml"}`))
		response := httptest.NewRecorder()
		handleFeeds(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.Code)
		}
	})
}

func TestUpdateFeed(t *testing.T) {
	t.Run("toggles enabled", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE feeds SET enabled=$1 WHERE id=$2")).
			WithArgs(false, "1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		request := httptest.NewRequest(http.MethodPost, "/api/feeds/1",
			strings.NewReader(`{"enabled":false}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("description only leaves enabled untouched", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE feeds SET description=$1 WHERE id=$2")).
			WithArgs("关注团本机制设计", "1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		request := httptest.NewRequest(http.MethodPost, "/api/feeds/1",
			strings.NewReader(`{"description":"关注团本机制设计"}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("enabled and description together", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE feeds SET enabled=$1, description=$2 WHERE id=$3")).
			WithArgs(true, "新描述", "1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		request := httptest.NewRequest(http.MethodPost, "/api/feeds/1",
			strings.NewReader(`{"enabled":true,"description":"新描述"}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("empty update is 400", func(t *testing.T) {
		withMockDatabase(t)
		isolateEditAccess(t)
		request := httptest.NewRequest(http.MethodPost, "/api/feeds/1",
			strings.NewReader(`{}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectExec("UPDATE feeds SET enabled").
			WithArgs(true, "99").
			WillReturnResult(sqlmock.NewResult(0, 0))
		request := httptest.NewRequest(http.MethodPost, "/api/feeds/99",
			strings.NewReader(`{"enabled":true}`))
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestHandleFeedPoll(t *testing.T) {
	t.Run("queues a manual poll", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		stubPollFeedLater(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM feeds WHERE id=$1)")).
			WithArgs("1").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		request := httptest.NewRequest(http.MethodPost, "/api/feeds/1/poll", nil)
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("99").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		request := httptest.NewRequest(http.MethodPost, "/api/feeds/99/poll", nil)
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("edit token required", func(t *testing.T) {
		withMockDatabase(t)
		isolateEditAccess(t)
		request := httptest.NewRequest(http.MethodPost, "/api/feeds/1/poll", nil)
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.Code)
		}
	})
}

func TestDeleteFeed(t *testing.T) {
	t.Run("unsubscribes and keeps sparks", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM feeds WHERE id=$1")).
			WithArgs("1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		// no DELETE FROM documents expected: imported sparks stay
		request := httptest.NewRequest(http.MethodDelete, "/api/feeds/1", nil)
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		mock := withMockDatabase(t)
		isolateEditAccess(t)
		mock.ExpectExec("DELETE FROM feeds").
			WithArgs("99").
			WillReturnResult(sqlmock.NewResult(0, 0))
		request := httptest.NewRequest(http.MethodDelete, "/api/feeds/99", nil)
		request.Header.Set("X-MDHub-Edit-Token", "secret")
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("edit token required", func(t *testing.T) {
		withMockDatabase(t)
		isolateEditAccess(t)
		request := httptest.NewRequest(http.MethodDelete, "/api/feeds/1", nil)
		response := httptest.NewRecorder()
		handleFeed(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.Code)
		}
	})
}
