package main

import (
	"strings"
	"sync"
	"time"
)

// documentProjectionTransitions serializes the durable commit and its
// synchronous runtime projection. Without this boundary, two successful
// writes can commit in one order and project in the opposite order, leaving
// searchIndex or embedIndex behind PostgreSQL, the source of truth.
var documentProjectionTransitions sync.Mutex

// commitAndProjectDocument is the publication seam for every document-writing
// state machine. store must commit all durable state before returning the
// committed document. A nil document represents an idempotent no-op.
func commitAndProjectDocument(store func() (*Document, error)) error {
	documentProjectionTransitions.Lock()
	defer documentProjectionTransitions.Unlock()

	doc, err := store()
	if err != nil {
		return err
	}
	if doc != nil {
		projectPublishedDocument(doc)
	}
	return nil
}

// publishDocument owns the complete publication transition after Markdown has
// been parsed: durable document state, synchronous runtime projections,
// Universe invalidation, and asynchronous derived projections. HTTP writes and
// vault imports are adapters to this same transition.
func publishDocument(doc *Document) error {
	return commitAndProjectDocument(func() (*Document, error) {
		if err := upsertDocument(doc); err != nil {
			return nil, err
		}
		return doc, nil
	})
}

// projectPublishedDocument updates runtime and asynchronous projections only
// after the caller's durable transaction has committed.
func projectPublishedDocument(doc *Document) {
	mu.Lock()
	delete(embedIndex, doc.Slug)
	if doc.Published {
		searchIndex[doc.Slug] = &searchEntry{
			slug:    doc.Slug,
			title:   doc.Title,
			plain:   strings.ToLower(doc.Content),
			display: doc.Content,
			mtime:   time.Now().UnixMilli(),
		}
	} else {
		delete(searchIndex, doc.Slug)
	}
	mu.Unlock()
	markUniverseDirty()

	// Publish jobs only after the durable and synchronous projections agree.
	// Workers may complete immediately, so enqueueing earlier would race with
	// the stale-vector deletion above.
	if doc.Published {
		if !doc.CategoryManual && doc.CategoryPath == "" {
			enqueueInsert(doc.Slug)
		}
	}
	// Sparks (kind='fleeting') stay out of every public projection but still
	// get embedded so the collision engine can compare them.
	if doc.Published || doc.Kind == "fleeting" {
		enqueueEmbed(doc.Slug)
	}
}

// removeDocument owns the inverse publication transition. Durable deletion
// happens first; a failed delete leaves all runtime projections untouched.
func removeDocument(slug string) (bool, error) {
	documentProjectionTransitions.Lock()
	defer documentProjectionTransitions.Unlock()

	removed, err := deleteStoredDocument(slug)
	if err != nil {
		return false, err
	}

	mu.Lock()
	delete(searchIndex, slug)
	delete(embedIndex, slug)
	mu.Unlock()
	markUniverseDirty()
	return removed, nil
}
