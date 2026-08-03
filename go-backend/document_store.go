package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// upsertDocument commits a document and all of its derived relationships as
// one state transition. Callers may update in-memory projections only after
// this function returns nil.
func upsertDocument(doc *Document) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := upsertDocumentTx(tx, doc); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit document: %w", err)
	}
	return nil
}

// upsertDocumentTx stores the document and all relationship projections in an
// existing transaction. It lets higher-level state machines, such as paper
// publication, commit their own state and the document atomically.
func upsertDocumentTx(tx *sql.Tx, doc *Document) error {
	if _, err := tx.Exec(`
		INSERT INTO documents (slug, file_path, title, content, raw_content, excerpt, word_count, published, kind, source, category_path, category_manual, file_mtime)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now())
		ON CONFLICT (slug) DO UPDATE SET
			title=EXCLUDED.title, content=EXCLUDED.content,
			raw_content=EXCLUDED.raw_content, excerpt=EXCLUDED.excerpt,
			word_count=EXCLUDED.word_count, published=EXCLUDED.published,
			kind=EXCLUDED.kind,
			source=EXCLUDED.source,
			category_manual=EXCLUDED.category_manual,
			category_path = CASE WHEN EXCLUDED.category_path <> '' THEN EXCLUDED.category_path WHEN documents.category_manual AND NOT EXCLUDED.category_manual THEN '' ELSE documents.category_path END,
			file_mtime=now()`,
		doc.Slug, doc.FilePath, doc.Title, doc.Content, doc.RawContent,
		doc.Excerpt, doc.WordCount, doc.Published, doc.Kind, doc.Source, doc.CategoryPath, doc.CategoryManual); err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM document_tags WHERE slug=$1", doc.Slug); err != nil {
		return fmt.Errorf("clear document tags: %w", err)
	}
	for _, tag := range doc.Tags {
		if _, err := tx.Exec("INSERT INTO tags (name) VALUES ($1) ON CONFLICT DO NOTHING", tag); err != nil {
			return fmt.Errorf("upsert tag %q: %w", tag, err)
		}
		if _, err := tx.Exec("INSERT INTO document_tags (slug, tag) VALUES ($1,$2) ON CONFLICT DO NOTHING", doc.Slug, tag); err != nil {
			return fmt.Errorf("link tag %q: %w", tag, err)
		}
	}

	if _, err := tx.Exec("DELETE FROM backlinks WHERE source_slug=$1", doc.Slug); err != nil {
		return fmt.Errorf("clear backlinks: %w", err)
	}
	for _, target := range doc.Backlinks {
		targetSlug, err := resolveStoredSlug(tx, target)
		if err != nil {
			return fmt.Errorf("resolve backlink %q: %w", target, err)
		}
		if targetSlug == "" {
			continue
		}
		if _, err := tx.Exec("INSERT INTO backlinks (source_slug, target_slug) VALUES ($1,$2) ON CONFLICT DO NOTHING", doc.Slug, targetSlug); err != nil {
			return fmt.Errorf("link backlink %q: %w", targetSlug, err)
		}
	}

	// An embedding is a projection of the exact title/content revision being
	// stored. Remove it in the same transaction so a committed document can
	// never be paired with a stale vector. Published documents are re-embedded
	// asynchronously after the transaction commits.
	if _, err := tx.Exec("DELETE FROM embeddings WHERE slug=$1", doc.Slug); err != nil {
		return fmt.Errorf("invalidate document embedding: %w", err)
	}

	return nil
}

func resolveStoredSlug(tx *sql.Tx, ref string) (string, error) {
	var slug string
	err := tx.QueryRow("SELECT slug FROM documents WHERE slug=$1", ref).Scan(&slug)
	if err == nil {
		return slug, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	err = tx.QueryRow(
		"SELECT slug FROM documents WHERE strpos(lower(title), lower($1)) > 0 ORDER BY length(title), slug LIMIT 1",
		ref,
	).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return slug, nil
}

func deleteStoredDocument(slug string) (bool, error) {
	result, err := db.Exec("DELETE FROM documents WHERE slug=$1", slug)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}
