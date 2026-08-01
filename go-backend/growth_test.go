package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// useUTC pins the local timezone so growth day bucketing is deterministic.
func useUTC(t *testing.T) {
	t.Helper()
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
}

func TestBucketGrowth(t *testing.T) {
	useUTC(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	t.Run("cross-day accumulation with zero-filled gaps", func(t *testing.T) {
		docs := []growthDoc{
			{kind: "fleeting", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
			{kind: "fleeting", at: time.Date(2026, 8, 1, 23, 30, 0, 0, time.UTC)},
			{kind: "note", published: true, at: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)},
			{kind: "note", published: false, at: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)}, // draft: not counted
		}
		collisions := []growthCollision{
			{verdict: "confirmed", createdAt: time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)},
			{
				verdict: "new", answeredBy: "notes/answer",
				createdAt:  time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC),
				answeredAt: time.Date(2026, 8, 4, 7, 0, 0, 0, time.UTC),
			},
		}

		resp := bucketGrowth(docs, collisions, now)

		if len(resp.Days) != 4 {
			t.Fatalf("days = %d, want 4 (2026-08-01..04): %+v", len(resp.Days), resp.Days)
		}
		day1 := resp.Days[0]
		if day1.Date != "2026-08-01" || day1.SparksNew != 2 || day1.SparksTotal != 2 {
			t.Errorf("day1 = %+v", day1)
		}
		day2 := resp.Days[1]
		if day2.Date != "2026-08-02" || day2.SparksNew != 0 || day2.SparksTotal != 2 ||
			day2.CollisionsNew != 2 || day2.CollisionsTotal != 2 || day2.ConfirmedTotal != 1 {
			t.Errorf("day2 = %+v", day2)
		}
		day3 := resp.Days[2] // gap day: nothing new, totals carried forward
		if day3.Date != "2026-08-03" || day3.SparksNew != 0 || day3.CollisionsNew != 0 ||
			day3.SparksTotal != 2 || day3.CollisionsTotal != 2 || day3.NotesTotal != 1 {
			t.Errorf("day3 = %+v", day3)
		}
		day4 := resp.Days[3]
		if day4.Date != "2026-08-04" || day4.AnsweredTotal != 1 || day4.NotesTotal != 1 {
			t.Errorf("day4 = %+v", day4)
		}

		want := growthTotals{Sparks: 2, Collisions: 2, Confirmed: 1, Answered: 1, Notes: 1}
		if resp.Totals != want {
			t.Errorf("totals = %+v, want %+v", resp.Totals, want)
		}
	})

	t.Run("empty input yields empty days", func(t *testing.T) {
		resp := bucketGrowth(nil, nil, now)
		if len(resp.Days) != 0 || resp.Totals != (growthTotals{}) {
			t.Fatalf("resp = %+v, want empty", resp)
		}
	})

	t.Run("range extends from earliest record to today", func(t *testing.T) {
		docs := []growthDoc{{kind: "fleeting", at: now}} // only record is today
		resp := bucketGrowth(docs, nil, now)
		if len(resp.Days) != 1 || resp.Days[0].Date != "2026-08-04" || resp.Days[0].SparksTotal != 1 {
			t.Fatalf("days = %+v", resp.Days)
		}
	})

	t.Run("answer without answered_at falls back to created date", func(t *testing.T) {
		collisions := []growthCollision{
			{verdict: "new", answeredBy: "notes/x", createdAt: time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)},
		}
		resp := bucketGrowth(nil, collisions, now)
		if resp.Days[0].AnsweredTotal != 1 {
			t.Fatalf("day = %+v, want answered on the created day", resp.Days[0])
		}
	})
}

func TestHandleGrowth(t *testing.T) {
	useUTC(t)
	now := time.Now()
	mock := withMockDatabase(t)
	mock.ExpectQuery("SELECT kind, published, file_mtime FROM documents").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "published", "file_mtime"}).
			AddRow("fleeting", false, now).
			AddRow("note", true, now).
			AddRow("note", false, now))
	mock.ExpectQuery("SELECT verdict, answered_by, created_at, answered_at FROM collisions").
		WillReturnRows(sqlmock.NewRows([]string{"verdict", "answered_by", "created_at", "answered_at"}).
			AddRow("confirmed", "notes/answer", now, now))

	response := httptest.NewRecorder()
	handleGrowth(response, httptest.NewRequest(http.MethodGet, "/api/growth", nil))

	body := response.Body.String()
	if response.Code != http.StatusOK ||
		!strings.Contains(body, `"totals":{"sparks":1,"collisions":1,"confirmed":1,"answered":1,"notes":1}`) {
		t.Fatalf("response = %d %q", response.Code, body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	response = httptest.NewRecorder()
	handleGrowth(response, httptest.NewRequest(http.MethodPost, "/api/growth", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", response.Code)
	}
}
