package main

import (
	"strings"
	"time"
)

// publishDocument owns the complete publication transition after Markdown has
// been parsed: durable document state, synchronous runtime projections,
// Universe invalidation, and asynchronous derived projections. HTTP writes and
// vault imports are adapters to this same transition.
func publishDocument(doc *Document) error {
	if err := upsertDocument(doc); err != nil {
		return err
	}

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
		enqueueEmbed(doc.Slug)
	}
	return nil
}

// removeDocument owns the inverse publication transition. Durable deletion
// happens first; a failed delete leaves all runtime projections untouched.
func removeDocument(slug string) (bool, error) {
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
