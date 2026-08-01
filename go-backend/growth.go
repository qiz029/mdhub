package main

// Growth visualization: cumulative curves of sparks, collisions, confirmed
// pairs, answered bounties and formal notes, bucketed by calendar day. The
// SQL side only dumps the raw rows; all bucketing happens in bucketGrowth,
// a pure function. Dates use the server's local timezone so they line up
// with the blind box's CURRENT_DATE (file_mtime/created_at are TIMESTAMPTZ).

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"
)

type growthDay struct {
	Date            string `json:"date"`
	SparksNew       int    `json:"sparks_new"`
	SparksTotal     int    `json:"sparks_total"`
	CollisionsNew   int    `json:"collisions_new"`
	CollisionsTotal int    `json:"collisions_total"`
	ConfirmedTotal  int    `json:"confirmed_total"`
	AnsweredTotal   int    `json:"answered_total"`
	NotesTotal      int    `json:"notes_total"`
}

type growthTotals struct {
	Sparks     int `json:"sparks"`
	Collisions int `json:"collisions"`
	Confirmed  int `json:"confirmed"`
	Answered   int `json:"answered"`
	Notes      int `json:"notes"`
}

type growthResponse struct {
	Days   []growthDay  `json:"days"`
	Totals growthTotals `json:"totals"`
}

// growthDoc is the per-document input to bucketGrowth.
type growthDoc struct {
	kind      string
	published bool
	at        time.Time
}

// growthCollision is the per-collision input to bucketGrowth. answeredAt is
// the zero time when the bounty is still open.
type growthCollision struct {
	verdict    string
	answeredBy string
	createdAt  time.Time
	answeredAt time.Time
}

func growthDate(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

// bucketGrowth turns raw document/collision rows into per-day cumulative
// curves. The range runs from the earliest record to today (later events
// extend it); days without events are zero-filled so the curves stay
// continuous. Notes count published kind='note' documents; sparks count by
// kind regardless of publication. Confirmed pairs are bucketed by the
// collision's created_at (a verdict carries no timestamp), answers by
// answered_at.
func bucketGrowth(docs []growthDoc, collisions []growthCollision, now time.Time) growthResponse {
	sparkNew := map[string]int{}
	noteNew := map[string]int{}
	collisionNew := map[string]int{}
	confirmedNew := map[string]int{}
	answeredNew := map[string]int{}

	minDate, maxDate := "", ""
	track := func(d string) {
		if minDate == "" || d < minDate {
			minDate = d
		}
		if d > maxDate {
			maxDate = d
		}
	}

	for _, doc := range docs {
		d := growthDate(doc.at)
		track(d)
		if doc.kind == "fleeting" {
			sparkNew[d]++
		} else if doc.published {
			noteNew[d]++
		}
	}
	for _, c := range collisions {
		d := growthDate(c.createdAt)
		track(d)
		collisionNew[d]++
		if c.verdict == "confirmed" {
			confirmedNew[d]++
		}
		if c.answeredBy != "" {
			ad := d
			if !c.answeredAt.IsZero() {
				ad = growthDate(c.answeredAt)
				track(ad)
			}
			answeredNew[ad]++
		}
	}

	resp := growthResponse{Days: []growthDay{}}
	if minDate == "" {
		return resp
	}

	if today := growthDate(now); today > maxDate {
		maxDate = today
	}
	day, err := time.ParseInLocation("2006-01-02", minDate, time.Local)
	if err != nil {
		return resp
	}

	var sparks, colls, confirmed, answered, notes int
	for d := growthDate(day); d <= maxDate; d = growthDate(day) {
		sparks += sparkNew[d]
		colls += collisionNew[d]
		confirmed += confirmedNew[d]
		answered += answeredNew[d]
		notes += noteNew[d]
		resp.Days = append(resp.Days, growthDay{
			Date:            d,
			SparksNew:       sparkNew[d],
			SparksTotal:     sparks,
			CollisionsNew:   collisionNew[d],
			CollisionsTotal: colls,
			ConfirmedTotal:  confirmed,
			AnsweredTotal:   answered,
			NotesTotal:      notes,
		})
		day = day.AddDate(0, 0, 1)
	}
	resp.Totals = growthTotals{
		Sparks:     sparks,
		Collisions: colls,
		Confirmed:  confirmed,
		Answered:   answered,
		Notes:      notes,
	}
	return resp
}

// handleGrowth serves GET /api/growth — public growth curves for the sparks
// dashboard.
func handleGrowth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	docRows, err := db.Query("SELECT kind, published, file_mtime FROM documents")
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	docs := []growthDoc{}
	for docRows.Next() {
		var d growthDoc
		if err := docRows.Scan(&d.kind, &d.published, &d.at); err != nil {
			docRows.Close()
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		docs = append(docs, d)
	}
	if err := docRows.Err(); err != nil {
		docRows.Close()
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	docRows.Close()

	collRows, err := db.Query("SELECT verdict, answered_by, created_at, answered_at FROM collisions")
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	collisions := []growthCollision{}
	for collRows.Next() {
		var c growthCollision
		var answeredAt sql.NullTime
		if err := collRows.Scan(&c.verdict, &c.answeredBy, &c.createdAt, &answeredAt); err != nil {
			collRows.Close()
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		if answeredAt.Valid {
			c.answeredAt = answeredAt.Time
		}
		collisions = append(collisions, c)
	}
	if err := collRows.Err(); err != nil {
		collRows.Close()
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	collRows.Close()

	writeJSON(w, bucketGrowth(docs, collisions, time.Now()))
}
